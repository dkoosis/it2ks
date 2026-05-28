package capture

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/tmc/it2/proto"
)

// blockingWriter blocks every Write until released. Used to simulate a
// stalled disk so we can prove the capture goroutine doesn't block waiting
// for the writer (it2ks-wm5).
type blockingWriter struct {
	release chan struct{} // closed → unblocks all pending + future writes

	mu    sync.Mutex
	times []time.Time
	lines []string
}

func newBlockingWriter() *blockingWriter {
	return &blockingWriter{release: make(chan struct{})}
}

func (b *blockingWriter) Write(t time.Time, line []byte) error {
	<-b.release // block until released
	b.mu.Lock()
	defer b.mu.Unlock()
	b.times = append(b.times, t)
	b.lines = append(b.lines, string(line))
	return nil
}

func (b *blockingWriter) Release() { close(b.release) }

// Test wm5: a writer that blocks must not back-pressure the notification
// channel. The capture goroutine should drain notifications into the
// internal queue and accept >queueSize events without the ws-side blocking.
func TestBackpressure_SlowWriter_DoesNotBlockWsReader(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)

	const n = defaultQueueSize + 16 // beyond queue capacity
	notifs := make(chan *pb.Notification, n)
	for range n {
		notifs <- ksNotif("sid-A")
	}
	close(notifs)

	bw := newBlockingWriter()
	tbl := NewSessionTable()

	deps := Deps{
		Notifications: notifs,
		Writer:        bw,
		Sessions:      tbl,
		ResolveApp:    func(string) (string, error) { return "term", nil },
		Filter:        NewFilter(nil, nil),
		Now:           func() time.Time { return now },
		MonoStart:     now,
	}

	// Capture must drain notifs quickly even though writer is stalled.
	done := make(chan struct{})
	go func() {
		_ = Run(context.Background(), deps)
		close(done)
	}()

	// Notif chan must drain rapidly (ws-side not blocked). Poll briefly.
	deadline := time.After(2 * time.Second)
	for len(notifs) != 0 {

		select {
		case <-deadline:
			t.Fatalf("ws notification channel did not drain — capture goroutine blocked on writer (remaining=%d)", len(notifs))
		case <-time.After(10 * time.Millisecond):
		}
	}

	// Release writer; Run should exit after channel-closed + drain.
	bw.Release()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("Run did not exit after writer release")
	}
}

// Test wm5: when the internal event queue overflows, the OLDEST queued
// events get dropped and a counter is bumped. Worker is blocked on writer
// so the queue fills.
func TestBackpressure_QueueFull_DropsOldest_BumpsCounter(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)

	// queueSize + extra events; writer blocked so worker can't drain.
	// Worker pulls one event off the queue and blocks on writer, so the
	// effective buffer is queueSize + 1 — bump extra past that so we are
	// guaranteed to see drops.
	const extra = 64
	const total = defaultQueueSize + extra
	// minDrops accounts for the +1 buffered in the blocked worker.
	const minDrops = extra - 1

	notifs := make(chan *pb.Notification, total)
	for range total {
		notifs <- ksNotif("sid-A")
	}
	close(notifs)

	bw := newBlockingWriter()
	tbl := NewSessionTable()

	var drops atomic.Uint64
	deps := Deps{
		Notifications: notifs,
		Writer:        bw,
		Sessions:      tbl,
		ResolveApp:    func(string) (string, error) { return "term", nil },
		Filter:        NewFilter(nil, nil),
		Now:           func() time.Time { return now },
		MonoStart:     now,
		DropCounter:   &drops,
	}

	done := make(chan struct{})
	go func() {
		_ = Run(context.Background(), deps)
		close(done)
	}()

	// Wait for capture to drain notifs (it should, fast, even though worker
	// is blocked — overflows go to the drop counter).
	deadline := time.After(2 * time.Second)
	for len(notifs) != 0 {

		select {
		case <-deadline:
			t.Fatalf("notif chan did not drain; remaining=%d", len(notifs))
		case <-time.After(10 * time.Millisecond):
		}
	}

	// Give the counter a beat to settle (capture finishes processing).
	time.Sleep(50 * time.Millisecond)

	got := drops.Load()
	if got < uint64(minDrops) {
		t.Errorf("expected ≥%d drops, got %d", minDrops, got)
	}

	bw.Release()
	<-done
}

