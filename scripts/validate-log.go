//go:build validatelog

// Run with: go run -tags=validatelog scripts/validate-log.go <log-file>
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

type event struct {
	TS   int64    `json:"ts"`
	Wall string   `json:"wall"`
	SID  string   `json:"sid"`
	App  string   `json:"app"`
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

	lastTS := map[string]int64{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var errs, n int
	for scanner.Scan() {
		n++
		var ev event
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			fmt.Printf("line %d: parse: %v\n", n, err)
			errs++
			continue
		}
		if ev.Wall == "" || ev.SID == "" {
			fmt.Printf("line %d: missing required field\n", n)
			errs++
		}
		if !validActs[ev.Act] {
			fmt.Printf("line %d: bad act %q\n", n, ev.Act)
			errs++
		}
		for _, m := range ev.Mods {
			if !validMods[m] {
				fmt.Printf("line %d: bad mod %q\n", n, m)
				errs++
			}
		}
		if prev, ok := lastTS[ev.SID]; ok && ev.TS < prev {
			fmt.Printf("line %d: ts regressed in session %s (%d < %d)\n", n, ev.SID, ev.TS, prev)
			errs++
		}
		lastTS[ev.SID] = ev.TS
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	fmt.Printf("checked %d events, %d errors\n", n, errs)
	if errs > 0 {
		os.Exit(1)
	}
}
