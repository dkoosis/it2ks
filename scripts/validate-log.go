//go:build validatelog

// Run with: go run -tags=validatelog scripts/validate-log.go <log-file>
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

type record struct {
	// Common.
	Type string `json:"type"`

	// Session-header fields.
	S   int    `json:"s"`
	SID string `json:"sid"`
	App string `json:"app"`
	T0  string `json:"t0"`

	// Event fields (S is shared with header).
	TS   int64    `json:"ts"`
	Wall string   `json:"wall"`
	Act  string   `json:"act"`
	Mods []string `json:"mods"`
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: validate-log <file>")
		os.Exit(2)
	}
	f, err := os.Open(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	defer f.Close()

	validActs := map[string]bool{"down": true, "up": true, "flags": true}
	validMods := map[string]bool{
		"control": true, "option": true, "command": true,
		"shift": true, "function": true, "numpad": true,
	}

	knownSessions := map[int]bool{}
	lastTS := map[int]int64{}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var errs, events, sessions, n int
	for scanner.Scan() {
		n++
		// Peek the line to detect record type by checking for "type":"session".
		var r record
		if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
			fmt.Printf("line %d: parse: %v\n", n, err)
			errs++
			continue
		}
		if r.Type == "session" {
			sessions++
			if r.SID == "" || r.App == "" || r.T0 == "" {
				fmt.Printf("line %d: session header missing required field\n", n)
				errs++
			}
			knownSessions[r.S] = true
			continue
		}
		// Otherwise it's an event.
		events++
		if r.Wall == "" {
			fmt.Printf("line %d: event missing wall\n", n)
			errs++
		}
		if !knownSessions[r.S] {
			fmt.Printf("line %d: event references unknown session s=%d\n", n, r.S)
			errs++
		}
		if !validActs[r.Act] {
			fmt.Printf("line %d: bad act %q\n", n, r.Act)
			errs++
		}
		for _, m := range r.Mods {
			if !validMods[m] {
				fmt.Printf("line %d: bad mod %q\n", n, m)
				errs++
			}
		}
		if prev, ok := lastTS[r.S]; ok && r.TS < prev {
			fmt.Printf("line %d: ts regressed in session s=%d (%d < %d)\n", n, r.S, r.TS, prev)
			errs++
		}
		lastTS[r.S] = r.TS
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	fmt.Printf("checked %d lines (%d session headers, %d events), %d errors\n", n, sessions, events, errs)
	if errs > 0 {
		os.Exit(1)
	}
}
