package capture

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/tmc/it2/proto"
)

var errInjectedWriteFailure = errors.New("injected write failure")

// recordingWriter captures every (time, line) pair written.
type recordingWriter struct {
	mu    sync.Mutex
	times []time.Time
	lines []string
	// failHeader: when non-nil and returns true, this Write call fails.
	failHeader func(line string) bool
}

func (r *recordingWriter) Write(t time.Time, line []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := string(line)
	if r.failHeader != nil && r.failHeader(s) {
		// Reset so subsequent writes succeed.
		r.failHeader = nil
		return errInjectedWriteFailure
	}
	r.times = append(r.times, t)
	r.lines = append(r.lines, s)
	return nil
}

func (r *recordingWriter) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.lines))
	copy(out, r.lines)
	return out
}

func ksNotif(sid string) *pb.Notification {
	s := sid
	keycode := int32(65)
	action := pb.KeystrokeNotification_KEY_DOWN
	return &pb.Notification{
		KeystrokeNotification: &pb.KeystrokeNotification{
			Session: &s,
			KeyCode: &keycode,
			Action:  &action,
		},
	}
}

// Test mty: clock ticks across midnight between capture's read and writer's
// write. Both header and event must land in the same dated bucket (the day
// capture observed); no index collision.
func TestBundle_SingleClockPerEvent_NoMidnightSplit(t *testing.T) {
	// Capture reads t0 = 23:59:59.999 on day N. Writer must NOT re-read clock
	// (since under the new contract it accepts the time explicitly).
	dayN := time.Date(2026, 5, 27, 23, 59, 59, int(900*time.Millisecond), time.UTC)

	notifs := make(chan *pb.Notification, 2)
	notifs <- ksNotif("sid-A")
	close(notifs)

	rw := &recordingWriter{}
	tbl := NewSessionTable()

	deps := Deps{
		Notifications: notifs,
		Writer:        rw,
		Sessions:      tbl,
		ResolveApp:    func(string) (string, error) { return "term", nil },
		Filter:        NewFilter(nil, nil),
		Now:           func() time.Time { return dayN },
		MonoStart:     dayN,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = Run(ctx, deps)

	got := rw.snapshot()
	if len(got) != 2 {
		t.Fatalf("expected 2 lines (header+event), got %d: %v", len(got), got)
	}
	for i, ts := range rw.times {
		if !ts.Equal(dayN) {
			t.Errorf("line %d time=%v, want %v (single clock per event)", i, ts, dayN)
		}
	}
}

// Test kke: two sequential Run calls share a SessionTable. With distinct
// sids, the combined output has no duplicate s index.
func TestBundle_SharedSessionTable_AcrossReconnects(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	rw := &recordingWriter{}
	tbl := NewSessionTable()

	mkDeps := func(notifs <-chan *pb.Notification) Deps {
		return Deps{
			Notifications: notifs,
			Writer:        rw,
			Sessions:      tbl,
			ResolveApp:    func(s string) (string, error) { return "app-" + s, nil },
			Filter:        NewFilter(nil, nil),
			Now:           func() time.Time { return now },
			MonoStart:     now,
		}
	}

	// Run 1: sid-A
	n1 := make(chan *pb.Notification, 1)
	n1 <- ksNotif("sid-A")
	close(n1)
	_ = Run(context.Background(), mkDeps(n1))

	// Run 2: sid-B (reconnect)
	n2 := make(chan *pb.Notification, 1)
	n2 <- ksNotif("sid-B")
	close(n2)
	_ = Run(context.Background(), mkDeps(n2))

	// Collect headers and assert distinct s.
	seen := map[int]bool{}
	for _, line := range rw.snapshot() {
		if !strings.Contains(line, `"type":"session"`) {
			continue
		}
		var rec SessionRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("unmarshal session: %v line=%q", err, line)
		}
		if seen[rec.S] {
			t.Fatalf("duplicate s=%d across reconnects: %v", rec.S, rw.snapshot())
		}
		seen[rec.S] = true
	}
	if len(seen) != 2 {
		t.Fatalf("expected 2 distinct headers, got %d: %v", len(seen), rw.snapshot())
	}
}

