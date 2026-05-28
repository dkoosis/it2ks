package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

var (
	errStopLoop          = errors.New("stop")
	errSubscribeRejected = errors.New("subscribe rejected")
	errTransient         = errors.New("transient")
	errDroppedAfterLong  = errors.New("dropped after long run")
)

// TestBackoffResetsAfterLongSuccess: when a run lasted >= resetThreshold
// before erroring, the next retry must wait 1s (initial backoff), not the
// previously escalated value.
func TestBackoffResetsAfterLongSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var sleeps []time.Duration
	now := time.Unix(0, 0)
	advance := func(d time.Duration) { now = now.Add(d) }

	clock := fakeClock{
		now: func() time.Time { return now },
		sleep: func(ctx context.Context, d time.Duration) error {
			sleeps = append(sleeps, d)
			return nil
		},
	}

	call := 0
	connect := func(ctx context.Context) error {
		call++
		switch call {
		case 1, 2, 3:
			// quick failures escalate backoff: 1s, 2s, 4s
			return errTransient
		case 4:
			// long successful run, then error
			advance(45 * time.Second)
			return errDroppedAfterLong
		case 5:
			// after reset, next sleep should be 1s
			cancel()
			return errStopLoop
		}
		return nil
	}

	_ = runWithBackoffLoop(ctx, connect, clock)

	// Expect sleeps: 1s, 2s, 4s, 1s (reset).
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, time.Second}
	if len(sleeps) < len(want) {
		t.Fatalf("not enough sleeps: got %v want >= %v", sleeps, want)
	}
	for i, w := range want {
		if sleeps[i] != w {
			t.Errorf("sleep[%d]=%v want %v (all sleeps=%v)", i, sleeps[i], w, sleeps)
		}
	}
}

// TestPermanentErrorExitsLoop: a permanent error must exit runWithBackoffLoop
// instead of retrying forever.
func TestPermanentErrorExitsLoop(t *testing.T) {
	ctx := t.Context()

	now := time.Unix(0, 0)
	clock := fakeClock{
		now:   func() time.Time { return now },
		sleep: func(ctx context.Context, d time.Duration) error { return nil },
	}

	calls := 0
	connect := func(ctx context.Context) error {
		calls++
		if calls > 5 {
			t.Fatalf("loop did not exit on permanent error (call=%d)", calls)
		}
		return &PermanentError{Err: errSubscribeRejected}
	}

	_ = runWithBackoffLoop(ctx, connect, clock)

	if calls != 1 {
		t.Errorf("permanent error should exit after first call, got %d calls", calls)
	}
}
