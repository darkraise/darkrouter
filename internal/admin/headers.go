package admin

import "net/http"

// contentSecurityPolicy is what the console needs and nothing else. Scripts
// come only from the bundle; inline styles are what the component library
// emits; the two font origins are the only third parties the page reaches.
const contentSecurityPolicy = "default-src 'self'; " +
	"img-src 'self' data:; " +
	"style-src 'self' 'unsafe-inline'; " +
	"style-src-elem 'self' 'unsafe-inline' https://fonts.googleapis.com; " +
	"font-src 'self' https://fonts.gstatic.com; " +
	"connect-src 'self'"

// securityHeaders sets the response headers every admin answer carries,
// including API errors and the SPA fallback. Set before the handler runs so
// a handler that writes its own status still gets them.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		next.ServeHTTP(w, r)
	})
}
