package capture

import (
	"sync"
	"time"
)

// Resolver returns the foreground process name for a session ID.
type Resolver func(sessionID string) (string, error)

// evictionFactor controls lazy eviction: entries older than TTL × this factor
// are dropped on Get to bound memory across long-lived daemon runs with churn
// from ephemeral iTerm sessions (tmux, repeated tab open/close).
const evictionFactor = 10

// AppCache caches session → app mappings, refreshing each entry after TTL.
// Used to avoid querying iTerm2 for `jobName` on every keystroke.
//
// Designed for a single Get caller (the capture loop). Mutex protects the map
// but Get is not strictly serializable across goroutines: two concurrent Gets
// on the same expired key may both invoke the resolver and the later write
// wins. Acceptable here since the resolver returns the same value either way.
//
// Memory: Get performs lazy eviction, dropping entries older than
// TTL × evictionFactor. Bounds map size for long-running daemons without
// requiring an explicit Forget(sid) hook on iTerm session-end notifications.
type AppCache struct {
	ttl      time.Duration
	resolver Resolver
	now      func() time.Time

	mu      sync.Mutex
	entries map[string]appEntry
}

type appEntry struct {
	app       string
	fetchedAt time.Time
}

func NewAppCache(ttl time.Duration, resolver Resolver, now func() time.Time) *AppCache {
	return &AppCache{
		ttl:      ttl,
		resolver: resolver,
		now:      now,
		entries:  map[string]appEntry{},
	}
}

// Get returns the cached app name for sessionID, refreshing it if stale.
// Returns "unknown" if the resolver errors.
func (c *AppCache) Get(sessionID string) string {
	c.mu.Lock()
	now := c.now()
	c.evictLocked(now)
	e, ok := c.entries[sessionID]
	fresh := ok && now.Sub(e.fetchedAt) < c.ttl
	c.mu.Unlock()

	if fresh {
		return e.app
	}

	app, err := c.resolver(sessionID)
	if err != nil {
		return "unknown"
	}

	c.mu.Lock()
	c.entries[sessionID] = appEntry{app: app, fetchedAt: c.now()}
	c.mu.Unlock()

	return app
}

// evictLocked drops entries older than TTL × evictionFactor.
// Caller must hold c.mu.
func (c *AppCache) evictLocked(now time.Time) {
	horizon := c.ttl * evictionFactor
	for sid, e := range c.entries {
		if now.Sub(e.fetchedAt) > horizon {
			delete(c.entries, sid)
		}
	}
}
