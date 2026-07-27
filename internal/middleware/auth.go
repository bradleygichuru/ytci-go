package middleware

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"

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

func RoleFromCtx(ctx context.Context) Role {
	v, _ := ctx.Value(CtxRole).(string)
	return Role(v)
}

func Email(ctx context.Context) string {
	v, _ := ctx.Value(CtxEmail).(string)
	return v
}

type jwk struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type jwksResponse struct {
	Keys []jwk `json:"keys"`
}

type JWKSCache struct {
	mu     sync.Mutex
	keys   map[string]*rsa.PublicKey
	expiry time.Time
	url    string
	ttl    time.Duration
	client *http.Client
	pool   *pgxpool.Pool
}

func NewJWKSCache(jwksURL string, ttlMinutes int, pool *pgxpool.Pool) *JWKSCache {
	return &JWKSCache{
		keys:   make(map[string]*rsa.PublicKey),
		url:    jwksURL,
		ttl:    time.Duration(ttlMinutes) * time.Minute,
		client: &http.Client{Timeout: 10 * time.Second},
		pool:   pool,
	}
}

func (c *JWKSCache) Ping() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.refreshLocked()
}

func (c *JWKSCache) getPublicKey(kid string) (*rsa.PublicKey, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if time.Now().Before(c.expiry) {
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
		return nil, fmt.Errorf("refresh JWKS: %w", err)
	}

	key, ok := c.keys[kid]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrKidNotFound, kid)
	}
	return key, nil
}

var ErrKidNotFound = fmt.Errorf("key not found in JWKS")

func (c *JWKSCache) refreshLocked() error {
	slog.Info("refreshing JWKS", "url", c.url)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("JWKS endpoint returned %d", resp.StatusCode)
	}

	var jwks jwksResponse
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return fmt.Errorf("decode: %w", err)
	}

	newKeys := make(map[string]*rsa.PublicKey, len(jwks.Keys))
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
	c.expiry = time.Now().Add(c.ttl)
	slog.Info("JWKS refreshed", "key_count", len(newKeys))
	return nil
}

func (c *JWKSCache) lookupWithRefresh(kid string) (*rsa.PublicKey, error) {
	key, err := c.getPublicKey(kid)
	if err == nil {
		return key, nil
	}
	if !errors.Is(err, ErrKidNotFound) {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.refreshLocked(); err != nil {
		return nil, err
	}
	if key, ok := c.keys[kid]; ok {
		return key, nil
	}
	return nil, fmt.Errorf("key %q not found after refresh", kid)
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

			// Session token fallback: if the token has fewer than 3 segments
			// (not a JWT), treat it as a better-auth session token and look up
			// the session in the shared database.
			if cfg.pool != nil && strings.Count(tokenStr, ".") < 2 {
				parts := strings.SplitN(tokenStr, ".", 2)
				sessionID := parts[0]
				var userID, role, email string
				err := cfg.pool.QueryRow(r.Context(),
					`SELECT u.id, u.role, u.email
					 FROM users u
					 JOIN sessions s ON s.user_id = u.id
					 WHERE s.token = $1 AND s.expires_at > now()`,
					sessionID,
				).Scan(&userID, &role, &email)
				if err != nil {
					handler.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid session")
					return
				}
				ctx := r.Context()
				ctx = context.WithValue(ctx, CtxUserID, userID)
				ctx = context.WithValue(ctx, CtxRole, role)
				ctx = context.WithValue(ctx, CtxEmail, email)
				next.ServeHTTP(w, r.WithContext(ctx))
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

			parsed, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
				if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
					return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
				}
				kid, ok := token.Header["kid"].(string)
				if !ok {
					return nil, fmt.Errorf("missing kid in JWT header")
				}
				return cfg.lookupWithRefresh(kid)
			}, opts...)
			if err != nil {
				handler.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", fmt.Sprintf("invalid token: %v", err))
				return
			}

			claims, ok := parsed.Claims.(jwt.MapClaims)
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

type Role string

const (
	RoleSuperAdmin   Role = "super_admin"
	RoleAdmin        Role = "administrator"
	RoleModerator    Role = "moderator"
	RoleCountyOfficer Role = "county_officer"
	RoleUser         Role = "user"
)

var adminRoles = map[Role]bool{
	RoleSuperAdmin: true,
	RoleAdmin:      true,
	RoleModerator:  true,
}

func IsAdmin(role Role) bool {
	return adminRoles[role]
}

func AdminGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		roleStr := RoleFromCtx(r.Context())
		if IsAdmin(roleStr) {
			next.ServeHTTP(w, r)
		} else {
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
