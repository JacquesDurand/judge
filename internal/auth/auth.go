// Package auth provides optional OIDC bearer-token authentication for the HTTP
// API.
//
// When configured (via OIDC_ISSUER / OIDC_AUDIENCE), it validates the JWT in the
// Authorization header against the provider's public keys. The validation is
// self-contained and offline: the server fetches the provider's signing keys
// once (the JWKS, discovered from the issuer's /.well-known/openid-configuration)
// and then verifies each token's signature and claims locally — it never calls
// the provider per request. This is standard OIDC, so it works with Authentik,
// Keycloak, Auth0, or any compliant provider.
//
// When not configured, the middleware is a no-op, so local development needs no
// identity provider.
package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
)

// Middleware validates OIDC bearer tokens. When verifier is nil (see Disabled),
// it lets every request through.
type Middleware struct {
	verifier *oidc.IDTokenVerifier
}

// ctxKey is a private type for context keys set by this package.
type ctxKey int

const subjectKey ctxKey = iota

// Subject returns the authenticated subject (the token's "sub" claim) attached
// to the request context by Wrap, or "" if the request was not authenticated
// (auth disabled). Downstream code (e.g. rate limiting) can use it as a stable
// per-user key.
func Subject(ctx context.Context) string {
	s, _ := ctx.Value(subjectKey).(string)
	return s
}

// Disabled returns a Middleware that lets every request through. Used when no
// OIDC_ISSUER is configured (local development).
func Disabled() *Middleware { return &Middleware{} }

// New builds a Middleware that verifies bearer tokens issued by issuer and
// intended for audience. It performs OIDC discovery — a network call to the
// issuer's /.well-known/openid-configuration — and caches the provider's JWKS,
// so the issuer must be reachable when the server starts.
func New(ctx context.Context, issuer, audience string) (*Middleware, error) {
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery for %q: %w", issuer, err)
	}
	return &Middleware{verifier: provider.Verifier(&oidc.Config{ClientID: audience})}, nil
}

// Enabled reports whether the middleware actually enforces authentication.
func (m *Middleware) Enabled() bool { return m.verifier != nil }

// Wrap returns a handler that rejects requests without a valid bearer token
// (401) before delegating to next. When auth is disabled it returns next
// unchanged.
func (m *Middleware) Wrap(next http.Handler) http.Handler {
	if m.verifier == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, ok := bearerToken(r)
		if !ok {
			unauthorized(w)
			return
		}
		token, err := m.verifier.Verify(r.Context(), raw)
		if err != nil {
			unauthorized(w)
			return
		}
		ctx := context.WithValue(r.Context(), subjectKey, token.Subject)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// bearerToken extracts the token from an "Authorization: Bearer <token>" header.
func bearerToken(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) < len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(h[len(prefix):])
	return token, token != ""
}

// unauthorized writes a 401 with a standard challenge header. The reason is
// deliberately not disclosed to the client (it only helps an attacker); operators
// see failures at the provider.
func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
}
