package middleware

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/bradleygichuru/ytci-go/internal/handler"
)

type CtxKey string

const (
	CtxUserID CtxKey = "user_id"
	CtxRole   CtxKey = "role"
	CtxEmail  CtxKey = "email"
)

func UserID(ctx context.Context) string {
	v, _ := ctx.Value(CtxUserID).(string)
	return v
}

func Role(ctx context.Context) string {
	v, _ := ctx.Value(CtxRole).(string)
	return v
}

func Email(ctx context.Context) string {
	v, _ := ctx.Value(CtxEmail).(string)
	return v
}

type jwk struct {
	Kid       string `json:"kid"`
	Kty       string `json:"kty"`
	Alg       string `json:"alg"`
	N         string `json:"n"`
	E         string `json:"e"`
	Use       string `json:"use"`
	KeyOps    []string `json:"key_ops"`
}

type jwksResponse struct {
	Keys []jwk `json:"keys"`
}

type JWKSCache struct {
	mu        sync.Mutex
	keys      map[string]*rsa.PublicKey
	expiresAt time.Time
	url       string
	ttl       time.Duration
}

func NewJWKSCache(jwksURL string, ttlMinutes int) *JWKSCache {
	return &JWKSCache{
		keys: make(map[string]*rsa.PublicKey),
		url:  jwksURL,
		ttl:  time.Duration(ttlMinutes) * time.Minute,
	}
}

func (c *JWKSCache) getPublicKey(kid string) (*rsa.PublicKey, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if time.Now().Before(c.expiresAt) {
		if key, ok := c.keys[kid]; ok {
			return key, nil
		}
	}

	if err := c.refreshLocked(); err != nil {
		if len(c.keys) > 0 {
			if key, ok := c.keys[kid]; ok {
				return key, nil
			}
		}
		return nil, fmt.Errorf("failed to refresh JWKS: %w", err)
	}

	key, ok := c.keys[kid]
	if !ok {
		return nil, fmt.Errorf("key %s not found in JWKS", kid)
	}
	return key, nil
}

func (c *JWKSCache) refreshLocked() error {
	slog.Info("refreshing JWKS", "url", c.url)

	resp, err := http.Get(c.url)
	if err != nil {
		return fmt.Errorf("fetch JWKS: %w", err)
	}
	defer resp.Body.Close()

	var jwks jwksResponse
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return fmt.Errorf("decode JWKS: %w", err)
	}

	newKeys := make(map[string]*rsa.PublicKey)
	for _, j := range jwks.Keys {
		if j.Kty != "RSA" {
			continue
		}
		key, err := j.toPublicKey()
		if err != nil {
			slog.Warn("failed to parse JWK key", "kid", j.Kid, "error", err)
			continue
		}
		newKeys[j.Kid] = key
	}

	c.keys = newKeys
	c.expiresAt = time.Now().Add(c.ttl)
	slog.Info("JWKS refreshed", "key_count", len(newKeys))
	return nil
}

func (j *jwk) toPublicKey() (*rsa.PublicKey, error) {
	nBytes, err := base64URLDecode(j.N)
	if err != nil {
		return nil, fmt.Errorf("decode n: %w", err)
	}
	eBytes, err := base64URLDecode(j.E)
	if err != nil {
		return nil, fmt.Errorf("decode e: %w", err)
	}

	var e int
	for _, b := range eBytes {
		e = (e << 8) | int(b)
	}

	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(nBytes),
		E: e,
	}, nil
}

func base64URLDecode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}

func JWTAuth(cfg *JWKSCache, expectedIssuer, expectedAudience string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenStr := extractBearerToken(r)
			if tokenStr == "" {
				handler.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing authorization header")
				return
			}

			opts := []jwt.ParserOption{
				jwt.WithValidMethods([]string{"RS256"}),
			}
			if expectedIssuer != "" {
				opts = append(opts, jwt.WithIssuer(expectedIssuer))
			}
			if expectedAudience != "" {
				opts = append(opts, jwt.WithAudience(expectedAudience))
			}

			token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
				if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
					return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
				}

				kid, ok := token.Header["kid"].(string)
				if !ok {
					return nil, fmt.Errorf("missing kid in JWT header")
				}

				return cfg.getPublicKey(kid)
			}, opts...)
			if err != nil {
				handler.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", fmt.Sprintf("invalid token: %v", err))
				return
			}

			claims, ok := token.Claims.(jwt.MapClaims)
			if !ok {
				handler.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid token claims")
				return
			}

			ctx := r.Context()
			if sub, ok := claims["sub"].(string); ok {
				ctx = context.WithValue(ctx, CtxUserID, sub)
			}
			if role, ok := claims["role"].(string); ok {
				ctx = context.WithValue(ctx, CtxRole, role)
			}
			if email, ok := claims["email"].(string); ok {
				ctx = context.WithValue(ctx, CtxEmail, email)
			}

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func extractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if len(auth) < 7 || auth[:7] != "Bearer " {
		return ""
	}
	return auth[7:]
}

func AdminGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role := Role(r.Context())
		switch role {
		case "super_admin", "administrator", "moderator":
			next.ServeHTTP(w, r)
		default:
			handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "admin access required")
		}
	})
}

func AuthGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if UserID(r.Context()) == "" {
			handler.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}
