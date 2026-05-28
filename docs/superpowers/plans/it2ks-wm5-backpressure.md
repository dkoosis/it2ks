# it2ks-wm5 — Backpressure: decouple ws reader from writer/resolver

## Problem

Today's pipeline is fully synchronous on the ws-notification goroutine:

```
it2 client ws reader → d.Notifications → capture.Run loop
  ├─ cache.Get(sid)             # may hit resolver (≤1s after fz1)
  └─ writer.Write(now, line)    # disk I/O, fsync stalls possible
```

If either step stalls, `d.Notifications` backs up; the it2 client's reader
goroutine blocks on send; iTerm2 can flag us as a slow consumer and drop the
keystroke subscription. Losing keystrokes silently is the worst outcome for a
psychomotor telemetry layer.

## Design

Insert a bounded in-memory queue between the ws reader and the slow work:

```
ws reader → notifs chan → capture loop (FAST: stamp time, parse) → eventQueue → worker → resolver + writer
```

The capture goroutine's per-event work shrinks to:

1. Read clock once (`now := d.Now()`).
2. Pull `sid` from the notification proto.
3. Enqueue `queuedEvent{now, sid, ks}` non-blocking; on full queue, drop the
   OLDEST event and bump a counter.

All slow work (resolver call, SessionTable.Assign, json.Marshal, writer.Write)
runs on a worker goroutine that drains the queue.

### Design decisions

- **Queue size: 1024.** At a typing burst of 20 keystrokes/sec, 1024 absorbs
  ~50 seconds of writer stall. Far longer than realistic fsync latency
  (milliseconds, p99 sub-second on healthy SSD). Memory: queuedEvent is
  small (~80 bytes incl. proto pointer) → ~80 KB ceiling. Defensible.
- **Worker count: 1.** Single worker preserves event ordering for free. We
  pay nothing for parallelism — the writer's per-line work is microseconds
  unless the disk is sick, in which case extra workers just contend on the
  same fsync. Avoids the cross-event ordering rabbit hole this bead is too
  small to swallow.
- **Drop policy: oldest-first.** Per spec rationale (recent psychomotor
  signal matters more than ancient). Implementation: when send fails (full
  buffered chan), drain one with non-blocking receive, then try send again.
- **Resolve location: worker.** Option (b) from the spec. The ws path stays
  fast — a cold-cache resolver hit (≤1s after fz1) would otherwise defeat
  the entire backpressure design. SessionTable.Assign moves to the worker
  too since it needs `app` (resolver output). SessionTable is already
  concurrency-safe per bundle-mty.
- **Shutdown:** when ctx is cancelled or notif chan closes, close
  eventQueue; worker drains remaining events (best-effort), bounded by a
  2s deadline so a stuck writer can't hold up exit. Pending events get
  flushed; SIGTERM doesn't silently lose buffered keystrokes.

### Drop counter

`atomic.Uint64` on capture, logged periodically (every N drops or every
flush — keep simple: log when first drop occurs and every 1024 drops
thereafter). Exposed for future metrics. Surfaced in graceful shutdown
log line.

### Timestamp travel

The `time.Time` is stamped on the ws goroutine at notification receipt
and travels through the queue inside `queuedEvent`. Worker uses that
exact time for `writer.Write(t, line)` — preserves the bundle-mty single-
clock-per-event contract and prevents date-bucket drift when worker
processes an event seconds after capture.

## Tests (TDD red)

1. **Slow writer doesn't block ws path.** `time.Sleep(200ms)` per Write;
   push >queueSize notifications; assert all are accepted at the queue
   boundary within a short wall-clock window.
2. **Queue-full drops oldest + counter bumps.** Block worker (writer holds
   a mutex); push queueSize+N events; assert drop counter == N, surviving
   events are the most recent ones.
3. **Timestamps preserved across queue delay.** Stamp t=T1, worker delayed
   to t=T2; assert writer received t=T1, not T2 or now().
4. **Graceful shutdown drains queue.** Cancel ctx with events still queued;
   assert worker flushes them before Run returns (within drain deadline).

## Files touched

- `internal/capture/capture.go` — split Run into ws-goroutine (fast) +
  worker (slow); add eventQueue, drop counter, shutdown drain.
- `internal/capture/queue.go` (new) — small queuedEvent struct +
  helpers if it keeps capture.go tidy.
- `internal/capture/bundle_test.go` (or new wm5_test.go) — the four tests
  above.
