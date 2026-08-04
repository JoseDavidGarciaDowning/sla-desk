package app

import "net/http"

// preflightMaxAge caps how long a browser may cache a preflight result. Ten
// minutes keeps the extra round trip rare without pinning a stale policy.
const preflightMaxAge = "600"

// corsMiddleware permits exactly one browser origin.
//
// The web app is served from Vercel and this API from Cloud Run, so they are on
// different origins and CORS is unavoidable. It is configured with a single
// explicit origin: a wildcard would let any site on the internet read
// authenticated responses, and there is no case in this project where that is
// what we want.
//
// Access-Control-Allow-Credentials is deliberately not set. Authentication
// travels in the Authorization header, not in cookies, so the browser never
// needs to attach credentials cross-origin.
func corsMiddleware(allowedOrigin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Unset means no cross-origin access at all — never a wildcard.
			// Locally the dev server proxies, so there is nothing to allow.
			if allowedOrigin == "" || r.Header.Get("Origin") != allowedOrigin {
				next.ServeHTTP(w, r)
				return
			}

			w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)

			// Without this a shared cache can hand one origin's response to
			// another, quietly defeating the restriction.
			w.Header().Add("Vary", "Origin")

			if r.Method == http.MethodOptions {
				w.Header().Set("Access-Control-Allow-Methods",
					"GET, POST, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers",
					"Authorization, Content-Type")
				w.Header().Set("Access-Control-Max-Age", preflightMaxAge)
				w.WriteHeader(http.StatusNoContent)

				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