// Test wm5: the time.Time stamped on the ws goroutine must travel with the
// event through the queue. The writer must receive that original time, not
// a later one.
func TestBackpressure_TimestampTravelsThroughQueue(t *testing.T) {
	stampedTime := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	laterTime := stampedTime.Add(5 * time.Second)

	// Now() returns stampedTime initially, then laterTime after first call.
	var nowCalls atomic.Int32
	nowFn := func() time.Time {
		c := nowCalls.Add(1)
		if c == 1 {
			return stampedTime
		}
		return laterTime
	}

	notifs := make(chan *pb.Notification, 1)
	notifs <- ksNotif("sid-A")
	close(notifs)

	// Use a slow writer so we know the worker runs strictly after capture.
	sw := &slowRecordingWriter{delay: 50 * time.Millisecond}
	tbl := NewSessionTable()

	deps := Deps{
		Notifications: notifs,
		Writer:        sw,
		Sessions:      tbl,
		ResolveApp:    func(string) (string, error) { return "term", nil },
		Filter:        NewFilter(nil, nil),
		Now:           nowFn,
		MonoStart:     stampedTime,
	}

	_ = Run(context.Background(), deps)

	times, _ := sw.snapshot()
	if len(times) == 0 {
		t.Fatalf("no writes recorded")
	}
	for i, ts := range times {
		if !ts.Equal(stampedTime) {
			t.Errorf("write %d t=%v, want stampedTime=%v (timestamp must travel through queue)", i, ts, stampedTime)
		}
	}
}

// Test wm5: graceful shutdown drains queued events before Run returns.
// Cancel the context with events still queued; the worker should process
// them (bounded by a drain timeout) so SIGTERM doesn't silently lose them.
func TestBackpressure_GracefulShutdown_DrainsQueue(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)

	const n = 32
	notifs := make(chan *pb.Notification, n)
	for range n {
		notifs <- ksNotif("sid-A")
	}
	// Don't close notifs — we'll cancel ctx instead, simulating SIGTERM.

	// Writer is initially slow but never blocks indefinitely.
	sw := &slowRecordingWriter{delay: 2 * time.Millisecond}
	tbl := NewSessionTable()

	deps := Deps{
		Notifications: notifs,
		Writer:        sw,
		Sessions:      tbl,
		ResolveApp:    func(string) (string, error) { return "term", nil },
		Filter:        NewFilter(nil, nil),
		Now:           func() time.Time { return now },
		MonoStart:     now,
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		_ = Run(ctx, deps)
		close(done)
	}()

	// Let capture enqueue everything.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("Run did not exit after ctx cancel")
	}

	// All events should be flushed: 1 header + n events = n+1 writes.
	_, lines := sw.snapshot()
	headers := 0
	events := 0
	for _, l := range lines {
		if strings.Contains(l, `"type":"session"`) {
			headers++
		} else {
			events++
		}
	}
	if headers != 1 {
		t.Errorf("expected 1 header, got %d", headers)
	}
	if events != n {
		t.Errorf("expected %d events flushed after shutdown, got %d (drained queue?)", n, events)
	}
}

// slowRecordingWriter records writes after a fixed per-write delay.
type slowRecordingWriter struct {
	delay time.Duration

	mu    sync.Mutex
	times []time.Time
	lines []string
}

func (s *slowRecordingWriter) Write(t time.Time, line []byte) error {
	time.Sleep(s.delay)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.times = append(s.times, t)
	s.lines = append(s.lines, string(line))
	return nil
}

func (s *slowRecordingWriter) snapshot() ([]time.Time, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := make([]time.Time, len(s.times))
	copy(t, s.times)
	l := make([]string, len(s.lines))
	copy(l, s.lines)
	return t, l
}
