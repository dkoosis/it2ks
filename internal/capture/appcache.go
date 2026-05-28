package capture

import (
	"sync"
	"time"
)

// Resolver returns the foreground process name for a session ID.
type Resolver func(sessionID string) (string, error)

// AppCache caches session → app mappings, refreshing each entry after TTL.
// Used to avoid querying iTerm2 for `jobName` on every keystroke.
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
	e, ok := c.entries[sessionID]
	fresh := ok && c.now().Sub(e.fetchedAt) < c.ttl
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
