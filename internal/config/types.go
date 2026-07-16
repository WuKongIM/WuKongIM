package config

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/app"
)

var defaultConfigPaths = []string{
	"./wukongim.toml",
	"./conf/wukongim.toml",
	"/etc/wukongim/wukongim.toml",
}

// Options configures product startup config loading.
type Options struct {
	// Args are command-line arguments after the binary name.
	Args []string
	// Environ overrides the process environment for tests. Empty uses os.Environ.
	Environ []string
}

// DefaultPaths returns the implicit TOML config lookup order.
func DefaultPaths() []string {
	out := make([]string, len(defaultConfigPaths))
	copy(out, defaultConfigPaths)
	return out
}

// Load reads TOML config and WK_* environment overrides into app.Config.
func Load(opts Options) (app.Config, error) {
	configPath, err := parseConfigPath(opts.Args)
	if err != nil {
		return app.Config{}, err
	}
	values := sourceValues{values: map[string]string{}, sources: map[string]string{}}
	loadedConfigPath := ""
	var attempted []string
	if configPath != "" {
		values, err = readTOMLValues(configPath)
		if err != nil {
			return app.Config{}, fmt.Errorf("load config: %w", err)
		}
		loadedConfigPath, err = absoluteConfigPath(configPath)
		if err != nil {
			return app.Config{}, err
		}
	} else {
		values, loadedConfigPath, attempted, err = readDefaultTOMLValues()
		if err != nil {
			return app.Config{}, err
		}
	}
	values, err = overlayEnv(values, environ(opts))
	if err != nil {
		return app.Config{}, fmt.Errorf("load config: %w", err)
	}
	cfg, err := buildConfig(values.values)
	if err != nil {
		if len(attempted) > 0 {
			missing := missingRequiredConfigKeys(values.values)
			if len(missing) > 0 {
				return app.Config{}, fmt.Errorf(
					"load config: no default config file found (tried %s); missing required config keys: %s",
					strings.Join(attempted, ", "),
					strings.Join(missing, ", "),
				)
			}
		}
		return app.Config{}, fmt.Errorf("load config: %w", err)
	}
	cfg.StartupConfigSnapshot = buildStartupSnapshot(values, cfg.NodeID)
	cfg.ConfigPath = loadedConfigPath
	return cfg, nil
}

func parseConfigPath(args []string) (string, error) {
	fs := flag.NewFlagSet("wukongim", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "path to wukongim.toml file")
	if err := fs.Parse(args); err != nil {
		return "", fmt.Errorf("parse flags: %w", err)
	}
	return strings.TrimSpace(*configPath), nil
}

func readDefaultTOMLValues() (sourceValues, string, []string, error) {
	attempted := make([]string, 0, len(defaultConfigPaths))
	for _, candidate := range defaultConfigPaths {
		attempted = append(attempted, candidate)
		if _, err := os.Stat(candidate); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return sourceValues{}, "", nil, fmt.Errorf("stat %s: %w", candidate, err)
		}
		values, err := readTOMLValues(candidate)
		if err != nil {
			return sourceValues{}, "", nil, err
		}
		resolved, err := absoluteConfigPath(candidate)
		if err != nil {
			return sourceValues{}, "", nil, err
		}
		return values, resolved, nil, nil
	}
	return sourceValues{values: map[string]string{}, sources: map[string]string{}}, "", attempted, nil
}

func absoluteConfigPath(path string) (string, error) {
	resolved, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve config path: %w", err)
	}
	return resolved, nil
}
