# it2ks — iTerm2 Keystroke Capture

Captures raw keystroke events from all iTerm2 sessions and writes them to local JSONL logs. No analysis, no network, no aggregation — just capture. Provides the psychomotor telemetry layer for mnemosyne's situational fingerprint (Layer 1) and feeds downstream tools like TypeNet for cognitive load estimation.

## Why

Keystroke timing is prosody for text — it encodes cognitive load that the final text erases (Banerjee 2014: +10pp state detection over text alone). The research corpus converges on timing-only features from standard keyboards as sufficient (Teh 2013 survey). But none of this is usable without a capture layer. it2ks is that layer.

Single user (dk), local only, privacy-scoped to iTerm2 sessions.

## Architecture

Go binary that connects to iTerm2's websocket API via the `it2` client library (`github.com/tmc/it2/internal/client`). Subscribes to keystroke notifications with `advanced=true` (key-up + flags-changed events) on all sessions. Enriches each event with the foreground process name and session metadata. Writes JSONL to `~/.it2ks/logs/`.

```
iTerm2 (websocket API)
  │
  ├─ KeystrokeNotification (KEY_DOWN, KEY_UP, FLAGS_CHANGED)
  │
  ▼
it2ks (Go binary)
  │
  ├─ timestamp (monotonic nanoseconds + wall clock)
  ├─ tag with session ID + foreground app
  ├─ apply include/exclude filter
  │
  ▼
~/.it2ks/logs/YYYY-MM-DD.jsonl
```

### Why Go, not Python

The peer project `it2` (~/Projects/it2) already has a Go client for iTerm2's websocket API with proto definitions, connection management, session resolution, and keystroke notification subscription. Building on that avoids reimplementing the transport layer in Python. One toolchain, shared patterns.

### Why not an it2 subcommand

it2ks is a long-running daemon. `it2` is a command-line tool for interactive use. Different lifecycle, different concerns. Sharing the client library is the right boundary.

## Dependencies

- `github.com/tmc/it2/internal/client` — websocket connection, request/response, notification channels
- `github.com/tmc/it2/proto` — protobuf types (`KeystrokeNotification`, `NotificationType`, `Notification`)
- `github.com/BurntSushi/toml` — config parsing (or stdlib `tomllib` equivalent for Go... actually, `BurntSushi/toml` is the standard Go choice)

### Required it2 client modification

The current `SubscribeToGenericNotifications` builds a `NotificationRequest` but does not set `KeystrokeMonitorRequest.advanced = true`. We need to either:

1. Add an option to the existing method to pass a `KeystrokeMonitorRequest`, or
2. Build the subscription request directly in it2ks using the proto types and `Client.SendRequest`

Option 2 is simpler and avoids modifying it2. The client's `SendRequest` and `messages` channel are the only surfaces we need.

## Event Schema

Each JSONL line is one keystroke event:

```json
{
  "ts": 1748390400123456789,
  "wall": "2026-05-28T01:20:00.123Z",
  "sid": "session-uuid",
  "app": "claude",
  "act": "down",
  "key": 0,
  "char": "a",
  "char_raw": "a",
  "mods": ["shift"]
}
```

| Field | Type | Description |
|-------|------|-------------|
| `ts` | int64 | Nanoseconds since process start, from Go's monotonic clock (`time.Since(startTime).Nanoseconds()`). Precision clock for computing intervals — immune to wall-clock adjustments |
| `wall` | string | ISO 8601 wall-clock timestamp (`time.Now().UTC()`). For human readability and cross-session correlation, not timing math |
| `sid` | string | iTerm2 session ID |
| `app` | string | Foreground process name (`claude`, `vim`, `zsh`, etc.), from iTerm2's `jobName` session variable. Cached per session and refreshed periodically (every ~5s) to avoid a variable query on every keystroke |
| `act` | string | `down`, `up`, or `flags` |
| `key` | int | Virtual keycode |
| `char` | string | `characters` from the KeystrokeNotification |
| `char_raw` | string | `charactersIgnoringModifiers` |
| `mods` | []string | Active modifiers |

