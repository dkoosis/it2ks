package capture

import "sync"

// SessionTable owns per-day session-index bookkeeping. Lifted out of
// capture.Run so it survives reconnects (it2ks-kke): a fresh Run with the
// same SessionTable continues numbering monotonically within the same UTC
// day instead of restarting at 0 and colliding with prior headers.
//
// Assign reserves an index for a (sid, app) pair. When isNew is true the
// caller must successfully write the header before the next event;
// otherwise it must call Rollback so the index is not orphaned
// (it2ks-n56).
//
// Day rollover is detected by Assign: when the caller-provided date does
// not match curDate, the table clears and counter resets. The caller's
// next Assign for any sid will report isNew=true and re-emit a header in
// the new day's file.
type SessionTable struct {
	mu      sync.Mutex
	indices map[string]int // "sid|app" -> s
	nextIdx int
	curDate string
}

func NewSessionTable() *SessionTable {
	return &SessionTable{indices: map[string]int{}}
}

// Assign returns the s index for (sid, app) in the given UTC date bucket.
// isNew is true if this call reserved a fresh index (caller must write the
// header). dateChanged is true if the table just rolled over to a new day.
func (t *SessionTable) Assign(date, sid, app string) (s int, isNew, dateChanged bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if date != t.curDate {
		t.indices = map[string]int{}
		t.nextIdx = 0
		t.curDate = date
		dateChanged = true
	}
	key := sid + "|" + app
	if idx, ok := t.indices[key]; ok {
		return idx, false, dateChanged
	}
	idx := t.nextIdx
	t.indices[key] = idx
	t.nextIdx++
	return idx, true, dateChanged
}

// Rollback undoes a freshly-assigned index when the header write failed.
// Only valid immediately after Assign returned isNew=true for the same
// (sid, app). If nextIdx has since advanced past this slot (another
// goroutine raced and got a later index), we still drop the map entry so
// the next Assign retries — a small index gap is acceptable.
func (t *SessionTable) Rollback(sid, app string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := sid + "|" + app
	idx, ok := t.indices[key]
	if !ok {
		return
	}
	delete(t.indices, key)
	// If this was the most recently assigned index, free the slot so the
	// retry reuses the same number (keeps headers dense in the common
	// single-goroutine path).
	if idx == t.nextIdx-1 {
		t.nextIdx = idx
	}
}
