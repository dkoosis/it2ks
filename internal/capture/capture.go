package capture

import (
	"context"
	"encoding/json"
	"log"
	"time"

	pb "github.com/tmc/it2/proto"
)

// LineWriter is the writer surface capture needs.
type LineWriter interface {
	Write(line []byte) error
}

// Deps bundles capture-loop dependencies.
type Deps struct {
	Notifications <-chan *pb.Notification
	Writer        LineWriter
	ResolveApp    Resolver
	Filter        *Filter
	IncludeChars  bool
	Now           func() time.Time
	MonoStart     time.Time
}

// Run consumes notifications until the channel closes or ctx is cancelled.
// Per-event errors are logged but never abort the loop.
//
// A SessionRecord JSON line is emitted before the first Event of each new
// (sid, app) pair, and re-emitted after a UTC day rollover so each day's file
// is self-contained for downstream consumers.
func Run(ctx context.Context, d Deps) error {
	cache := NewAppCache(5*time.Second, d.ResolveApp, d.Now)

	// Session-index table: maps "sid|app" → index. Reset on day rollover.
	sessions := map[string]int{}
	nextIdx := 0
	curDate := ""

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
			now := d.Now()
			date := now.UTC().Format("2006-01-02")
			if date != curDate {
				sessions = map[string]int{}
				nextIdx = 0
				curDate = date
			}
			wall := now.UTC().Format(time.RFC3339Nano)
			key := sid + "|" + app
			idx, found := sessions[key]
			if !found {
				idx = nextIdx
				sessions[key] = idx
				nextIdx++
				rec := SessionRecord{Type: "session", S: idx, SID: sid, App: app, T0: wall}
				if line, err := json.Marshal(rec); err != nil {
					log.Printf("it2ks: marshal session: %v", err)
				} else if err := d.Writer.Write(line); err != nil {
					log.Printf("it2ks: write session: %v", err)
				}
			}
			mono := now.Sub(d.MonoStart).Nanoseconds()
			ev := NewEvent(ks, idx, mono, wall, d.IncludeChars)

			line, err := json.Marshal(ev)
			if err != nil {
				log.Printf("it2ks: marshal event: %v", err)
				continue
			}
			if err := d.Writer.Write(line); err != nil {
				log.Printf("it2ks: write event: %v", err)
				continue
			}
		}
	}
}