This schema captures everything the research feature sets need:
- **Hold time** — down→up for the same key (requires `advanced=true`)
- **Flight time** — up→next down
- **Digraph latency** — consecutive down events
- **Revision behavior** — keycode for backspace/delete
- **Word boundaries** — from `char` field
- **Pause distributions** — gaps implicit in timestamps

### Volume estimate

~5 keys/sec active typing, ~2-4 hours/day = 36K-72K events/day. At ~80 bytes/event: 3-6 MB/day. ~1-2 GB/year. Trivially storable.

## Configuration

`~/.it2ks/config.toml`:

```toml
[capture]
log_dir = "~/.it2ks/logs"
include_chars = true

[filter]
# Only capture these apps. Empty = capture all.
# Matching is case-insensitive and whitespace-trimmed
# (e.g. "Vim", " vim ", "VIM" all match app "vim").
apps_include = []
# Exclude these apps. Applied after apps_include.
apps_exclude = []
```

Missing config file → all defaults (capture all sessions, include characters, log to `~/.it2ks/logs/`).

## File Layout

```
~/Projects/it2ks/           ← this repo (source)
  cmd/it2ks/main.go
  internal/capture/         ← subscription, event formatting, writer
  internal/config/           ← TOML parsing
  go.mod
  go.sum
  docs/
  Makefile
  com.dk.it2ks.plist         ← launchd template

~/.it2ks/                    ← runtime data
  config.toml
  logs/
    2026-05-27.jsonl
    2026-05-28.jsonl
```

## Lifecycle Management

Managed by `launchd` via a user-level plist at `~/Library/LaunchAgents/com.dk.it2ks.plist`.

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.dk.it2ks</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/local/bin/it2ks</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>/tmp/it2ks.out.log</string>
  <key>StandardErrorPath</key>
  <string>/tmp/it2ks.err.log</string>
</dict>
</plist>
```

`make install` copies the binary and loads the plist. `make uninstall` unloads and removes.

Note: iTerm2 must be running with the Python API enabled for the websocket connection to succeed. If iTerm2 isn't running, it2ks retries on a backoff until it connects. launchd's `KeepAlive` restarts the process if it exits.

## Error Handling

- **iTerm2 not running** — retry connection with exponential backoff (1s, 2s, 4s... capped at 60s)
- **Session closes** — notification stream continues for other sessions; no special handling needed
- **Log directory missing** — create `~/.it2ks/logs/` on startup
- **Disk write failure** — log error to stderr, drop the event, keep capturing
- **Config file missing** — use defaults
- **Day rollover** — next event opens new file by date; no explicit rotation

No retry logic for individual events. If a write fails, the event is lost. For baseline-building, a few dropped events don't affect statistics.

## Testing

1. **Unit tests** (no iTerm2 required):
   - Config loading: defaults, partial config, full config, missing file
   - Event formatting: notification proto → JSONL line
   - Log path generation: date-based filename
   - App filtering: include/exclude logic

2. **Integration smoke test** (requires iTerm2):
   - Install, type in sessions, inspect JSONL output
   - Verify: events appear, timestamps are monotonic, app tagging works, filtering works

3. **Log validator script**:
   - Reads a log file, validates schema: required fields present, `ts` monotonically increasing within a session, `act` is valid, `mods` from known set

No mocking of the iTerm2 API.

## What this does NOT do

- Analysis, aggregation, or feature extraction
- Network communication (no Trixi push, no remote storage)
- Real-time dashboards or summary stats
- Baseline calibration or deviation detection
- Log cleanup or retention policies

These are all downstream concerns for separate projects once we have real data.

## Related nugs

- `1fe653da94e4` — iTerm2 keystroke telemetry feasibility and architecture (original research)
- `105ad6eb9d8f` — open-source toolkits for text-based behavioral biometrics (TypeNet, ConvoKit)
- `a75121eeea29` — mnemosyne situational fingerprint (Layer 1 psychomotor signals)
- `4096a8b04323` — Teh 2013 survey: timing features on standard keyboards suffice
- `d44739c1245f` — Banerjee 2014: keystroke timing as prosody, +10pp over text alone
