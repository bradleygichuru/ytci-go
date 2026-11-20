package collab

import (
	"sync"
	"time"
)

// Limiter is a small fixed-window limiter. It is process-local; the invite
// limits are guardrails, not a billing boundary, and a single Render instance
// is the current deployment. Replace with a database/Redis counter if the API
// ever scales horizontally.
type Limiter struct {
	mu      sync.Mutex
	entries map[string]limitEntry
	limit   int
	window  time.Duration
}

type limitEntry struct {
	count int
	reset time.Time
}

func NewLimiter(limit int, window time.Duration) *Limiter {
	return &Limiter{
		entries: make(map[string]limitEntry),
		limit:   limit,
		window:  window,
	}
}

func (l *Limiter) Allow(key string) bool {
	if l == nil || l.limit <= 0 {
		return true
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	entry, ok := l.entries[key]
	if !ok || now.After(entry.reset) {
		l.entries[key] = limitEntry{count: 1, reset: now.Add(l.window)}
		return true
	}
	if entry.count >= l.limit {
		return false
	}
	entry.count++
	l.entries[key] = entry
	return true
}
