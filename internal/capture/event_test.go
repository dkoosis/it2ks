package capture

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
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

	ev := NewEvent(n, 0, 1_000_000_000, "2026-05-27T12:00:00Z", true)
	if ev.Act != "down" {
		t.Errorf("Act = %q, want down", ev.Act)
	}
	if ev.Char != "a" {
		t.Errorf("Char = %q, want a", ev.Char)
	}
	if ev.CharRaw != "" {
		t.Errorf("CharRaw should be omitted when equal to Char; got %q", ev.CharRaw)
	}
	if ev.S != 0 {
		t.Errorf("S = %d, want 0", ev.S)
	}
	if len(ev.Mods) != 1 || ev.Mods[0] != "shift" {
		t.Errorf("Mods = %v, want [shift]", ev.Mods)
	}
}

func TestEventFromNotification_CharRawDiffers(t *testing.T) {
	chars := "A"
	raw := "a"
	keyCode := int32(0)
	action := pb.KeystrokeNotification_KEY_DOWN
	sess := "s1"
	n := &pb.KeystrokeNotification{
		Characters: &chars, CharactersIgnoringModifiers: &raw,
		KeyCode: &keyCode, Session: &sess, Action: &action,
	}
	ev := NewEvent(n, 0, 0, "t", true)
	if ev.Char != "A" || ev.CharRaw != "a" {
		t.Errorf("char/char_raw = %q/%q, want A/a", ev.Char, ev.CharRaw)
	}
}

func TestEventMarshalJSON_FieldOrderAndNames(t *testing.T) {
	ev := Event{
		TS:      1234,
		Wall:    "2026-05-27T12:00:00Z",
		S:       3,
		Act:     "down",
		Key:     7,
		Char:    "A",
		CharRaw: "a",
		Mods:    []string{"shift"},
	}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, want := range []string{`"ts":1234`, `"wall":"2026-05-27T12:00:00Z"`, `"s":3`, `"act":"down"`, `"key":7`, `"char":"A"`, `"char_raw":"a"`, `"mods":["shift"]`} {
		if !strings.Contains(got, want) {
			t.Errorf("JSON missing %q; got %s", want, got)
		}
	}
}

func TestEventMarshalJSON_OmitsEmptyOptionals(t *testing.T) {
	ev := Event{TS: 1, Wall: "t", S: 0, Act: "down", Key: 1}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, banned := range []string{`"char"`, `"char_raw"`, `"mods"`} {
		if strings.Contains(got, banned) {
			t.Errorf("JSON should omit %s; got %s", banned, got)
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
	ev := NewEvent(n, 0, 0, "t", false)
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

func (w *fakeWriter) Write(_ time.Time, p []byte) error {
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

func TestRun_EmitsSessionHeaderBeforeFirstEvent(t *testing.T) {
	ch := make(chan *pb.Notification, 4)
	chars, raw := "a", "a"
	keyCode := int32(0)
	action := pb.KeystrokeNotification_KEY_DOWN
	sess := "s1"
	mk := func() *pb.Notification {
		return &pb.Notification{
			KeystrokeNotification: &pb.KeystrokeNotification{
				Characters: &chars, CharactersIgnoringModifiers: &raw,
				KeyCode: &keyCode, Session: &sess, Action: &action,
			},
		}
	}
	ch <- mk()
	ch <- mk()
	close(ch)

	w := &fakeWriter{}
	deps := Deps{
		Notifications: ch,
		Writer:        w,
		Sessions:      NewSessionTable(),
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
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3 (1 session header + 2 events)", len(lines))
	}
	var hdr SessionRecord
	if err := json.Unmarshal(lines[0], &hdr); err != nil {
		t.Fatal(err)
	}
	if hdr.Type != "session" || hdr.SID != "s1" || hdr.App != "claude" || hdr.S != 0 {
		t.Errorf("session header = %+v, want type=session sid=s1 app=claude s=0", hdr)
	}
	for i, ln := range lines[1:] {
		var ev Event
		if err := json.Unmarshal(ln, &ev); err != nil {
			t.Fatal(err)
		}
		if ev.S != 0 || ev.Act != "down" || ev.Char != "a" {
			t.Errorf("event[%d] = %+v, want s=0 act=down char=a", i, ev)
		}
	}
}

func TestRun_NewSessionWhenAppChanges(t *testing.T) {
	chars, raw := "a", "a"
	keyCode := int32(0)
	action := pb.KeystrokeNotification_KEY_DOWN
	sess := "s1"
	ch := make(chan *pb.Notification, 2)
	ch <- &pb.Notification{KeystrokeNotification: &pb.KeystrokeNotification{
		Characters: &chars, CharactersIgnoringModifiers: &raw,
		KeyCode: &keyCode, Session: &sess, Action: &action,
	}}
	ch <- &pb.Notification{KeystrokeNotification: &pb.KeystrokeNotification{
		Characters: &chars, CharactersIgnoringModifiers: &raw,
		KeyCode: &keyCode, Session: &sess, Action: &action,
	}}
	close(ch)

	apps := []string{"zsh", "vim"}
	i := 0
	w := &fakeWriter{}
	deps := Deps{
		Notifications: ch,
		Writer:        w,
		Sessions:      NewSessionTable(),
		// New AppCache TTL is 5s, so successive calls would normally cache.
		// Force re-resolution by giving each call a unique app via a counter,
		// but only on the second logical event by advancing Now beyond TTL.
		ResolveApp: func(sid string) (string, error) {
			out := apps[i]
			if i < len(apps)-1 {
				i++
			}
			return out, nil
		},
		IncludeChars: true,
		Filter:       NewFilter(nil, nil),
		Now: (func() func() time.Time {
			base := time.Now()
			var calls atomic.Int32
			return func() time.Time {
				c := calls.Add(1)
				return base.Add(time.Duration(c) * 10 * time.Second)
			}
		})(),
		MonoStart: time.Now(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := Run(ctx, deps); err != nil {
		t.Fatal(err)
	}
	lines := w.get()
	// Expect: hdr(zsh) ev hdr(vim) ev = 4 lines
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4 (hdr+ev hdr+ev); lines=%s", len(lines), lines)
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
		Sessions:      NewSessionTable(),
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
