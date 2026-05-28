# it2ks bundle: capture↔writer contract redesign

Beads: it2ks-mty, it2ks-kke, it2ks-fz1, it2ks-n56
Branch: fix/it2ks-bundle-mty
Date: 2026-05-28

## Why one bundle

The audit (4 separate bugs) all root in the same structural defect: capture
and writer each own pieces of state (clock reads, day strings, session table,
header-vs-index ordering) that should be jointly invariant. Fix once,
correctly, by tightening the contract between them.

## Contract

### One clock per event (mty)

`capture.Run` computes `now := d.Now()` once per notification and passes
`now` explicitly to `d.Writer.Write(now, line)`. Writer no longer reads a
clock for per-event rotation; it uses the passed time. Writer's own
`now` is kept only for legacy paths (header commit-time fields, if any)
and for tests that don't pass a time.

New writer surface:

```go
type LineWriter interface {
    Write(t time.Time, line []byte) error
}
```

(Renamed from `Write(line []byte) error`. cmd/it2ks adapter takes the
time and forwards it.)

### Session state lifted to caller (kke)

`sessions map`, `nextIdx`, `curDate` move out of `capture.Run` into a
new `SessionTable` type defined in `internal/capture/session_table.go`.
`main` constructs one `SessionTable` and passes it via `Deps.Sessions`.
Reconnects reuse it. Same-day file gets monotone `s` indices across
reconnects.

```go
type SessionTable struct {
    mu       sync.Mutex
    indices  map[string]int  // "sid|app" -> s
    nextIdx  int
    curDate  string
}

// Assign returns (s, isNew, dateChanged). On dateChanged the caller
// is responsible for re-emitting headers for any keys it still tracks.
func (t *SessionTable) Assign(date, sid, app string) (s int, isNew, dateChanged bool)

// Rollback undoes a freshly-assigned index when the header write failed.
// Only valid immediately after Assign returns isNew=true.
func (t *SessionTable) Rollback(sid, app string)
```

`Assign` resets indices when `date != curDate`.

### Resolver timeout (fz1)

`ResolveApp` in `cmd/it2ks/main.go` wraps inbound ctx with
`context.WithTimeout(ctx, cfg.ResolveTimeout)`. Default 1s. On
timeout returns ("", err) — the AppCache negative-caches that error
(f7u already merged), so subsequent events on the same sid hit the
1s negTTL and don't reach the resolver.

Add `Capture.ResolveTimeout` (duration, default `1s`) to config TOML.

### Header-then-commit (n56)

`SessionTable.Assign` reserves an index but does not commit until the
caller confirms. Pattern in capture.Run:

```go
s, isNew, _ := d.Sessions.Assign(date, sid, app)
if isNew {
    rec := SessionRecord{...S: s, ...}
    if line, err := json.Marshal(rec); err != nil {
        log.Printf(...)
        d.Sessions.Rollback(sid, app)
        continue
    } else if err := d.Writer.Write(now, line); err != nil {
        log.Printf(...)
        d.Sessions.Rollback(sid, app)
        continue
    }
}
```

On rollback the next event for (sid, app) re-tries the header with a
fresh `nextIdx`.

### Day boundary

When `Assign` sees a new date it clears its internal map and resets
`nextIdx` to 0, returns `dateChanged=true`. Live sessions naturally
re-emit a header on their next event in the new day (since `isNew`
becomes true again). We do not pro-actively re-emit headers for
silent sessions — they get a header when they next produce a
keystroke. This is acceptable because the file's invariant is
"every event has a preceding header for its s in this file" and that
holds (no s is assigned without a header write).

## Non-goals

- Backfill old logs.
- Cross-day session-ID continuity (events on either side of midnight
  legitimately have different `s` values).

## Test plan

- mty: clock that ticks across midnight between capture's read and
  writer's write — both header and event land in the day-N file
  (the one read by capture), no index collision.
- kke: two sequential Run calls into same recording writer with
  distinct sids — combined output has no duplicate `s`.
- fz1: resolver that blocks > 1s — capture loop continues processing
  within timeout + epsilon; subsequent events emit "unknown" via
  negative-cache.
- n56: writer that fails first header write, succeeds after — second
  event for same sid carries a fresh header with a fresh `s`.

## Risk

- Adapter signature change touches cmd/it2ks. Small blast radius.
- Race detector enabled for capture + writer in verification.
