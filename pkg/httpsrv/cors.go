package httpsrv

import "net/http"

// CORS adds permissive CORS headers suitable for an OSS test fixture.
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS")
		h.Set("Access-Control-Allow-Headers",
			"Authorization, Content-Type, X-API-Key, X-Request-Id")
		h.Set("Access-Control-Expose-Headers", "X-Request-Id")
		h.Set("Access-Control-Max-Age", "600")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
