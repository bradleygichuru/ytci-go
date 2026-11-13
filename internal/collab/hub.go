package collab

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	NotifyChannel = "itinerary_events"

	heartbeatInterval   = 15 * time.Second
	presenceStaleAfter  = 90 * time.Second
	presenceExpireAfter = 2 * time.Minute
	cleanupInterval     = 60 * time.Second

	maxStreamsPerUser = 3
	maxStreamsTotal   = 200
	subscriberBuffer  = 16
)

var (
	ErrTooManyStreams = errors.New("too many open trip streams")
)

// Querier is satisfied by *pgxpool.Pool and pgx.Tx.
type Querier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

// Event is an invalidation hint, never state. The client refetches.
type Event struct {
	Type        string    `json:"type"` // change | presence | shutdown
	ItineraryID string    `json:"itineraryId,omitempty"`
	Scope       string    `json:"scope,omitempty"`  // plan | budget | members
	Action      string    `json:"action,omitempty"` // updated | deleted | ...
	Version     int       `json:"version,omitempty"`
	ActorID     string    `json:"actorId,omitempty"`
	Viewers     []Viewer  `json:"viewers,omitempty"`
	ServerTime  time.Time `json:"serverTime,omitempty"`
}

type Viewer struct {
	UserID     string    `json:"userId"`
	Name       string    `json:"name"`
	Scope      string    `json:"scope"`
	LastSeenAt time.Time `json:"lastSeenAt"`
}

type Subscription struct {
	ItineraryID string
	UserID      string
	Events      chan Event

	hub *Hub
}

type Hub struct {
	pool *pgxpool.Pool

	mu         sync.Mutex
	subs       map[string]map[*Subscription]struct{}
	userCounts map[string]int
	total      int

	ctx    context.Context
	cancel context.CancelFunc
	once   sync.Once
	wg     sync.WaitGroup

	notifyConn *pgxpool.Conn
}

func NewHub(pool *pgxpool.Pool) *Hub {
	ctx, cancel := context.WithCancel(context.Background())
	return &Hub{
		pool:       pool,
		subs:       make(map[string]map[*Subscription]struct{}),
		userCounts: make(map[string]int),
		ctx:        ctx,
		cancel:     cancel,
	}
}

// Start begins the Postgres LISTEN bridge and presence cleanup. Safe to call
// more than once.
func (h *Hub) Start() {
	h.once.Do(func() {
		if h.pool == nil {
			return
		}
		h.wg.Add(2)
		go h.listenLoop()
		go h.cleanupLoop()
	})
}

func (h *Hub) Done() <-chan struct{} {
	return h.ctx.Done()
}

func (h *Hub) Shutdown() {
	h.cancel()
	h.mu.Lock()
	conn := h.notifyConn
	h.notifyConn = nil
	h.mu.Unlock()
	if conn != nil {
		_ = conn.Conn().Close(context.Background())
	}

	h.mu.Lock()
	for _, set := range h.subs {
		for sub := range set {
			select {
			case sub.Events <- Event{Type: "shutdown", ServerTime: time.Now().UTC()}:
			default:
			}
			close(sub.Events)
		}
	}
	h.subs = make(map[string]map[*Subscription]struct{})
	h.userCounts = make(map[string]int)
	h.total = 0
	h.mu.Unlock()

	h.wg.Wait()
}

func (h *Hub) Subscribe(itineraryID, userID string) (*Subscription, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.total >= maxStreamsTotal || h.userCounts[userID] >= maxStreamsPerUser {
		return nil, ErrTooManyStreams
	}

	sub := &Subscription{
		ItineraryID: itineraryID,
		UserID:      userID,
		Events:      make(chan Event, subscriberBuffer),
		hub:         h,
	}
	if h.subs[itineraryID] == nil {
		h.subs[itineraryID] = make(map[*Subscription]struct{})
	}
	h.subs[itineraryID][sub] = struct{}{}
	h.userCounts[userID]++
	h.total++
	return sub, nil
}

func (h *Hub) Unsubscribe(sub *Subscription) {
	if sub == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	set := h.subs[sub.ItineraryID]
	if set == nil {
		return
	}
	if _, ok := set[sub]; !ok {
		return
	}
	delete(set, sub)
	if len(set) == 0 {
		delete(h.subs, sub.ItineraryID)
	}
	h.userCounts[sub.UserID]--
	if h.userCounts[sub.UserID] <= 0 {
		delete(h.userCounts, sub.UserID)
	}
	h.total--
	close(sub.Events)
}

func (h *Hub) broadcast(ev Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.subs[ev.ItineraryID] {
		select {
		case sub.Events <- ev:
		default:
			// A slow subscriber is a dropped hint, not a lost write. The next
			// refetch or polling cycle heals it.
		}
	}
}

