package capture

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	pb "github.com/tmc/it2/proto"
)

func TestEventFromNotification_DownKey(t *testing.T) {
	chars := "a"
	raw := "a"
	keyCode := int32(0)
	action := pb.KeystrokeNotification_KEY_DOWN
	sess := "session-abc"
	n := &pb.KeystrokeNotification{
		Characters:                  &chars,
		CharactersIgnoringModifiers: &raw,
		KeyCode:                     &keyCode,
		Session:                     &sess,
		Action:                      &action,
		Modifiers:                   []pb.Modifiers{pb.Modifiers_SHIFT},
	}

	ev := NewEvent(n, "claude", 1_000_000_000, "2026-05-27T12:00:00Z", true)
	if ev.Act != "down" {
		t.Errorf("Act = %q, want down", ev.Act)
	}
	if ev.Char != "a" {
		t.Errorf("Char = %q, want a", ev.Char)
	}
	if ev.App != "claude" {
		t.Errorf("App = %q, want claude", ev.App)
	}
	if len(ev.Mods) != 1 || ev.Mods[0] != "shift" {
		t.Errorf("Mods = %v, want [shift]", ev.Mods)
	}
	if ev.SID != "session-abc" {
		t.Errorf("SID = %q, want session-abc", ev.SID)
	}
}

func TestEventMarshalJSON_FieldOrderAndNames(t *testing.T) {
	ev := Event{
		TS:      1234,
		Wall:    "2026-05-27T12:00:00Z",
		SID:     "s1",
		App:     "zsh",
		Act:     "down",
		Key:     7,
		Char:    "a",
		CharRaw: "a",
		Mods:    []string{"shift"},
	}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, want := range []string{`"ts":1234`, `"wall":"2026-05-27T12:00:00Z"`, `"sid":"s1"`, `"app":"zsh"`, `"act":"down"`, `"key":7`, `"char":"a"`, `"char_raw":"a"`, `"mods":["shift"]`} {
		if !strings.Contains(got, want) {
			t.Errorf("JSON missing %q; got %s", want, got)
		}
	}
}

func TestEventFromNotification_IncludeCharsFalse_OmitsChar(t *testing.T) {
	chars := "secret"
	raw := "secret"
	keyCode := int32(1)
	action := pb.KeystrokeNotification_KEY_DOWN
	sess := "s"
	n := &pb.KeystrokeNotification{
		Characters:                  &chars,
		CharactersIgnoringModifiers: &raw,
		KeyCode:                     &keyCode,
		Session:                     &sess,
		Action:                      &action,
	}
	ev := NewEvent(n, "vim", 0, "t", false)
	if ev.Char != "" || ev.CharRaw != "" {
		t.Errorf("Char/CharRaw should be empty when includeChars=false; got %q/%q", ev.Char, ev.CharRaw)
	}
}

func TestActionString(t *testing.T) {
	cases := map[pb.KeystrokeNotification_Action]string{
		pb.KeystrokeNotification_KEY_DOWN:      "down",
		pb.KeystrokeNotification_KEY_UP:        "up",
		pb.KeystrokeNotification_FLAGS_CHANGED: "flags",
	}
	for in, want := range cases {
		if got := actionString(in); got != want {
			t.Errorf("actionString(%v) = %q, want %q", in, got, want)
		}
	}
}

type fakeWriter struct {
	mu    sync.Mutex
	lines [][]byte
}

func (w *fakeWriter) Write(p []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	dup := make([]byte, len(p))
	copy(dup, p)
	w.lines = append(w.lines, dup)
	return nil
}

func (w *fakeWriter) get() [][]byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([][]byte(nil), w.lines...)
}

func TestRun_PassesEventsThroughToWriter(t *testing.T) {
	ch := make(chan *pb.Notification, 4)
	chars, raw := "a", "a"
	keyCode := int32(0)
	action := pb.KeystrokeNotification_KEY_DOWN
	sess := "s1"
	ch <- &pb.Notification{
		KeystrokeNotification: &pb.KeystrokeNotification{
			Characters:                  &chars,
			CharactersIgnoringModifiers: &raw,
			KeyCode:                     &keyCode,
			Session:                     &sess,
			Action:                      &action,
		},
	}
	close(ch)

	w := &fakeWriter{}
	deps := Deps{
		Notifications: ch,
		Writer:        w,
		ResolveApp:    func(sid string) (string, error) { return "claude", nil },
		IncludeChars:  true,
		Filter:        NewFilter(nil, nil),
		Now:           time.Now,
		MonoStart:     time.Now(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := Run(ctx, deps); err != nil {
		t.Fatalf("Run() = %v", err)
	}

	lines := w.get()
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1", len(lines))
	}
	var ev Event
	if err := json.Unmarshal(lines[0], &ev); err != nil {
		t.Fatal(err)
	}
	if ev.App != "claude" || ev.Char != "a" || ev.Act != "down" {
		t.Errorf("event = %+v, want app=claude char=a act=down", ev)
	}
}

func TestRun_DropsFilteredApps(t *testing.T) {
	ch := make(chan *pb.Notification, 1)
	chars, raw := "x", "x"
	keyCode := int32(0)
	action := pb.KeystrokeNotification_KEY_DOWN
	sess := "s1"
	ch <- &pb.Notification{KeystrokeNotification: &pb.KeystrokeNotification{
		Characters: &chars, CharactersIgnoringModifiers: &raw,
		KeyCode: &keyCode, Session: &sess, Action: &action,
	}}
	close(ch)

	w := &fakeWriter{}
	deps := Deps{
		Notifications: ch,
		Writer:        w,
		ResolveApp:    func(sid string) (string, error) { return "1password", nil },
		IncludeChars:  true,
		Filter:        NewFilter(nil, []string{"1password"}),
		Now:           time.Now,
		MonoStart:     time.Now(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := Run(ctx, deps); err != nil {
		t.Fatal(err)
	}
	if len(w.get()) != 0 {
		t.Errorf("expected 0 lines after filter; got %d", len(w.get()))
	}
}
