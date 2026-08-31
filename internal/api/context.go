package api

import (
	"context"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

// ctxKey is an unexported type for context keys defined by this package,
// per the standard library's own recommendation for avoiding collisions
// between packages that both stash values on a context.Context.
type ctxKey int

const authContextKey ctxKey = iota

// AuthContext is what withAuthContext (auth.go) attaches to every request's
// context after parsing the X-Emby-Authorization/Authorization header (or
// the bare X-Emby-Token fallback) and, if a Token was present, resolving it
// against storage.UserStore.LookupToken.
//
// It is attached unconditionally — on a request with no header, an
// unparseable header, or a token that doesn't resolve, AuthContext is still
// present in the context, just with Authenticated == false. This lets a
// handler that doesn't require auth (e.g. AuthenticateByName, which still
// needs Client/Device/DeviceId/Version off the header to register the
// device the new token belongs to) read the parsed fields without forcing
// a token to be present, while a handler that does require auth checks
// Authenticated (see requireAuth).
//
// Exported so a later slice (sessions, playstate, ...) can pull identity
// out of context via AuthFromContext without re-parsing the header itself.
type AuthContext struct {
	// Client/Device/DeviceID/Version are the MediaBrowser client-identification
	// fields off the header, present whenever the header parses at all —
	// independent of whether Token/Authenticated ended up set.
	Client   string
	Device   string
	DeviceID string
	Version  string

	// Authenticated is true only when Token resolved to a live AuthToken
	// via storage.UserStore.LookupToken (and the User/AuthToken fields
	// below are populated). Check this, not "Token != """ or a nil check
	// on User — an invalid/expired token still leaves Token set to
	// whatever the client sent.
	Authenticated bool
	Token         string
	AuthToken     storage.AuthToken
	User          storage.User
}

func contextWithAuth(ctx context.Context, ac *AuthContext) context.Context {
	return context.WithValue(ctx, authContextKey, ac)
}

// AuthFromContext returns the AuthContext attached by the server's auth
// middleware. ok is false only if called against a context that never went
// through Server.Handler() (e.g. a handler unit-tested in isolation
// without going through the middleware) — real request handling always has
// one, even for unauthenticated requests, so a handler that requires auth
// should check ac.Authenticated, not ok.
func AuthFromContext(ctx context.Context) (ac *AuthContext, ok bool) {
	ac, ok = ctx.Value(authContextKey).(*AuthContext)
	return ac, ok
}
