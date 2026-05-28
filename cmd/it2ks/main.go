// it2ks captures iTerm2 keystroke events to JSONL.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/dkoosis/it2ks/internal/capture"
	"github.com/dkoosis/it2ks/internal/config"
	"github.com/dkoosis/it2ks/internal/writer"

	"github.com/tmc/it2/client"
	pb "github.com/tmc/it2/proto"
)

func main() {
	var (
		cfgPath = flag.String("config", defaultConfigPath(), "path to it2ks config TOML")
		wsURL   = flag.String("url", "ws://localhost:1912", "iTerm2 websocket URL")
	)
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("it2ks: load config: %v", err)
	}

	w, err := writer.New(cfg.LogDir, time.Now)
	if err != nil {
		log.Fatalf("it2ks: open writer: %v", err)
	}
	defer w.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Periodic flush goroutine is owned by the Writer; deferred Close above
	// joins it before returning so we don't leak the goroutine on shutdown.
	w.StartFlusher(2 * time.Second)

	if err := runWithBackoff(ctx, *wsURL, cfg, w); err != nil {
		// Flush before exiting so partial buffers reach disk; launchd sees
		// the non-zero exit and surfaces the error in stderr logs.
		_ = w.Flush()
		log.Fatalf("it2ks: %v", err)
	}
}

func defaultConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".it2ks", "config.toml")
}

// PermanentError wraps an unrecoverable failure (e.g. iTerm2 API rejecting
// our subscribe request). runWithBackoffLoop exits when it sees one; launchd
// surfaces the non-zero exit so the daemon doesn't spin silently forever.
type PermanentError struct{ Err error }

func (e *PermanentError) Error() string { return "permanent: " + e.Err.Error() }
func (e *PermanentError) Unwrap() error { return e.Err }

// errAdvancedSubscribeStatus is the sentinel for non-OK / non-ALREADY_SUBSCRIBED
// responses to the advanced-keystroke subscribe RPC.
var errAdvancedSubscribeStatus = errors.New("advanced subscribe rejected")

// resetThreshold: if a connectAndRun call lasted at least this long before
// erroring, the connection was healthy and the backoff resets to initial.
const resetThreshold = 30 * time.Second

const (
	initialBackoff = time.Second
	maxBackoff     = 60 * time.Second
)

// clock abstracts time for tests.
type clock struct {
	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) error
}

// fakeClock is an alias used by tests (defined here so tests can construct one).
type fakeClock = clock

func realClock() clock {
	return clock{
		now: time.Now,
		sleep: func(ctx context.Context, d time.Duration) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(d):
				return nil
			}
		},
	}
}

func runWithBackoff(ctx context.Context, wsURL string, cfg config.Config, w *writer.Writer) error {
	monoStart := time.Now()
	// SessionTable is lifted out of capture.Run so reconnects reuse it
	// (it2ks-kke). Without this, every reconnect restarts s=0 and produces
	// duplicate {type:"session",s:0,...} headers in the same day's file.
	sessions := capture.NewSessionTable()
	connect := func(ctx context.Context) error {
		return connectAndRun(ctx, wsURL, cfg, w, monoStart, sessions)
	}
	return runWithBackoffLoop(ctx, connect, realClock())
}

