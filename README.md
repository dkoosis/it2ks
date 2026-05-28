# it2ks

**iTerm2 keystroke capture for personal psychomotor telemetry.**

`it2ks` is a small Go daemon that subscribes to iTerm2's keystroke notifications and writes raw, timestamped events to local JSONL files. It does one thing: capture. Analysis lives elsewhere.

---

## Why this exists

Keystroke timing is a cheap, continuous signal about the person at the keyboard — speed, rhythm, hesitation, fatigue, error patterns. The research literature (keystroke dynamics, TypeNet, cognitive-load studies) has decades of techniques for turning raw key events into useful features. None of them work without raw events to feed on.

`it2ks` is the capture layer for one user (the author), on one machine, all day. It produces the substrate; downstream tools compute features and baselines.

It is **Layer 1** of a larger personal-telemetry project (`mnemosyne`). It is designed to be useful on its own.

## Non-goals

- **No analysis.** No aggregates, no rolling stats, no classifiers. Just events.
- **No network.** Everything is local. No telemetry leaves the machine.
- **No multi-user.** Single user, single machine. Not a product.
- **No transport reimplementation.** Uses [`github.com/tmc/it2`](https://github.com/tmc/it2) for the iTerm2 websocket API.
- **No pre-filtering.** Captures all sessions, tags by foreground app. Filtering happens at analysis time.

## Relationship to iTerm2

iTerm2 (since v3.3) exposes a [Python/websocket API](https://iterm2.com/python-api/) that, with the **Keystroke Monitor** permission granted, streams keystroke notifications for every session. `it2ks` is a long-lived subscriber to that stream. It does not read your terminal scrollback, your shell history, or the contents of any file — only the key events iTerm2 publishes.

You must grant the permission once (iTerm2 → Settings → General → Magic → "Enable Python API" + accept the script's permission prompt on first run). If iTerm2 quits, `it2ks` reconnects when it restarts.

## Architecture

```
  iTerm2  ──(websocket, keystroke notifications)──▶  it2ks
                                                       │
                                              tag with foreground app
                                                       │
                                                       ▼
                                       ~/.it2ks/logs/YYYY-MM-DD.jsonl
                                                       │
                                                       ▼
                                          (downstream analysis tools)
```

- **Process:** single Go binary, managed by `launchd` (`com.dk.it2ks.plist`), `KeepAlive=true`.
- **Storage:** one JSONL file per UTC day under `~/.it2ks/logs/`. Append-only. Rotated by date, never edited.
- **Config:** `~/.it2ks/config.toml` (optional; defaults are sane).

## Event schema

Two record types, both newline-delimited JSON.

**Session record** — emitted when a new iTerm2 session is observed:

```json
{"type":"session","s":0,"sid":"334F926C-...","app":"node","t0":"2026-05-28T05:27:08.246284Z"}
```

| field   | meaning                                                  |
|---------|----------------------------------------------------------|
| `s`     | short session index, stable within this log file         |
| `sid`   | iTerm2's session UUID                                    |
| `app`   | foreground process name at session start (`zsh`, `vim`, `node`, …) |
| `t0`    | wall-clock start, RFC3339 microseconds, UTC              |

**Keystroke record:**

```json
{"ts":503438167,"wall":"2026-05-28T05:27:08.246284Z","s":0,"act":"down","key":51,"char":""}
```

| field   | meaning                                                  |
|---------|----------------------------------------------------------|
| `ts`    | monotonic timestamp, nanoseconds since process start     |
| `wall`  | wall-clock time, RFC3339 microseconds, UTC               |
| `s`     | session index (joins to the session record)              |
| `act`   | `down` — iTerm2 currently only publishes key-down events |
| `key`   | iTerm2 keycode (integer)                                 |
| `char`  | rendered character if printable, else empty              |

> **Note on `act`:** iTerm2's API does not currently emit key-up events, so dwell time (key-down → key-up duration) is not directly recoverable. Inter-key interval (down → next down) and most flight-time and pause-distribution analyses still work. If iTerm2 ever adds key-up, this schema extends cleanly.

A validator lives at `scripts/validate-log.go` — `go run scripts/validate-log.go ~/.it2ks/logs/$(date -u +%Y-%m-%d).jsonl` to sanity-check a day's log.

## Designed for downstream analysis

The schema and capture discipline are chosen so that standard keystroke-analysis methods can be applied without reshaping the data:

| Method family                          | What it needs           | Supported |
|----------------------------------------|-------------------------|-----------|
| Inter-key interval (IKI) statistics    | down-event timestamps   | yes       |
| Pause-distribution / burst analysis    | down-event timestamps   | yes       |
| Backspace and error-rate features      | keycodes                | yes       |
| Per-app conditioning                   | foreground app tag      | yes       |
| Digraph / trigraph flight time         | ordered down events     | yes       |
| Keystroke-dynamics identity (TypeNet)  | flight time, per-app    | yes       |
| Dwell time (key-down → key-up)         | key-up events           | **no** (iTerm2 limitation) |

Downstream tools are expected to compute baselines per `(app, time-of-day)` and report deviations, not absolute thresholds — individual variance dominates population effects in this domain.

## Install

Requires Go 1.26+ and iTerm2 with the Python API enabled.

```bash
git clone https://github.com/dkoosis/it2ks.git
cd it2ks
make install     # builds, installs to ~/.local/bin, loads launchd plist
```

On first run, accept the iTerm2 permission prompt. Logs begin streaming to `~/.it2ks/logs/`.

To stop:

```bash
make uninstall
```

## Develop

```bash
make help        # list all targets
make check       # vet + lint + test + build (fast inner loop)
make audit       # exhaustive: race + dupe + vuln on top of check
make validate    # validate today's JSONL against the schema
```

Project layout:

```
cmd/it2ks/        entry point
internal/capture/ iTerm2 subscription + event handling
internal/writer/  JSONL writer + rotation
internal/config/  TOML config loader
scripts/          validator
com.dk.it2ks.plist  launchd unit
```

## Status

MVP shipped and running continuously on the author's machine. Schema is `v1` and considered stable for downstream consumers. Issue tracking lives in [beads](https://github.com/gastownhall/beads) (`bd ready` for current state).

## License

Personal project. No license granted; do not redistribute.
