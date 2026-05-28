package capture

import "strings"

type Filter struct {
	include map[string]struct{}
	exclude map[string]struct{}
}

// normalize lowercases and trims whitespace so config entries like "Vim" or
// " zsh " match the lowercase, trimmed app labels reported by iTerm2.
func normalize(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func NewFilter(include, exclude []string) *Filter {
	f := &Filter{}
	if len(include) > 0 {
		f.include = make(map[string]struct{}, len(include))
		for _, a := range include {
			f.include[normalize(a)] = struct{}{}
		}
	}
	if len(exclude) > 0 {
		f.exclude = make(map[string]struct{}, len(exclude))
		for _, a := range exclude {
			f.exclude[normalize(a)] = struct{}{}
		}
	}
	return f
}

// Allow reports whether an event tagged with app should be captured.
// Matching is case-insensitive and whitespace-trimmed on both sides.
func (f *Filter) Allow(app string) bool {
	key := normalize(app)
	if f.include != nil {
		if _, ok := f.include[key]; !ok {
			return false
		}
	}
	if f.exclude != nil {
		if _, ok := f.exclude[key]; ok {
			return false
		}
	}
	return true
}
