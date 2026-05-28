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
			app := cache.Get(ks.GetSession())
			if !d.Filter.Allow(app) {
				continue
			}
			now := d.Now()
			mono := now.Sub(d.MonoStart).Nanoseconds()
			wall := now.UTC().Format(time.RFC3339Nano)
			ev := NewEvent(ks, app, mono, wall, d.IncludeChars)

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
