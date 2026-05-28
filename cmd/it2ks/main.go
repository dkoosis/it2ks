// it2ks captures iTerm2 keystroke events to JSONL.
package main

import (
	"context"
	"encoding/json"
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

	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := w.Flush(); err != nil {
					log.Printf("it2ks: periodic flush: %v", err)
				}
			}
		}
	}()

	runWithBackoff(ctx, *wsURL, cfg, w)
}

func defaultConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".it2ks", "config.toml")
}

func runWithBackoff(ctx context.Context, wsURL string, cfg config.Config, w *writer.Writer) {
	backoff := time.Second
	const maxBackoff = 60 * time.Second
	monoStart := time.Now()

	for {
		if ctx.Err() != nil {
			return
		}
		err := connectAndRun(ctx, wsURL, cfg, w, monoStart)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Printf("it2ks: session ended: %v (retry in %s)", err, backoff)
		} else {
			log.Printf("it2ks: notification channel closed (retry in %s)", backoff)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < maxBackoff {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

func connectAndRun(ctx context.Context, wsURL string, cfg config.Config, w *writer.Writer, monoStart time.Time) error {
	c := client.New(wsURL)
	if err := c.Connect(ctx); err != nil {
		return fmt.Errorf("connect: %w", err)
	}

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
		ResolveApp: func(sid string) (string, error) {
			v, err := c.GetVariable(ctx, sid, "jobName")
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
			return fmt.Errorf("advanced subscribe status: %v", s)
		}
	}
	return nil
}

type writerAdapter struct{ w *writer.Writer }

func (a writerAdapter) Write(p []byte) error { return a.w.Write(p) }
