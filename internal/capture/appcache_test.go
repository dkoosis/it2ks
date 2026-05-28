package capture

import (
	"errors"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

var errSessionGone = errors.New("session gone")

func TestAppCache_CachesWithinTTL(t *testing.T) {
	var calls atomic.Int32
	resolver := func(sid string) (string, error) {
		calls.Add(1)
		return "claude", nil
	}
	now := time.Unix(0, 0)
	clock := func() time.Time { return now }

	c := NewAppCache(5*time.Second, resolver, clock)

	if got := c.Get("sess-1"); got != "claude" {
		t.Errorf("first Get = %q, want claude", got)
	}
	if got := c.Get("sess-1"); got != "claude" {
		t.Errorf("second Get = %q, want claude", got)
	}
	if calls.Load() != 1 {
		t.Errorf("resolver called %d times, want 1 (within TTL)", calls.Load())
	}
}

func TestAppCache_RefreshAfterTTL(t *testing.T) {
	var calls atomic.Int32
	resolver := func(sid string) (string, error) {
		calls.Add(1)
		if calls.Load() == 1 {
			return "claude", nil
		}
		return "vim", nil
	}
	now := time.Unix(0, 0)
	clock := func() time.Time { return now }

	c := NewAppCache(5*time.Second, resolver, clock)
	_ = c.Get("sess-1")

	now = now.Add(6 * time.Second)
	if got := c.Get("sess-1"); got != "vim" {
		t.Errorf("after TTL Get = %q, want vim", got)
	}
	if calls.Load() != 2 {
		t.Errorf("resolver calls = %d, want 2", calls.Load())
	}
}

func TestAppCache_EvictsStaleEntries(t *testing.T) {
	resolver := func(sid string) (string, error) {
		return "claude", nil
	}
	now := time.Unix(0, 0)
	clock := func() time.Time { return now }

	ttl := 5 * time.Second
	c := NewAppCache(ttl, resolver, clock)

	// Populate many distinct sessions, then jump well past the eviction
	// horizon (TTL × 10) and Get again. Stale entries should be dropped
	// lazily so the map does not grow unbounded.
	for i := range 1000 {
		_ = c.Get("stale-" + strconv.Itoa(i))
	}

	now = now.Add(ttl * 100)
	_ = c.Get("fresh-sid")

	c.mu.Lock()
	size := len(c.entries)
	c.mu.Unlock()

	if size > 10 {
		t.Errorf("map size after eviction = %d, want <= 10 (stale entries not evicted)", size)
	}
}

func TestAppCache_ResolverErrorReturnsUnknown(t *testing.T) {
	resolver := func(sid string) (string, error) {
		return "", errSessionGone
	}
	now := time.Unix(0, 0)
	c := NewAppCache(5*time.Second, resolver, func() time.Time { return now })

	if got := c.Get("sess-1"); got != "unknown" {
		t.Errorf("Get on error = %q, want unknown", got)
	}
}

// Resolver returning ("", nil) — e.g., iTerm2 session opened before its child
// process spawned — must not surface app="" to downstream records. Treat as
// a resolver miss and return the same sentinel as the error path.
func TestAppCache_EmptyResolverResultReturnsUnknown(t *testing.T) {
	resolver := func(sid string) (string, error) {
		return "", nil
	}
	now := time.Unix(0, 0)
	c := NewAppCache(5*time.Second, resolver, func() time.Time { return now })

	if got := c.Get("sess-1"); got != "unknown" {
		t.Errorf("Get on empty success = %q, want unknown", got)
	}
}

// Resolver errors must be negatively cached for a short TTL so a burst of
// keystrokes from a dead/erroring session does not produce a websocket storm
// (one resolver round-trip per event). The bd-it2ks-f7u acceptance: 10 Gets
// within 1s after a resolver error → resolver called at most twice.
func TestAppCache_ResolverErrorIsNegativelyCached(t *testing.T) {
	var calls atomic.Int32
	resolver := func(sid string) (string, error) {
		calls.Add(1)
		return "", errSessionGone
	}
	now := time.Unix(0, 0)
	c := NewAppCache(5*time.Second, resolver, func() time.Time { return now })

	for i := range 10 {
		if got := c.Get("sess-dead"); got != "unknown" {
			t.Errorf("Get #%d on error = %q, want unknown", i, got)
		}
	}
	if got := calls.Load(); got > 2 {
		t.Errorf("resolver calls = %d, want <= 2 (negative cache should suppress storm)", got)
	}
}

// After the negative-cache TTL elapses, the resolver must be invoked again so
// a session that starts erroring (transient) and then recovers can be resolved
// to its real app name.
func TestAppCache_ResolverErrorRefreshAfterNegTTL(t *testing.T) {
	var calls atomic.Int32
	resolver := func(sid string) (string, error) {
		calls.Add(1)
		if calls.Load() == 1 {
			return "", errSessionGone
		}
		return "claude", nil
	}
	now := time.Unix(0, 0)
	clock := func() time.Time { return now }
	c := NewAppCache(5*time.Second, resolver, clock)

	if got := c.Get("sess-1"); got != "unknown" {
		t.Errorf("first Get = %q, want unknown", got)
	}

	// Advance past the negative-cache TTL. Use a generous jump so the test
	// stays correct even if negTTL is tuned within the 1–2s acceptance range.
	now = now.Add(10 * time.Second)

	if got := c.Get("sess-1"); got != "claude" {
		t.Errorf("Get after negTTL = %q, want claude (resolver must be reinvoked)", got)
	}
	if calls.Load() != 2 {
		t.Errorf("resolver calls = %d, want 2", calls.Load())
	}
}

// Empty resolver result must not be cached: a subsequent Get should retry the
// resolver in case the child process has since spawned.
func TestAppCache_EmptyResolverResultNotCached(t *testing.T) {
	var calls atomic.Int32
	resolver := func(sid string) (string, error) {
		calls.Add(1)
		if calls.Load() == 1 {
			return "", nil
		}
		return "claude", nil
	}
	now := time.Unix(0, 0)
	c := NewAppCache(5*time.Second, resolver, func() time.Time { return now })

	if got := c.Get("sess-1"); got != "unknown" {
		t.Errorf("first Get = %q, want unknown", got)
	}
	if got := c.Get("sess-1"); got != "claude" {
		t.Errorf("second Get = %q, want claude (empty must not be cached)", got)
	}
	if calls.Load() != 2 {
		t.Errorf("resolver calls = %d, want 2", calls.Load())
	}
}
