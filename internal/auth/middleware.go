package auth

import (
	"context"
	"net/http"
)

type contextKey string

const sessionContextKey contextKey = "session"

// RequireSession is HTTP middleware that validates the X-Session-Token header.
// On success it stores *Session in the request context.
func RequireSession(store *SessionStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := r.Header.Get("X-Session-Token")
			if token == "" {
				writeError(w, http.StatusUnauthorized, "missing_token", "X-Session-Token header required")
				return
			}
			sess, ok := store.Get(token)
			if !ok {
				writeError(w, http.StatusUnauthorized, "invalid_token", "session not found or expired")
				return
			}
			ctx := context.WithValue(r.Context(), sessionContextKey, sess)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// SessionFromContext extracts the session injected by RequireSession.
func SessionFromContext(ctx context.Context) (*Session, bool) {
	v := ctx.Value(sessionContextKey)
	if v == nil {
		return nil, false
	}
	s, ok := v.(*Session)
	return s, ok
}
