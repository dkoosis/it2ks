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
		cfg.LogDir = expandHome(ff.Capture.LogDir)
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
		IncludeChars: true,
	}
}

func expandHome(p string) string {
	if len(p) >= 2 && p[:2] == "~/" {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}
