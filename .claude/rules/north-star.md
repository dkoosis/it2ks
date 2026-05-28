# it2ks North Star

Nug: 37b8a6245c36

## What it2ks does

Captures raw keystroke events from iTerm2 → local JSONL logs. No analysis, no network, no aggregation. Psychomotor telemetry layer for mnemosyne's situational fingerprint (Layer 1).

## Key rules

1. **Capture only.** Analysis is a separate project. it2ks writes raw events and does nothing else.
2. **Local only.** Single user (dk), single machine, no network.
3. **Raw events, not aggregates.** Downstream tools (TypeNet, research classifiers) expect raw timestamped keystrokes. Don't pre-aggregate.
4. **Tag by app, filter later.** Capture all sessions, tag with foreground process. Filtering decisions happen at analysis time.
5. **Build on it2.** Use `github.com/tmc/it2` client library for iTerm2 websocket API. Don't reimplement transport.
6. **Baseline before deviation.** No universal keystroke stress markers exist. First weeks are calibration. Deltas from dk's personal baseline are the signal.

## Stack

Go binary, imports it2 client + proto. Managed by launchd. Config at `~/.it2ks/config.toml`. Logs at `~/.it2ks/logs/YYYY-MM-DD.jsonl`.
