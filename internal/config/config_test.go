package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_MissingFileReturnsDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "does-not-exist.toml"))
	if err != nil {
		t.Fatalf("Load() returned error for missing file: %v", err)
	}
	if cfg.IncludeChars {
		t.Errorf("IncludeChars default = true, want false (opt-in)")
	}
	if cfg.LogDir == "" {
		t.Errorf("LogDir default is empty, want non-empty path")
	}
	if len(cfg.AppsInclude) != 0 {
		t.Errorf("AppsInclude default = %v, want empty", cfg.AppsInclude)
	}
	if len(cfg.AppsExclude) != 0 {
		t.Errorf("AppsExclude default = %v, want empty", cfg.AppsExclude)
	}
}

func TestLoad_PartialConfigOverridesOnlySetFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(`
[capture]
include_chars = false

[filter]
apps_exclude = ["vim"]
`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.IncludeChars {
		t.Errorf("IncludeChars = true, want false")
	}
	if cfg.LogDir == "" {
		t.Errorf("LogDir is empty — default should still apply")
	}
	if len(cfg.AppsExclude) != 1 || cfg.AppsExclude[0] != "vim" {
		t.Errorf("AppsExclude = %v, want [vim]", cfg.AppsExclude)
	}
}

func TestLoad_ExpandsHomeInLogDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.toml")
	if err := os.WriteFile(path, []byte(`
[capture]
log_dir = "~/custom/logs"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, "custom", "logs")
	if cfg.LogDir != want {
		t.Errorf("LogDir = %q, want %q", cfg.LogDir, want)
	}
}

func TestLoad_RejectsBareTildeLogDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.toml")
	if err := os.WriteFile(path, []byte(`
[capture]
log_dir = "~"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatalf("Load() returned nil error for log_dir = \"~\", want validation error")
	}
}

func TestLoad_RejectsTildeUserLogDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.toml")
	if err := os.WriteFile(path, []byte(`
[capture]
log_dir = "~user/logs"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatalf("Load() returned nil error for log_dir = \"~user/logs\", want validation error")
	}
}