// runWithBackoffLoop drives connect with exponential backoff. It resets the
// backoff when a call lasted >= resetThreshold (a healthy connection that
// later dropped), and exits when connect returns a *PermanentError.
func runWithBackoffLoop(ctx context.Context, connect func(context.Context) error, clk clock) error {
	backoff := initialBackoff

	for {
		if ctx.Err() != nil {
			return nil //nolint:nilerr // ctx cancel is clean shutdown, not an error to surface
		}
		start := clk.now()
		err := connect(ctx)
		elapsed := clk.now().Sub(start)
		if ctx.Err() != nil {
			return nil //nolint:nilerr // ctx cancel is clean shutdown, not an error to surface
		}

		var perm *PermanentError
		if errors.As(err, &perm) {
			log.Printf("it2ks: permanent error, exiting loop: %v", err)
			return err
		}

		if elapsed >= resetThreshold {
			backoff = initialBackoff
		}

		if err != nil {
			log.Printf("it2ks: session ended after %s: %v (retry in %s)", elapsed, err, backoff)
		} else {
			log.Printf("it2ks: notification channel closed after %s (retry in %s)", elapsed, backoff)
		}

		if err := clk.sleep(ctx, backoff); err != nil {
			return nil //nolint:nilerr // sleep err means ctx cancelled → clean shutdown
		}
		if backoff < maxBackoff {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

// teardownClientAPI is the slice of *client.Client teardownClient needs;
// declared as an interface so the cleanup path is unit-testable without a
// live iTerm2 websocket.
type teardownClientAPI interface {
	Close() error
	UnsubscribeFromNotifications(ctx context.Context, notifType, sessionID string) error
	SendRequest(ctx context.Context, msg *pb.ClientOriginatedMessage) (*pb.ServerOriginatedMessage, error)
}

// teardownTimeout bounds the shutdown unsubscribe + Close round-trips so a
// hung iTerm2 can't block the daemon's exit / reconnect path indefinitely.
const teardownTimeout = 2 * time.Second

// teardownClient best-effort tears a connected it2 client down: it sends an
// advanced-keystroke Unsubscribe so iTerm2 stops emitting to the dead
// socket, then always Close()s the client to release the websocket FD +
// reader goroutine. Errors are logged but never block Close.
func teardownClient(_ context.Context, c teardownClientAPI) {
	// Always use a fresh bounded context — caller's ctx may already be
	// cancelled (SIGTERM path) and we still want to send the unsubscribe.
	ctx, cancel := context.WithTimeout(context.Background(), teardownTimeout)
	defer cancel()

	subscribe := false
	notifType := pb.NotificationType_NOTIFY_ON_KEYSTROKE
	advanced := true
	session := "all"
	msg := &pb.ClientOriginatedMessage{
		Submessage: &pb.ClientOriginatedMessage_NotificationRequest{
			NotificationRequest: &pb.NotificationRequest{
				Session:          &session,
				Subscribe:        &subscribe,
				NotificationType: &notifType,
				Arguments: &pb.NotificationRequest_KeystrokeMonitorRequest{
					KeystrokeMonitorRequest: &pb.KeystrokeMonitorRequest{
						Advanced: &advanced,
					},
				},
			},
		},
	}
	if _, err := c.SendRequest(ctx, msg); err != nil {
		log.Printf("it2ks: teardown advanced unsubscribe: %v", err)
	}

	if err := c.Close(); err != nil {
		log.Printf("it2ks: teardown client close: %v", err)
	}
}

func connectAndRun(ctx context.Context, wsURL string, cfg config.Config, w *writer.Writer, monoStart time.Time, sessions *capture.SessionTable) error {
	c := client.New(wsURL)
	if err := c.Connect(ctx); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	// Tear down on every exit path (error return, normal channel close,
	// SIGTERM cancellation). Without this, each reconnect leaks a websocket
	// FD + reader goroutine, and iTerm2 keeps pushing keystrokes to the
	// orphaned socket until TCP keepalive eventually trips.
	defer teardownClient(ctx, c)

	// iTerm2 requires an explicit session sentinel for keystroke subscriptions;
	// the empty string is rejected as SESSION_NOT_FOUND. "all" subscribes to every session.
	notifications, err := c.SubscribeToGenericNotifications(ctx, "keystroke", "all")
	if err != nil {
		return fmt.Errorf("subscribe keystroke: %w", err)
	}

	// Tear down the basic subscription so the advanced re-subscribe is not a no-op
	// (iTerm2 returns ALREADY_SUBSCRIBED and ignores the new arguments otherwise).
	// The notification channel's reader goroutine survives this — it reads all
	// notifications from c.messages regardless of subscription state.
	if err := c.UnsubscribeFromNotifications(ctx, "keystroke", "all"); err != nil {
		return fmt.Errorf("unsubscribe basic keystroke: %w", err)
	}

	if err := sendAdvancedKeystrokeSubscribe(ctx, c); err != nil {
		return fmt.Errorf("advanced subscribe: %w", err)
	}

	deps := capture.Deps{
		Notifications: notifications,
		Writer:        writerAdapter{w},
		Sessions:      sessions,
		ResolveApp: func(sid string) (string, error) {
			// Bound the GetVariable round-trip so a hung iTerm2 jobName lookup
			// can't block the capture loop (it2ks-fz1). The AppCache's
			// negative-cache (f7u) absorbs the resulting error so we don't
			// re-fire the resolver per keystroke on a dead session.
			rctx, cancel := context.WithTimeout(ctx, resolveTimeout)
			defer cancel()
			v, err := c.GetVariable(rctx, sid, "jobName")
			if err != nil {
				return "", err
			}
			// iTerm2 returns variables JSON-encoded; unwrap if it's a JSON string.
			var unquoted string
			if json.Unmarshal([]byte(v), &unquoted) == nil {
				return unquoted, nil
			}
			return v, nil
		},
		Filter:       capture.NewFilter(cfg.AppsInclude, cfg.AppsExclude),
		IncludeChars: cfg.IncludeChars,
		Now:          time.Now,
		MonoStart:    monoStart,
	}
	return capture.Run(ctx, deps)
}

// resolveTimeout bounds the per-keystroke GetVariable("jobName") round-trip.
// 1s is well above a healthy local-websocket RTT but short enough that a hung
// iTerm2 surfaces fast and the negative-cache (f7u) takes over.
const resolveTimeout = 1 * time.Second

func sendAdvancedKeystrokeSubscribe(ctx context.Context, c *client.Client) error {
	subscribe := true
	notifType := pb.NotificationType_NOTIFY_ON_KEYSTROKE
	advanced := true
	session := "all"

	msg := &pb.ClientOriginatedMessage{
		Submessage: &pb.ClientOriginatedMessage_NotificationRequest{
			NotificationRequest: &pb.NotificationRequest{
				Session:          &session,
				Subscribe:        &subscribe,
				NotificationType: &notifType,
				Arguments: &pb.NotificationRequest_KeystrokeMonitorRequest{
					KeystrokeMonitorRequest: &pb.KeystrokeMonitorRequest{
						Advanced: &advanced,
					},
				},
			},
		},
	}
	resp, err := c.SendRequest(ctx, msg)
	if err != nil {
		return err
	}
	if nr := resp.GetNotificationResponse(); nr != nil {
		s := nr.GetStatus()
		if s != pb.NotificationResponse_OK && s != pb.NotificationResponse_ALREADY_SUBSCRIBED {
			// API-level rejection isn't recoverable by retrying — surface it.
			return &PermanentError{Err: fmt.Errorf("%w: %v", errAdvancedSubscribeStatus, s)}
		}
	}
	return nil
}

type writerAdapter struct{ w *writer.Writer }

func (a writerAdapter) Write(t time.Time, p []byte) error { return a.w.Write(t, p) }
