// Package capture wires the iTerm2 keystroke subscription to the JSONL writer.
package capture

import (
	pb "github.com/tmc/it2/proto"
)

// Event is one keystroke as written to the JSONL log.
type Event struct {
	TS      int64    `json:"ts"`
	Wall    string   `json:"wall"`
	SID     string   `json:"sid"`
	App     string   `json:"app"`
	Act     string   `json:"act"`
	Key     int32    `json:"key"`
	Char    string   `json:"char"`
	CharRaw string   `json:"char_raw"`
	Mods    []string `json:"mods"`
}

// NewEvent builds an Event from an iTerm2 KeystrokeNotification.
// monoNanos is monotonic nanoseconds since process start; wall is an ISO-8601 string.
// If includeChars is false, char and char_raw are left empty.
func NewEvent(n *pb.KeystrokeNotification, app string, monoNanos int64, wall string, includeChars bool) Event {
	ev := Event{
		TS:   monoNanos,
		Wall: wall,
		SID:  n.GetSession(),
		App:  app,
		Act:  actionString(n.GetAction()),
		Key:  n.GetKeyCode(),
		Mods: modifierStrings(n.GetModifiers()),
	}
	if includeChars {
		ev.Char = n.GetCharacters()
		ev.CharRaw = n.GetCharactersIgnoringModifiers()
	}
	return ev
}

func actionString(a pb.KeystrokeNotification_Action) string {
	switch a {
	case pb.KeystrokeNotification_KEY_DOWN:
		return "down"
	case pb.KeystrokeNotification_KEY_UP:
		return "up"
	case pb.KeystrokeNotification_FLAGS_CHANGED:
		return "flags"
	default:
		return "unknown"
	}
}

func modifierStrings(ms []pb.Modifiers) []string {
	if len(ms) == 0 {
		return nil
	}
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		switch m {
		case pb.Modifiers_CONTROL:
			out = append(out, "control")
		case pb.Modifiers_OPTION:
			out = append(out, "option")
		case pb.Modifiers_COMMAND:
			out = append(out, "command")
		case pb.Modifiers_SHIFT:
			out = append(out, "shift")
		case pb.Modifiers_FUNCTION:
			out = append(out, "function")
		case pb.Modifiers_NUMPAD:
			out = append(out, "numpad")
		}
	}
	return out
}
