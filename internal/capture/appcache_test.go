package capture

import (
	"errors"
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
