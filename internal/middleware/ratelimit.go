package middleware

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/bradleygichuru/ytci-go/internal/handler"
)

type rateLimiterClient struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

type RateLimiter struct {
	mu      sync.Mutex
	clients map[string]*rateLimiterClient
	rate    rate.Limit
	burst   int
	ttl     time.Duration
}

func NewRateLimiter(r rate.Limit, burst int) *RateLimiter {
	rl := &RateLimiter{
		clients: make(map[string]*rateLimiterClient),
		rate:    r,
		burst:   burst,
		ttl:     5 * time.Minute,
	}
	go rl.cleanup(1 * time.Minute)
	return rl
}

func (rl *RateLimiter) getClient(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	c, ok := rl.clients[ip]
	if !ok {
		limiter := rate.NewLimiter(rl.rate, rl.burst)
		rl.clients[ip] = &rateLimiterClient{limiter: limiter, lastSeen: time.Now()}
		return limiter
	}
	c.lastSeen = time.Now()
	return c.limiter
}

func (rl *RateLimiter) cleanup(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		rl.mu.Lock()
		for ip, c := range rl.clients {
			if time.Since(c.lastSeen) > rl.ttl {
				delete(rl.clients, ip)
			}
		}
		rl.mu.Unlock()
	}
}

func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := r.RemoteAddr
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			if idx := strings.IndexByte(fwd, ','); idx != -1 {
				ip = strings.TrimSpace(fwd[:idx])
			} else {
				ip = fwd
			}
		}

		limiter := rl.getClient(ip)
		if !limiter.Allow() {
			w.Header().Set("Retry-After", "60")
			handler.WriteError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many requests")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (rl *RateLimiter) MiddlewareKeyed(keyFn func(r *http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := keyFn(r)

			limiter := rl.getClient(key)
			if !limiter.Allow() {
				w.Header().Set("Retry-After", "60")
				handler.WriteError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many requests")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func PublicRateLimiter() *RateLimiter {
	return NewRateLimiter(1, 60)
}

func AuthenticatedRateLimiter() *RateLimiter {
	return NewRateLimiter(2, 120)
}
