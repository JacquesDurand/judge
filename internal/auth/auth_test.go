package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

// oidcTestProvider stands in for a real OIDC provider (Authentik, Keycloak, ...).
// It serves the two endpoints the server relies on — the discovery document and
// the JWKS — and can mint signed tokens. This mirrors exactly what a real
// provider does, so the middleware is exercised without any external infra.
type oidcTestProvider struct {
	server *httptest.Server
	key    *rsa.PrivateKey
	keyID  string
}

func newOIDCTestProvider(t *testing.T) *oidcTestProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	p := &oidcTestProvider{key: key, keyID: "test-key-1"}

	jwks := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
		Key:       key.Public(),
		KeyID:     p.keyID,
		Algorithm: string(jose.RS256),
		Use:       "sig",
	}}}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                p.issuer(),
			"jwks_uri":                              p.issuer() + "/jwks",
			"authorization_endpoint":                p.issuer() + "/auth",
			"token_endpoint":                        p.issuer() + "/token",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(jwks)
	})
	p.server = httptest.NewServer(mux)
	t.Cleanup(p.server.Close)
	return p
}

func (p *oidcTestProvider) issuer() string { return p.server.URL }

// token signs a JWT with the given claims. keyID lets a test forge a token
// signed by an unknown key (to check it is rejected).
func (p *oidcTestProvider) token(t *testing.T, claims map[string]any) string {
	t.Helper()
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: p.key, KeyID: p.keyID}},
		(&jose.SignerOptions{}).WithType("JWT"),
	)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	jws, err := signer.Sign(payload)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	tok, err := jws.CompactSerialize()
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	return tok
}

const testAudience = "judge-mobile"

// validClaims returns a set of claims a real provider would issue for our client.
func (p *oidcTestProvider) validClaims() map[string]any {
	now := time.Now()
	return map[string]any{
		"iss": p.issuer(),
		"aud": testAudience,
		"sub": "user-123",
		"exp": now.Add(time.Hour).Unix(),
		"iat": now.Unix(),
	}
}

// okHandler is the protected handler; it writes 200 only if auth let the request
// through.
func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestMiddlewareDisabled(t *testing.T) {
	h := Disabled().Wrap(okHandler())
	if Disabled().Enabled() {
		t.Fatal("Disabled().Enabled() = true, want false")
	}
	req := httptest.NewRequest(http.MethodGet, "/chat", nil) // no Authorization header
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("disabled auth should pass request through: got %d, want 200", rec.Code)
	}
}

func TestMiddlewareEnforcesTokens(t *testing.T) {
	p := newOIDCTestProvider(t)
	mw, err := New(context.Background(), p.issuer(), testAudience)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !mw.Enabled() {
		t.Fatal("mw.Enabled() = false, want true")
	}
	h := mw.Wrap(okHandler())

	expired := p.validClaims()
	expired["exp"] = time.Now().Add(-time.Hour).Unix()

	wrongAud := p.validClaims()
	wrongAud["aud"] = "some-other-client"

	tests := []struct {
		name       string
		authHeader string
		wantStatus int
	}{
		{"valid token", "Bearer " + p.token(t, p.validClaims()), http.StatusOK},
		{"no header", "", http.StatusUnauthorized},
		{"wrong scheme", "Basic " + p.token(t, p.validClaims()), http.StatusUnauthorized},
		{"empty bearer", "Bearer ", http.StatusUnauthorized},
		{"garbage token", "Bearer not-a-jwt", http.StatusUnauthorized},
		{"expired token", "Bearer " + p.token(t, expired), http.StatusUnauthorized},
		{"wrong audience", "Bearer " + p.token(t, wrongAud), http.StatusUnauthorized},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/chat", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if tc.wantStatus == http.StatusUnauthorized {
				if got := rec.Header().Get("WWW-Authenticate"); got == "" {
					t.Errorf("401 response missing WWW-Authenticate header")
				}
			}
		})
	}
}

// TestMiddlewareRejectsForeignKey checks that a token signed by a key the
// provider never published is rejected (signature verification, not just claims).
func TestMiddlewareRejectsForeignKey(t *testing.T) {
	p := newOIDCTestProvider(t)
	mw, err := New(context.Background(), p.issuer(), testAudience)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h := mw.Wrap(okHandler())

	// A second provider with a different signing key, but claiming the first
	// provider's issuer/audience.
	attacker := newOIDCTestProvider(t)
	claims := p.validClaims()           // correct iss/aud for the real provider...
	forged := attacker.token(t, claims) // ...but signed with the attacker's key

	req := httptest.NewRequest(http.MethodPost, "/chat", nil)
	req.Header.Set("Authorization", "Bearer "+forged)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("token signed by unknown key: status = %d, want 401", rec.Code)
	}
}
