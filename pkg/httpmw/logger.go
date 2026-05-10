package httpmw

import (
	"log/slog"
	"net/http"
	"time"
)

// AccessLog returns middleware that emits a structured info-level log
// line per request. Captures the status the response writer ultimately
// wrote (via statusRecorder) and reads the resolved identity from the
// per-request holder seeded by RequestID — `r.WithContext(...)` inside
// the per-route Identity middleware doesn't flow back up here, so this
// path can't use inbound.FromContext directly.
func AccessLog(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := newStatusRecorder(w)
			next.ServeHTTP(rec, r)

			fields := []any{
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"bytes", rec.bytes,
				"duration_ms", time.Since(start).Milliseconds(),
				"request_id", RequestIDFromContext(r.Context()),
			}
			if id := resolvedIdentity(r.Context()); id != nil {
				fields = append(fields,
					"auth_type", id.AuthType,
					"subject", id.Subject,
				)
			}
			logger.Info("request", fields...)
		})
	}
}

// statusRecorder wraps http.ResponseWriter to capture the status code and
// response byte count for logging.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

func newStatusRecorder(w http.ResponseWriter) *statusRecorder {
	return &statusRecorder{ResponseWriter: w, status: http.StatusOK}
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.wroteHeader {
		return
	}
	s.wroteHeader = true
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		s.WriteHeader(http.StatusOK)
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}
