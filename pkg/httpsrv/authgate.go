package httpsrv

import (
	"net/http"
	"strings"
)

// hasCredential reports whether the request carries something the inbound
// auth chain might recognize: an X-API-Key header or an Authorization:
// Bearer token. Used by the portal auth middleware to decide whether to
// run the (relatively expensive) chain at all on requests that lack any
// credential, and to gate the script-client fallback path.
func hasCredential(r *http.Request) bool {
	if r.Header.Get("X-API-Key") != "" {
		return true
	}
	a := r.Header.Get("Authorization")
	if a == "" {
		return false
	}
	return strings.HasPrefix(strings.ToLower(a), "bearer ")
}
