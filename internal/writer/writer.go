// Package writer is a date-bucketed JSONL writer with buffered IO.
package writer

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Writer struct {
	dir string
	now func() time.Time

	mu      sync.Mutex
	curDate string
	file    *os.File
	buf     *bufio.Writer
}

func New(dir string, now func() time.Time) (*Writer, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	return &Writer{dir: dir, now: now}, nil
}

// Write appends one JSON line (the trailing newline is added).
func (w *Writer) Write(line []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	date := w.now().UTC().Format("2006-01-02")
	if date != w.curDate {
		if err := w.rotateLocked(date); err != nil {
			return err
		}
	}
	if _, err := w.buf.Write(line); err != nil {
		return err
	}
	return w.buf.WriteByte('\n')
}

// Flush forces buffered bytes to disk.
func (w *Writer) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.buf == nil {
		return nil
	}
	if err := w.buf.Flush(); err != nil {
		return err
	}
	return w.file.Sync()
}

// Close flushes and closes the underlying file.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.closeLocked()
}

func (w *Writer) rotateLocked(date string) error {
	if err := w.closeLocked(); err != nil {
		return err
	}
	path := filepath.Join(w.dir, date+".jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	w.file = f
	w.buf = bufio.NewWriterSize(f, 32*1024)
	w.curDate = date
	return nil
}

func (w *Writer) closeLocked() error {
	var firstErr error
	if w.buf != nil {
		if err := w.buf.Flush(); err != nil {
			firstErr = err
		}
	}
	if w.file != nil {
		if err := w.file.Sync(); err != nil && firstErr == nil {
			firstErr = err
		}
		if err := w.file.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	w.file = nil
	w.buf = nil
	w.curDate = ""
	return firstErr
}