func (h *Hub) listenLoop() {
	defer h.wg.Done()

	backoff := time.Second
	for {
		select {
		case <-h.ctx.Done():
			return
		default:
		}

		conn, err := h.pool.Acquire(h.ctx)
		if err != nil {
			if !h.sleep(backoff) {
				return
			}
			backoff = minDuration(backoff*2, 30*time.Second)
			continue
		}

		if _, err := conn.Exec(h.ctx, "LISTEN "+NotifyChannel); err != nil {
			conn.Release()
			if !h.sleep(backoff) {
				return
			}
			backoff = minDuration(backoff*2, 30*time.Second)
			continue
		}

		h.mu.Lock()
		h.notifyConn = conn
		h.mu.Unlock()
		slog.Info("collab: listening", "channel", NotifyChannel)
		backoff = time.Second

		healthySince := time.Now()
		for {
			notification, err := conn.Conn().WaitForNotification(h.ctx)
			if err != nil {
				break
			}
			if time.Since(healthySince) > 30*time.Second {
				backoff = time.Second
				healthySince = time.Now()
			}
			var ev Event
			if err := json.Unmarshal([]byte(notification.Payload), &ev); err != nil {
				slog.Warn("collab: bad notification", "error", err)
				continue
			}
			if ev.Type == "" {
				continue
			}
			if ev.Type == "shutdown" {
				continue
			}
			h.broadcast(ev)
		}

		h.mu.Lock()
		if h.notifyConn == conn {
			h.notifyConn = nil
		}
		h.mu.Unlock()
		conn.Release()

		if !h.sleep(backoff) {
			return
		}
		backoff = minDuration(backoff*2, 30*time.Second)
	}
}

func (h *Hub) sleep(d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-h.ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (h *Hub) cleanupLoop() {
	defer h.wg.Done()
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-h.ctx.Done():
			return
		case <-ticker.C:
			if err := CleanupPresence(h.ctx, h.pool); err != nil {
				slog.Warn("collab: presence cleanup", "error", err)
			}
		}
	}
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

// Publish emits an invalidation hint through Postgres. Call inside the same
// transaction as the write so NOTIFY is delivered only on commit.
func Publish(ctx context.Context, q Querier, ev Event) error {
	ev.ServerTime = time.Now().UTC()
	payload, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if len(payload) > 7900 {
		// Presence rosters are small; this is a guard against accidentally
		// putting a large payload on NOTIFY.
		ev.Viewers = nil
		payload, _ = json.Marshal(ev)
	}
	_, err = q.Exec(ctx, `SELECT pg_notify($1, $2)`, NotifyChannel, string(payload))
	return err
}

func PublishChange(ctx context.Context, q Querier, itineraryID, scope, action string, version int, actorID string) error {
	return Publish(ctx, q, Event{
		Type:        "change",
		ItineraryID: itineraryID,
		Scope:       scope,
		Action:      action,
		Version:     version,
		ActorID:     actorID,
	})
}

func UpsertPresence(ctx context.Context, q Querier, itineraryID, userID, scope string) ([]Viewer, error) {
	if scope == "" {
		scope = "plan"
	}
	_, err := q.Exec(ctx,
		`INSERT INTO itinerary_presence (itinerary_id, user_id, display_name, scope, last_seen_at)
		 SELECT $1, u.id, u.name, $3, now()
		 FROM users u WHERE u.id = $2
		 ON CONFLICT (itinerary_id, user_id)
		 DO UPDATE SET display_name = EXCLUDED.display_name,
		               scope = EXCLUDED.scope,
		               last_seen_at = now()`,
		itineraryID, userID, scope)
	if err != nil {
		return nil, err
	}
	return Roster(ctx, q, itineraryID)
}

func DeletePresence(ctx context.Context, q Querier, itineraryID, userID string) error {
	_, err := q.Exec(ctx,
		`DELETE FROM itinerary_presence WHERE itinerary_id = $1 AND user_id = $2`,
		itineraryID, userID)
	return err
}

func Roster(ctx context.Context, q Querier, itineraryID string) ([]Viewer, error) {
	rows, err := q.Query(ctx,
		`SELECT p.user_id, p.display_name, p.scope, p.last_seen_at
		 FROM itinerary_presence p
		 WHERE p.itinerary_id = $1 AND p.last_seen_at > now() - interval '90 seconds'
		 ORDER BY p.last_seen_at DESC, p.display_name`,
		itineraryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	viewers := make([]Viewer, 0)
	for rows.Next() {
		var v Viewer
		if err := rows.Scan(&v.UserID, &v.Name, &v.Scope, &v.LastSeenAt); err != nil {
			return nil, err
		}
		viewers = append(viewers, v)
	}
	return viewers, rows.Err()
}

func PublishPresence(ctx context.Context, q Querier, itineraryID string) error {
	viewers, err := Roster(ctx, q, itineraryID)
	if err != nil {
		return err
	}
	return Publish(ctx, q, Event{
		Type:        "presence",
		ItineraryID: itineraryID,
		Viewers:     viewers,
	})
}

func CleanupPresence(ctx context.Context, q Querier) error {
	_, err := q.Exec(ctx,
		`DELETE FROM itinerary_presence WHERE last_seen_at < now() - interval '2 minutes'`)
	return err
}
