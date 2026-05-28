package writer

import (
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
