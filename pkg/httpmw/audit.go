package httpmw

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/plexara/api-test/pkg/audit"
	"github.com/plexara/api-test/pkg/auth/inbound"
	"github.com/plexara/api-test/pkg/endpoints"
)

// AuditOptions tunes the audit middleware.
type AuditOptions struct {
	// CapturePayloads enables writing the audit_payloads sibling row.
	// Default true; set false when only the indexable summary is wanted.
	CapturePayloads bool

	// CaptureHeaders includes request and response headers in the
	// payload row. Default true.
	CaptureHeaders bool

	// MaxPayloadBytes caps per-side body capture. Bodies exceeding the
	// cap are truncated and the matching truncated flag is set.
	// Default 1 MiB.
	MaxPayloadBytes int

	// RedactKeys lists case-insensitive substrings that, when matched
	// against a header name or query param key, replace the value with
	// "[redacted]" before persisting to the payload row.
	RedactKeys []string
}

func (o AuditOptions) withDefaults() AuditOptions {
	if o.MaxPayloadBytes == 0 {
		o.MaxPayloadBytes = 1 << 20
	}
	return o
}

// Audit returns middleware that records an audit.Event (and optional
// Payload) for each request. Uses the registry to derive the route name
// + endpoint group for the event row.
//
// Body capture: request body is read up to MaxPayloadBytes via a
// teeReader so the downstream handler still sees the full body.
// Response body is captured by a buffering ResponseWriter wrapper. Both
// sides flag truncation when the cap is reached.
//
// The Logger interface only requires Log; implementations that want
// to broadcast (AsyncLogger live-tail in M3) are free to do so internally.
func Audit(logger audit.Logger, registry *endpoints.Registry, slogger *slog.Logger, opts AuditOptions) func(http.Handler) http.Handler {
	opts = opts.withDefaults()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ev := audit.NewEvent(r.Method, r.URL.Path)
			ev.RequestID = RequestIDFromContext(r.Context())
			ev.RemoteAddr = r.RemoteAddr
			ev.UserAgent = r.UserAgent()
			if id := inbound.FromContext(r.Context()); id != nil {
				ev.UserSubject = id.Subject
				ev.UserEmail = id.Email
				ev.AuthType = id.AuthType
				ev.APIKeyName = id.KeyName
			}
			if registry != nil {
				ev.EndpointGroup, ev.RouteName = registry.RouteForRequest(r.Method, r.URL.Path)
			}

			// --- Request capture ---
			var (
				reqBodyBuf  bytes.Buffer
				reqBodyCap  = opts.MaxPayloadBytes
				reqOversize bool
			)
			if r.Body != nil && opts.CapturePayloads {
				// teeReader so the handler still gets the full body.
				r.Body = readCloserTee{r: r.Body, w: capWriter{buf: &reqBodyBuf, max: reqBodyCap, oversize: &reqOversize}}
			}

			// --- Response capture ---
			rec := newAuditRecorder(w, opts.MaxPayloadBytes, opts.CapturePayloads)
			next.ServeHTTP(rec, r)

			ev.Status = rec.status
			ev.BytesIn = reqBodyBuf.Len()
			if reqOversize {
				ev.BytesIn = reqBodyCap // approximate; we know we capped
			}
			ev.BytesOut = rec.bytesTotal
			ev.DurationMS = time.Since(start).Milliseconds()
			ev.Success = rec.status >= 200 && rec.status < 400

			if opts.CapturePayloads {
				p := &audit.Payload{
					RequestSizeBytes:    reqBodyBuf.Len(),
					RequestTruncated:    reqOversize,
					RequestRemoteAddr:   r.RemoteAddr,
					RequestContentType:  r.Header.Get("Content-Type"),
					RequestBody:         reqBodyBuf.Bytes(),
					ResponseSizeBytes:   rec.bytesTotal,
					ResponseTruncated:   rec.truncated,
					ResponseContentType: rec.Header().Get("Content-Type"),
					ResponseBody:        rec.body.Bytes(),
					// If this request came in through the portal's
					// /audit/replay handler, the replay marker
					// header carries the original event's ID.
					// Recording it links the replay to its source.
					ReplayedFrom: r.Header.Get(audit.ReplayHeaderName),
				}
				if opts.CaptureHeaders {
					p.RequestHeaders = audit.SanitizeHeaders(r.Header, opts.RedactKeys)
					p.RequestQuery = audit.SanitizeQuery(r.URL.Query(), opts.RedactKeys)
					p.ResponseHeaders = audit.SanitizeHeaders(rec.Header(), opts.RedactKeys)
				}
				ev.Payload = p
			}

			// Log uses a fresh background context so a client disconnect
			// (request ctx cancelled) doesn't strand the audit write.
			if err := logger.Log(context.Background(), *ev); err != nil {
				slogger.Warn("audit log failed", "err", err, "path", ev.Path)
			}
		})
	}
}

// auditRecorder wraps http.ResponseWriter to capture the status code, a
// truncated body buffer, and the total response byte count.
type auditRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool

	body       bytes.Buffer
	max        int
	capture    bool
	truncated  bool
	bytesTotal int
}

func newAuditRecorder(w http.ResponseWriter, maxBytes int, capture bool) *auditRecorder {
	return &auditRecorder{ResponseWriter: w, status: http.StatusOK, max: maxBytes, capture: capture}
}

func (a *auditRecorder) WriteHeader(code int) {
	if a.wroteHeader {
		return
	}
	a.wroteHeader = true
	a.status = code
	a.ResponseWriter.WriteHeader(code)
}

func (a *auditRecorder) Write(b []byte) (int, error) {
	if !a.wroteHeader {
		a.WriteHeader(http.StatusOK)
	}
	if a.capture {
		remaining := a.max - a.body.Len()
		captured := 0
		if remaining > 0 {
			captured = remaining
			if captured > len(b) {
				captured = len(b)
			}
			a.body.Write(b[:captured])
		}
		if captured < len(b) {
			a.truncated = true
		}
	}
	n, err := a.ResponseWriter.Write(b)
	a.bytesTotal += n
	return n, err
}

// readCloserTee is an io.ReadCloser that mirrors reads into w (best-effort,
// errors ignored) so the audit middleware can capture inbound bodies
// without disturbing the downstream handler.
type readCloserTee struct {
	r io.ReadCloser
	w io.Writer
}

func (t readCloserTee) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if n > 0 {
		_, _ = t.w.Write(p[:n])
	}
	return n, err
}

func (t readCloserTee) Close() error { return t.r.Close() }

// capWriter writes up to max bytes into buf, then drops further writes
// and sets *oversize true. The tee always reports the input size so the
// underlying reader keeps draining.
type capWriter struct {
	buf      *bytes.Buffer
	max      int
	oversize *bool
}

func (c capWriter) Write(p []byte) (int, error) {
	remaining := c.max - c.buf.Len()
	if remaining <= 0 {
		*c.oversize = true
		return len(p), nil
	}
	n := remaining
	if n > len(p) {
		n = len(p)
	}
	c.buf.Write(p[:n])
	if n < len(p) {
		*c.oversize = true
	}
	return len(p), nil
}
