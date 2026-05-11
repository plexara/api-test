package httpsrv

import "net/http"

// docsHTML is the Redoc viewer for /openapi.json. The Redoc bundle is
// pulled from the unpkg CDN; api-test is a developer-facing fixture so
// the extra dependency is acceptable. Operators who need an offline page
// can override /docs upstream.
const docsHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>api-test &mdash; API reference</title>
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta name="referrer" content="no-referrer">
  <link rel="icon" href="data:,">
  <style>
    body { margin: 0; padding: 0; }
  </style>
</head>
<body>
  <redoc spec-url="/openapi.json"></redoc>
  <script src="https://cdn.redoc.ly/redoc/latest/bundles/redoc.standalone.js"></script>
</body>
</html>
`

// docsHandler serves the embedded Redoc page that points at /openapi.json.
func docsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(docsHTML))
	}
}
