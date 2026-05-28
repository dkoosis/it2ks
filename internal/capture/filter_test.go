package capture

import "testing"

func TestFilter_EmptyAllowsAll(t *testing.T) {
	f := NewFilter(nil, nil)
	if !f.Allow("anything") {
		t.Error("empty filter should allow all apps")
	}
	if !f.Allow("") {
		t.Error("empty filter should allow even unknown app")
	}
}

func TestFilter_IncludeOnlyListed(t *testing.T) {
	f := NewFilter([]string{"claude", "vim"}, nil)
	if !f.Allow("claude") {
		t.Error("claude should be allowed")
	}
	if f.Allow("zsh") {
		t.Error("zsh should be blocked when not in include list")
	}
}

func TestFilter_ExcludeBlocksListed(t *testing.T) {
	f := NewFilter(nil, []string{"1password"})
	if f.Allow("1password") {
		t.Error("1password should be blocked by exclude")
	}
	if !f.Allow("zsh") {
		t.Error("zsh should pass exclude filter")
	}
}

func TestFilter_ExcludeAppliesAfterInclude(t *testing.T) {
	f := NewFilter([]string{"claude", "1password"}, []string{"1password"})
	if !f.Allow("claude") {
		t.Error("claude should pass include")
	}
	if f.Allow("1password") {
		t.Error("1password must be blocked by exclude even though in include list")
	}
}

func TestFilter_ExcludeCaseInsensitive(t *testing.T) {
	f := NewFilter(nil, []string{"Vim"})
	if f.Allow("vim") {
		t.Error("vim should be blocked when exclude has 'Vim' (case-insensitive)")
	}
}

func TestFilter_ExcludeTrimsWhitespace(t *testing.T) {
	f := NewFilter(nil, []string{" zsh "})
	if f.Allow("zsh") {
		t.Error("zsh should be blocked when exclude has ' zsh ' (whitespace-trimmed)")
	}
}

func TestFilter_IncludeCaseInsensitive(t *testing.T) {
	f := NewFilter([]string{"VIM", " Zsh "}, nil)
	if !f.Allow("vim") {
		t.Error("vim should pass include when list has 'VIM'")
	}
	if !f.Allow("zsh") {
		t.Error("zsh should pass include when list has ' Zsh '")
	}
	if f.Allow("bash") {
		t.Error("bash should be blocked, not in include list")
	}
}

func TestFilter_LookupNormalizesAppArg(t *testing.T) {
	// App label from upstream may itself have varying case/whitespace.
	f := NewFilter(nil, []string{"1password"})
	if f.Allow(" 1Password ") {
		t.Error("' 1Password ' should be blocked by '1password' exclude entry")
	}
}
