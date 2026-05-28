package capture

import (
	"context"
	"encoding/json"
	"log"
	"sync/atomic"
	"time"

	pb "github.com/tmc/it2/proto"
)

// LineWriter is the writer surface capture needs.
//
// Write takes an explicit time so the capture loop owns the single clock
// read per event (it2ks-mty). Writers MUST use this time for any
// date-bucketing decision; recomputing now() internally re-opens the
// midnight-split race.
type LineWriter interface {
	Write(t time.Time, line []byte) error
}

// Deps bundles capture-loop dependencies.
type Deps struct {
	Notifications <-chan *pb.Notification
	Writer        LineWriter
	// Sessions is the session-index table. Lifted out of Run so reconnects
	// reuse it (it2ks-kke). Caller owns its lifetime.
	Sessions     *SessionTable
	ResolveApp   Resolver
	Filter       *Filter
	IncludeChars bool
	Now          func() time.Time
	MonoStart    time.Time
	// DropCounter (optional) is incremented every time the internal event
	// queue overflows and an event is dropped (it2ks-wm5). Surfaced for
	// tests and future metrics; if nil, drops are still logged but not
	// counted externally.
	DropCounter *atomic.Uint64
}

// defaultQueueSize bounds the in-memory queue between the ws-notification
// goroutine and the worker (it2ks-wm5).
//
// Sizing rationale: at a typing burst of ~20 keystrokes/sec, 1024 absorbs
// ~50 seconds of writer stall — well above any realistic fsync hiccup on
// a healthy SSD. Memory: queuedEvent is ~80 bytes, so the worst-case
// ceiling is ~80 KB. Safe.
const defaultQueueSize = 1024

// drainTimeout bounds the graceful-shutdown queue drain. Long enough to
// flush a full queue against a healthy disk; short enough that a stuck
// writer can't hold up SIGTERM exit indefinitely.
const drainTimeout = 2 * time.Second

// queuedEvent travels from the ws-notification goroutine to the worker.
// The timestamp is stamped at notification receipt so date-bucketing /
// monotonic-since-start math reflects when iTerm2 emitted the event,
// not when the worker eventually processes it (preserves the bundle-mty
// single-clock-per-event contract across the queue boundary).
type queuedEvent struct {
	now time.Time
	sid string
	ks  *pb.KeystrokeNotification
}

// Run consumes notifications until the channel closes or ctx is cancelled.
// Per-event errors are logged but never abort the loop.
//
// Contract (see docs/superpowers/plans/it2ks-bundle-mty-contract.md and
// docs/superpowers/plans/it2ks-wm5-backpressure.md):
//   - One clock read per event; the same time.Time is handed to the writer.
//   - Session-index table is owned by the caller and survives reconnects.
//   - Header is written BEFORE the session-index entry is committed: a
//     failed header write rolls back the index so the next event for the
//     same (sid, app) retries with a fresh slot (it2ks-n56).
//   - The ws-notification goroutine ONLY stamps the clock and enqueues
//     (it2ks-wm5). All slow work — resolver, SessionTable.Assign, marshal,
//     writer.Write — runs on a single worker goroutine. On queue overflow
//     the OLDEST event is dropped and DropCounter is bumped; recent
//     psychomotor signal matters more than ancient.
func Run(ctx context.Context, d Deps) error {
	cache := NewAppCache(5*time.Second, d.ResolveApp, d.Now)
	queue := make(chan queuedEvent, defaultQueueSize)

	// Worker drains queue, does slow work, writes.
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		runWorker(queue, cache, d)
	}()

	// Capture stays on the current goroutine: fast path only.
	captureErr := runCapture(ctx, queue, d)

	// Either ctx cancelled or notifs closed → close queue so worker drains
	// remaining events then exits.
	close(queue)

	// Bounded drain so a stuck writer cannot hold up shutdown forever.
	select {
	case <-workerDone:
	case <-time.After(drainTimeout):
		log.Printf("it2ks: worker drain timeout (%s) exceeded; some queued events may be lost", drainTimeout)
	}

	return captureErr
}

// runCapture is the ws-notification goroutine path. Stays FAST: reads the
// clock once per event, enqueues. On full queue, drops the OLDEST event
// (most-recent-keystroke priority) then enqueues the new one.
func runCapture(ctx context.Context, queue chan queuedEvent, d Deps) error {
	var dropsLogged uint64
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case n, ok := <-d.Notifications:
			if !ok {
				return nil
			}
			ks := n.GetKeystrokeNotification()
			if ks == nil {
				continue
			}
			ev := queuedEvent{now: d.Now(), sid: ks.GetSession(), ks: ks}

			select {
			case queue <- ev:
				// fast path
			default:
				// Queue full → drop OLDEST, then enqueue this one. Only one
				// sender (this goroutine) so no race for the slot we free.
				select {
				case <-queue:
					if d.DropCounter != nil {
						d.DropCounter.Add(1)
					}
					dropsLogged++
					// Bound log noise: first drop + every 1024 thereafter.
					if dropsLogged == 1 || dropsLogged%1024 == 0 {
						log.Printf("it2ks: event queue full; dropped oldest (total drops in this Run: %d)", dropsLogged)
					}
				default:
				}
				select {
				case queue <- ev:
				default:
					if d.DropCounter != nil {
						d.DropCounter.Add(1)
					}
					dropsLogged++
				}
			}
		}
	}
}

// runWorker drains the event queue and does the slow work: resolver,
// SessionTable.Assign, marshal, write. Single worker preserves
// per-session event ordering trivially; extra workers would only contend
// on the writer.
func runWorker(queue <-chan queuedEvent, cache *AppCache, d Deps) {
	for qe := range queue {
		processEvent(qe, cache, d)
	}
}

// processEvent runs the slow path for one queued event.
func processEvent(qe queuedEvent, cache *AppCache, d Deps) {
	app := cache.Get(qe.sid)
	if !d.Filter.Allow(app) {
		return
	}

	now := qe.now
	date := now.UTC().Format("2006-01-02")
	wall := now.UTC().Format(time.RFC3339Nano)

	s, isNew, _ := d.Sessions.Assign(date, qe.sid, app)
	if isNew {
		rec := SessionRecord{Type: "session", S: s, SID: qe.sid, App: app, T0: wall}
		line, err := json.Marshal(rec)
		if err != nil {
			log.Printf("it2ks: marshal session: %v", err)
			d.Sessions.Rollback(qe.sid, app)
			return
		}
		if err := d.Writer.Write(now, line); err != nil {
			log.Printf("it2ks: write session: %v", err)
			d.Sessions.Rollback(qe.sid, app)
			return
		}
	}

	mono := now.Sub(d.MonoStart).Nanoseconds()
	ev := NewEvent(qe.ks, s, mono, wall, d.IncludeChars)

	line, err := json.Marshal(ev)
	if err != nil {
		log.Printf("it2ks: marshal event: %v", err)
		return
	}
	if err := d.Writer.Write(now, line); err != nil {
		log.Printf("it2ks: write event: %v", err)
		return
	}
}
