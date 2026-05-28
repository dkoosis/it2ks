// Package config loads it2ks runtime configuration from TOML.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// errUnsupportedTildeForm is returned when a path uses tilde notation we
// don't expand (bare "~", "~user/"). Only "~/" prefix is supported.
var errUnsupportedTildeForm = errors.New(`unsupported tilde form: only "~/" prefix is supported (bare "~" and "~user/" are not expanded)`)

type Config struct {
	LogDir       string   `toml:"log_dir"`
	IncludeChars bool     `toml:"include_chars"`
	AppsInclude  []string `toml:"apps_include"`
	AppsExclude  []string `toml:"apps_exclude"`
}

type fileFormat struct {
	Capture struct {
		LogDir       string `toml:"log_dir"`
		IncludeChars *bool  `toml:"include_chars"`
	} `toml:"capture"`
	Filter struct {
		AppsInclude []string `toml:"apps_include"`
		AppsExclude []string `toml:"apps_exclude"`
	} `toml:"filter"`
}

// Load reads the TOML config at path. Missing file → all defaults.
func Load(path string) (Config, error) {
	cfg := defaults()

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}

	var ff fileFormat
	if err := toml.Unmarshal(data, &ff); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}

	if ff.Capture.LogDir != "" {
		expanded, err := expandHome(ff.Capture.LogDir)
		if err != nil {
			return Config{}, fmt.Errorf("config %s: log_dir: %w", path, err)
		}
		cfg.LogDir = expanded
	}
	if ff.Capture.IncludeChars != nil {
		cfg.IncludeChars = *ff.Capture.IncludeChars
	}
	cfg.AppsInclude = append([]string(nil), ff.Filter.AppsInclude...)
	cfg.AppsExclude = append([]string(nil), ff.Filter.AppsExclude...)
	return cfg, nil
}

func defaults() Config {
	home, _ := os.UserHomeDir()
	return Config{
		LogDir:       filepath.Join(home, ".it2ks", "logs"),
		IncludeChars: false,
	}
}

// expandHome expands a leading "~/" to the current user's home directory.
// Any other leading "~" form (bare "~", "~user/...") is rejected as a
// validation error — silently treating those as literal paths caused logs
// to land in unexpected directories under launchd.
func expandHome(p string) (string, error) {
	if len(p) == 0 || p[0] != '~' {
		return p, nil
	}
	if p == "~/" || (len(p) >= 2 && p[:2] == "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir: %w", err)
		}
		return filepath.Join(home, p[2:]), nil
	}
	return "", fmt.Errorf("%w: %q", errUnsupportedTildeForm, p)
}
