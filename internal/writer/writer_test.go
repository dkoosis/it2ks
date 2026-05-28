package writer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriter_WritesLineToDatedFile(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	w, err := New(dir, func() time.Time { return t0 })
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if err := w.Write([]byte(`{"k":"v"}`)); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "2026-05-27.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "{\"k\":\"v\"}\n" {
		t.Errorf("file contents = %q, want JSON line with newline", string(got))
	}
}

func TestWriter_RollsOverOnDateChange(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 27, 23, 59, 59, 0, time.UTC)
	clock := func() time.Time { return now }
	w, err := New(dir, clock)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if err := w.Write([]byte(`{"day":1}`)); err != nil {
		t.Fatal(err)
	}

	now = time.Date(2026, 5, 28, 0, 0, 1, 0, time.UTC)
	if err := w.Write([]byte(`{"day":2}`)); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}

	day1, err := os.ReadFile(filepath.Join(dir, "2026-05-27.jsonl"))
	if err != nil {
		t.Fatalf("day 1 file: %v", err)
	}
	if !strings.Contains(string(day1), `"day":1`) {
		t.Errorf("day 1 file = %q", string(day1))
	}
	day2, err := os.ReadFile(filepath.Join(dir, "2026-05-28.jsonl"))
	if err != nil {
		t.Fatalf("day 2 file: %v", err)
	}
	if !strings.Contains(string(day2), `"day":2`) {
		t.Errorf("day 2 file = %q", string(day2))
	}
}

func TestWriter_CreatesDirIfMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "logs")
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	w, err := New(dir, func() time.Time { return t0 })
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.Write([]byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "2026-01-01.jsonl")); err != nil {
		t.Errorf("expected dated file: %v", err)
	}
}

func TestWriter_LogFileMode0600(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	w, err := New(dir, func() time.Time { return t0 })
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.Write([]byte(`{"k":"v"}`)); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "2026-05-27.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("log file mode = %o, want 0600", got)
	}
}

func TestWriter_LogDirMode0700(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested-logs")
	t0 := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	w, err := New(dir, func() time.Time { return t0 })
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Errorf("log dir mode = %o, want 0700", got)
	}
}

func TestWriter_WriteAfterCloseReturnsErrClosed(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	w, err := New(dir, func() time.Time { return t0 })
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Write([]byte(`{"k":"v"}`)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	// Snapshot files in dir after Close.
	before, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	err = w.Write([]byte(`{"k":"after"}`))
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("Write after Close: got %v, want ErrClosed", err)
	}

	// No new file should have been created by the post-Close Write.
	after, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Errorf("post-Close Write created files: before=%d after=%d", len(before), len(after))
	}
}

func TestWriter_FlushAfterCloseReturnsErrClosed(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	w, err := New(dir, func() time.Time { return t0 })
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); !errors.Is(err, ErrClosed) {
		t.Fatalf("Flush after Close: got %v, want ErrClosed", err)
	}
}

func TestWriter_CloseJoinsFlusherGoroutine(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	w, err := New(dir, func() time.Time { return t0 })
	if err != nil {
		t.Fatal(err)
	}

	// Start a periodic flusher with a short interval so the goroutine
	// has likely entered its select loop.
	w.StartFlusher(10 * time.Millisecond)
	time.Sleep(30 * time.Millisecond)

	done := make(chan error, 1)
	go func() { done <- w.Close() }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return within 2s — flusher goroutine not joined")
	}

	// After Close returns, Flush should report ErrClosed (proves the flusher
	// can no longer touch the writer).
	if err := w.Flush(); !errors.Is(err, ErrClosed) {
		t.Errorf("Flush after Close: got %v, want ErrClosed", err)
	}
}

func TestWriter_RotationFsyncsParentDir(t *testing.T) {
	dir := t.TempDir()

	var synced []string
	orig := syncDir
	syncDir = func(path string) error {
		synced = append(synced, path)
		return orig(path)
	}
	t.Cleanup(func() { syncDir = orig })

	now := time.Date(2026, 5, 27, 23, 59, 59, 0, time.UTC)
	clock := func() time.Time { return now }
	w, err := New(dir, clock)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// First write creates the day-1 file → must fsync parent dir.
	if err := w.Write([]byte(`{"day":1}`)); err != nil {
		t.Fatal(err)
	}
	if len(synced) < 1 {
		t.Fatalf("expected parent dir fsync after first file creation; calls=%v", synced)
	}
	if synced[0] != dir {
		t.Errorf("syncDir[0] = %q, want %q", synced[0], dir)
	}

	// Date change → rotation creates day-2 file → must fsync parent dir again.
	now = time.Date(2026, 5, 28, 0, 0, 1, 0, time.UTC)
	if err := w.Write([]byte(`{"day":2}`)); err != nil {
		t.Fatal(err)
	}
	if len(synced) < 2 {
		t.Fatalf("expected parent dir fsync after rotation; calls=%v", synced)
	}
	if synced[1] != dir {
		t.Errorf("syncDir[1] = %q, want %q", synced[1], dir)
	}
}

func TestWriter_CloseIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	w, err := New(dir, func() time.Time { return t0 })
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
