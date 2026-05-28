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

// negTTL is the short TTL used to negatively cache resolver errors. Without
// it, a session that errors on GetVariable (e.g., SESSION_NOT_FOUND race
// between session close and a buffered keystroke notification) re-invokes the
// resolver on every subsequent keystroke, producing a websocket round-trip
// storm that can block the capture loop. Kept small (1s) so a transient
// failure resolves on its own within ~one human reaction time.
const negTTL = 1 * time.Second

// AppCache caches session → app mappings, refreshing each entry after TTL.
// Used to avoid querying iTerm2 for `jobName` on every keystroke.
//
// Designed for a single Get caller (the capture loop). Mutex protects the map
// but Get is not strictly serializable across goroutines: two concurrent Gets
// on the same expired key may both invoke the resolver and the later write
// wins. Acceptable here since the resolver returns the same value either way.
//
// Errors from the resolver are negatively cached for negTTL (a short fixed
// window) so dead/erroring sessions do not generate a per-keystroke storm of
// websocket requests. Empty success results ("", nil) are NOT cached — a
// brand-new tab whose child process hasn't spawned should be retried promptly.
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
	negative  bool // true → resolver errored; app is "unknown", honor negTTL
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
// Returns "unknown" if the resolver errors or returns an empty string.
//
// Resolver errors are negatively cached for negTTL so a burst of keystrokes
// on a dead session does not produce a websocket round-trip per event.
func (c *AppCache) Get(sessionID string) string {
	c.mu.Lock()
	now := c.now()
	c.evictLocked(now)
	e, ok := c.entries[sessionID]
	// Negative entries use the shorter negTTL; positive entries use c.ttl.
	var fresh bool
	if ok {
		if e.negative {
			fresh = now.Sub(e.fetchedAt) < negTTL
		} else {
			fresh = now.Sub(e.fetchedAt) < c.ttl
		}
	}
	c.mu.Unlock()

	if fresh {
		return e.app
	}

	app, err := c.resolver(sessionID)
	if err != nil {
		c.mu.Lock()
		c.entries[sessionID] = appEntry{app: "unknown", fetchedAt: c.now(), negative: true}
		c.mu.Unlock()
		return "unknown"
	}
	// Treat empty resolver result as a miss (iTerm2 returns "" for a session
	// whose child process hasn't spawned yet — e.g., GetVariable("jobName")
	// on a brand-new tab). Surfacing "" downstream produces ambiguous records.
	// Don't cache: a subsequent Get should retry in case the process appears.
	if app == "" {
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
