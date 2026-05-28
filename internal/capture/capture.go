package capture

import (
	"context"
	"encoding/json"
	"log"
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
}

// Run consumes notifications until the channel closes or ctx is cancelled.
// Per-event errors are logged but never abort the loop.
//
// Contract (see docs/superpowers/plans/it2ks-bundle-mty-contract.md):
//   - One clock read per event; the same time.Time is handed to the writer.
//   - Session-index table is owned by the caller and survives reconnects.
//   - Header is written BEFORE the session-index entry is committed: a
//     failed header write rolls back the index so the next event for the
//     same (sid, app) retries with a fresh slot (it2ks-n56).
func Run(ctx context.Context, d Deps) error {
	cache := NewAppCache(5*time.Second, d.ResolveApp, d.Now)

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
			sid := ks.GetSession()
			app := cache.Get(sid)
			if !d.Filter.Allow(app) {
				continue
			}
			// Single clock read per event — pass `now` to the writer so its
			// date-bucket decision matches capture's.
			now := d.Now()
			date := now.UTC().Format("2006-01-02")
			wall := now.UTC().Format(time.RFC3339Nano)

			s, isNew, _ := d.Sessions.Assign(date, sid, app)
			if isNew {
				rec := SessionRecord{Type: "session", S: s, SID: sid, App: app, T0: wall}
				line, err := json.Marshal(rec)
				if err != nil {
					log.Printf("it2ks: marshal session: %v", err)
					d.Sessions.Rollback(sid, app)
					continue
				}
				if err := d.Writer.Write(now, line); err != nil {
					log.Printf("it2ks: write session: %v", err)
					d.Sessions.Rollback(sid, app)
					continue
				}
			}

			mono := now.Sub(d.MonoStart).Nanoseconds()
			ev := NewEvent(ks, s, mono, wall, d.IncludeChars)

			line, err := json.Marshal(ev)
			if err != nil {
				log.Printf("it2ks: marshal event: %v", err)
				continue
			}
			if err := d.Writer.Write(now, line); err != nil {
				log.Printf("it2ks: write event: %v", err)
				continue
			}
		}
	}
}
