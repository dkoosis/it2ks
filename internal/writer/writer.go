// Package writer is a date-bucketed JSONL writer with buffered IO.
package writer

import (
	"bufio"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ErrClosed is returned by Write/Flush after Close has been called.
// It prevents the post-Close reopen race where a late Write would
// rotateLocked() back to an open file that nothing would ever close.
var ErrClosed = errors.New("writer: closed")

type Writer struct {
	dir string
	now func() time.Time

	mu      sync.Mutex
	curDate string
	file    *os.File
	buf     *bufio.Writer
	closed  bool

	// Flusher lifecycle: stop signals the flusher to exit; flusherWG lets
	// Close block until the goroutine has actually returned. Both are
	// guarded by mu when read/written by Close/StartFlusher.
	stop      chan struct{}
	flusherWG sync.WaitGroup
}

func New(dir string, now func() time.Time) (*Writer, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	return &Writer{dir: dir, now: now}, nil
}

// Write appends one JSON line (the trailing newline is added). The caller
// passes the event time so capture's single-clock-per-event contract
// (it2ks-mty) is honored — the writer MUST NOT recompute now() for the
// date-bucket decision or the midnight-split race re-opens.
//
// Returns ErrClosed if the writer has been closed.
func (w *Writer) Write(t time.Time, line []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return ErrClosed
	}

	date := t.UTC().Format("2006-01-02")
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
// Returns ErrClosed if the writer has been closed.
func (w *Writer) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return ErrClosed
	}
	if w.buf == nil {
		return nil
	}
	if err := w.buf.Flush(); err != nil {
		return err
	}
	return w.file.Sync()
}

// StartFlusher launches a goroutine that calls Flush every interval until
// Close is called. Close blocks until the goroutine has returned, so callers
// don't need to manage a separate done-chan or WaitGroup. Safe to call at
// most once per Writer; subsequent calls are no-ops.
func (w *Writer) StartFlusher(interval time.Duration) {
	w.mu.Lock()
	if w.closed || w.stop != nil {
		w.mu.Unlock()
		return
	}
	w.stop = make(chan struct{})
	stop := w.stop
	w.flusherWG.Add(1)
	w.mu.Unlock()

	go func() {
		defer w.flusherWG.Done()
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				// Flush may return ErrClosed if Close raced ahead between
				// the tick firing and us acquiring the lock; that's expected
				// and not worth logging.
				if err := w.Flush(); err != nil && !errors.Is(err, ErrClosed) {
					log.Printf("writer: periodic flush: %v", err)
				}
			}
		}
	}()
}

// Close flushes, closes the underlying file, and joins the flusher goroutine
// (if one was started). After Close returns, Write/Flush return ErrClosed
// and the writer cannot be reused. Idempotent.
func (w *Writer) Close() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	stop := w.stop
	w.stop = nil
	err := w.closeFileLocked()
	w.mu.Unlock()

	if stop != nil {
		close(stop)
	}
	// Wait outside the lock so the flusher's final Flush() call (if mid-tick)
	// can acquire the mutex, see closed=true, and return ErrClosed promptly.
	w.flusherWG.Wait()
	return err
}

func (w *Writer) rotateLocked(date string) error {
	if err := w.closeFileLocked(); err != nil {
		return err
	}
	path := filepath.Join(w.dir, date+".jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	w.file = f
	w.buf = bufio.NewWriterSize(f, 32*1024)
	w.curDate = date
	return nil
}

// closeFileLocked flushes+closes the current file (if any) and resets the
// per-file fields. Used by both Close and rotateLocked. Caller holds w.mu.
func (w *Writer) closeFileLocked() error {
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