// Test n56: writer fails on first header write; second event for same sid
// must carry a fresh header (no orphan events referencing a missing s).
func TestBundle_HeaderFailureRollback(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)

	rw := &recordingWriter{
		failHeader: func(line string) bool {
			return strings.Contains(line, `"type":"session"`)
		},
	}
	tbl := NewSessionTable()

	notifs := make(chan *pb.Notification, 2)
	notifs <- ksNotif("sid-A")
	notifs <- ksNotif("sid-A")
	close(notifs)

	deps := Deps{
		Notifications: notifs,
		Writer:        rw,
		Sessions:      tbl,
		ResolveApp:    func(string) (string, error) { return "term", nil },
		Filter:        NewFilter(nil, nil),
		Now:           func() time.Time { return now },
		MonoStart:     now,
	}
	_ = Run(context.Background(), deps)

	got := rw.snapshot()
	// Expect: header (succeeded on retry) + event + event.
	headers := 0
	for _, line := range got {
		if strings.Contains(line, `"type":"session"`) {
			headers++
		}
	}
	if headers != 1 {
		t.Fatalf("expected exactly 1 successful header, got %d: %v", headers, got)
	}
	// Find the s used in the surviving header, ensure events reference it.
	var headerS int
	for _, line := range got {
		if strings.Contains(line, `"type":"session"`) {
			var rec SessionRecord
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			headerS = rec.S
		}
	}
	events := 0
	for _, line := range got {
		if strings.Contains(line, `"type":"session"`) {
			continue
		}
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("unmarshal event: %v line=%q", err, line)
		}
		if ev.S != headerS {
			t.Errorf("event s=%d, want %d (header)", ev.S, headerS)
		}
		events++
	}
	if events == 0 {
		t.Fatalf("no events written: %v", got)
	}
}

// Test fz1: resolver timeout. The ResolveApp wrapper is in cmd/it2ks, but we
// can verify the policy via AppCache + a slow resolver here, asserting the
// capture loop continues processing within timeout + epsilon.
func TestBundle_ResolverTimeout_CaptureLoopContinues(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)

	const resolverDelay = 200 * time.Millisecond
	const timeout = 50 * time.Millisecond

	var resolverCalls atomic.Int32

	// Wrap a slow resolver with a timeout — this is what cmd/it2ks ResolveApp
	// will do after fz1 lands.
	slowResolver := func(sid string) (string, error) {
		resolverCalls.Add(1)
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		done := make(chan string, 1)
		go func() {
			time.Sleep(resolverDelay)
			done <- "would-have-been-term"
		}()
		select {
		case v := <-done:
			return v, nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	notifs := make(chan *pb.Notification, 1)
	notifs <- ksNotif("sid-A")
	close(notifs)

	rw := &recordingWriter{}
	tbl := NewSessionTable()

	deps := Deps{
		Notifications: notifs,
		Writer:        rw,
		Sessions:      tbl,
		ResolveApp:    slowResolver,
		Filter:        NewFilter(nil, nil),
		Now:           func() time.Time { return now },
		MonoStart:     now,
	}

	start := time.Now()
	_ = Run(context.Background(), deps)
	elapsed := time.Since(start)

	if elapsed > timeout+150*time.Millisecond {
		t.Errorf("capture loop blocked: elapsed=%v want ≤ %v", elapsed, timeout+150*time.Millisecond)
	}
	got := rw.snapshot()
	// Header for app="unknown" should appear (resolver timed out → f7u negcache).
	foundUnknown := false
	for _, line := range got {
		if strings.Contains(line, `"app":"unknown"`) {
			foundUnknown = true
		}
	}
	if !foundUnknown {
		t.Errorf("expected unknown-app header on resolver timeout, got: %v", got)
	}
}
