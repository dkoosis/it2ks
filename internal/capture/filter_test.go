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
