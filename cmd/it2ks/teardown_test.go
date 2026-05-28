package main

import (
	"context"
	"errors"
	"testing"
	"time"

	pb "github.com/tmc/it2/proto"
)

var errTeardownBoom = errors.New("boom")

// fakeTeardownClient records the Close + Unsubscribe + SendRequest calls so
// tests can assert teardown order and semantics.
type fakeTeardownClient struct {
	closeCalls       int
	closeErr         error
	unsubCalls       []unsubCall
	unsubErr         error
	sendCalls        []*pb.ClientOriginatedMessage
	sendResp         *pb.ServerOriginatedMessage
	sendErr          error
	observedDeadline time.Time
}

type unsubCall struct {
	notifType string
	sessionID string
}

func (f *fakeTeardownClient) Close() error {
	f.closeCalls++
	return f.closeErr
}

func (f *fakeTeardownClient) UnsubscribeFromNotifications(ctx context.Context, notifType, sessionID string) error {
	f.unsubCalls = append(f.unsubCalls, unsubCall{notifType, sessionID})
	if d, ok := ctx.Deadline(); ok {
		f.observedDeadline = d
	}
	return f.unsubErr
}

func (f *fakeTeardownClient) SendRequest(ctx context.Context, msg *pb.ClientOriginatedMessage) (*pb.ServerOriginatedMessage, error) {
	f.sendCalls = append(f.sendCalls, msg)
	if d, ok := ctx.Deadline(); ok {
		f.observedDeadline = d
	}
	if f.sendErr != nil {
		return nil, f.sendErr
	}
	return f.sendResp, nil
}

// TestTeardownClosesClient: teardownClient must always call Close on the
// underlying client. Without defer c.Close() in connectAndRun, every
// reconnect leaks a websocket FD + reader goroutine.
func TestTeardownClosesClient(t *testing.T) {
	f := &fakeTeardownClient{}
	teardownClient(context.Background(), f)
	if f.closeCalls != 1 {
		t.Errorf("Close called %d times, want 1", f.closeCalls)
	}
}

// TestTeardownUnsubscribesAdvanced: teardown must send an advanced-keystroke
// Unsubscribe (subscribe=false, advanced=true, session="all"). iTerm2 keeps
// emitting to the dead socket until told to stop.
func TestTeardownUnsubscribesAdvanced(t *testing.T) {
	f := &fakeTeardownClient{}
	teardownClient(context.Background(), f)
	if len(f.sendCalls) == 0 {
		t.Fatal("teardown did not SendRequest for advanced unsubscribe")
	}
	nr := f.sendCalls[0].GetNotificationRequest()
	if nr == nil {
		t.Fatalf("expected NotificationRequest submessage, got %+v", f.sendCalls[0])
	}
	if got := nr.GetSubscribe(); got != false {
		t.Errorf("Subscribe=%v want false", got)
	}
	if got := nr.GetNotificationType(); got != pb.NotificationType_NOTIFY_ON_KEYSTROKE {
		t.Errorf("NotificationType=%v want NOTIFY_ON_KEYSTROKE", got)
	}
	if got := nr.GetSession(); got != "all" {
		t.Errorf("Session=%q want %q", got, "all")
	}
	km := nr.GetKeystrokeMonitorRequest()
	if km == nil || !km.GetAdvanced() {
		t.Errorf("KeystrokeMonitorRequest advanced=true missing, got %+v", km)
	}
}

// TestTeardownUsesShortDeadline: teardown context must be bounded (~2s)
// so a hung iTerm2 doesn't block shutdown forever.
func TestTeardownUsesShortDeadline(t *testing.T) {
	f := &fakeTeardownClient{}
	start := time.Now()
	teardownClient(context.Background(), f)
	if f.observedDeadline.IsZero() {
		t.Fatal("teardown did not set a context deadline")
	}
	delta := f.observedDeadline.Sub(start)
	if delta <= 0 || delta > 5*time.Second {
		t.Errorf("teardown deadline %v out of expected ~2s range", delta)
	}
}

// TestTeardownClosesEvenWhenUnsubscribeFails: best-effort means errors from
// the unsubscribe path must not skip Close.
func TestTeardownClosesEvenWhenUnsubscribeFails(t *testing.T) {
	f := &fakeTeardownClient{sendErr: errTeardownBoom}
	teardownClient(context.Background(), f)
	if f.closeCalls != 1 {
		t.Errorf("Close calls=%d, want 1 even when unsubscribe errored", f.closeCalls)
	}
}
