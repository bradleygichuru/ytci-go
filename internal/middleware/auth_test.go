package middleware_test

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/middleware"
)

func base64URLEncode(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

func generateTestKey(t *testing.T) (*rsa.PrivateKey, string) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	n := base64URLEncode(key.PublicKey.N.Bytes())
	e := base64URLEncode(big.NewInt(int64(key.PublicKey.E)).Bytes())

	jwks := fmt.Sprintf(`{
		"keys": [{
			"kty": "RSA",
			"kid": "test-key",
			"alg": "RS256",
			"n": "%s",
			"e": "%s",
			"use": "sig"
		}]
	}`, n, e)

	return key, jwks
}

func signJWT(key *rsa.PrivateKey, claims jwt.MapClaims) string {
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "test-key"
	s, _ := token.SignedString(key)
	return s
}

func TestJWTAuthValidToken(t *testing.T) {
	key, jwksJSON := generateTestKey(t)

	jwksSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(jwksJSON))
	}))
	defer jwksSrv.Close()

	cache := middleware.NewJWKSCache(jwksSrv.URL, 60, nil)
	authmw := middleware.JWTAuth(cache, "", "")

	var userID, role, email string
	ts := httptest.NewServer(authmw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID = middleware.UserID(r.Context())
		role = string(middleware.RoleFromCtx(r.Context()))
		email = middleware.Email(r.Context())
		w.WriteHeader(http.StatusOK)
	})))
	defer ts.Close()

	token := signJWT(key, jwt.MapClaims{
		"sub":   "user-123",
		"role":  "super_admin",
		"email": "admin@test.com",
		"exp":   time.Now().Add(1 * time.Hour).Unix(),
	})

	req, _ := http.NewRequest("GET", ts.URL, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "user-123", userID)
	assert.Equal(t, "super_admin", role)
	assert.Equal(t, "admin@test.com", email)
}

func TestJWTAuthMissingToken(t *testing.T) {
	cache := middleware.NewJWKSCache("http://example.com/jwks", 60, nil)
	authmw := middleware.JWTAuth(cache, "", "")

	ts := httptest.NewServer(authmw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	})))
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestJWTAuthExpiredToken(t *testing.T) {
	key, jwksJSON := generateTestKey(t)

	jwksSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(jwksJSON))
	}))
	defer jwksSrv.Close()

	cache := middleware.NewJWKSCache(jwksSrv.URL, 60, nil)
	authmw := middleware.JWTAuth(cache, "", "")

	ts := httptest.NewServer(authmw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	})))
	defer ts.Close()

	token := signJWT(key, jwt.MapClaims{
		"sub": "user-123",
		"exp": time.Now().Add(-1 * time.Hour).Unix(),
	})

	req, _ := http.NewRequest("GET", ts.URL, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestJWTAuthInvalidSignature(t *testing.T) {
	_, jwksJSON := generateTestKey(t)

	jwksSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(jwksJSON))
	}))
	defer jwksSrv.Close()

	cache := middleware.NewJWKSCache(jwksSrv.URL, 60, nil)
	authmw := middleware.JWTAuth(cache, "", "")

	ts := httptest.NewServer(authmw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	})))
	defer ts.Close()

	wrongKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	token := signJWT(wrongKey, jwt.MapClaims{
		"sub": "user-123",
		"exp": time.Now().Add(1 * time.Hour).Unix(),
	})

	req, _ := http.NewRequest("GET", ts.URL, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestJWTAuthWrongIssuer(t *testing.T) {
	key, jwksJSON := generateTestKey(t)

	jwksSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(jwksJSON))
	}))
	defer jwksSrv.Close()

	cache := middleware.NewJWKSCache(jwksSrv.URL, 60, nil)
	authmw := middleware.JWTAuth(cache, "https://expected.issuer.com", "")

	ts := httptest.NewServer(authmw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	})))
	defer ts.Close()

	token := signJWT(key, jwt.MapClaims{
		"sub": "user-123",
		"iss": "https://wrong.issuer.com",
		"exp": time.Now().Add(1 * time.Hour).Unix(),
	})

	req, _ := http.NewRequest("GET", ts.URL, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}
