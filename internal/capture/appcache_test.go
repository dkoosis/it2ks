package capture

import (
	"errors"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

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
	for i := 0; i < 1000; i++ {
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
		return "", errors.New("session gone")
	}
	now := time.Unix(0, 0)
	c := NewAppCache(5*time.Second, resolver, func() time.Time { return now })

	if got := c.Get("sess-1"); got != "unknown" {
		t.Errorf("Get on error = %q, want unknown", got)
	}
}
