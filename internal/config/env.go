package config

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

func environ(opts Options) []string {
	if opts.Environ != nil {
		return opts.Environ
	}
	return os.Environ()
}

func overlayEnv(values sourceValues, env []string) (sourceValues, error) {
	if values.values == nil {
		values.values = map[string]string{}
	}
	if values.sources == nil {
		values.sources = map[string]string{}
	}
	known := schemaByEnvKey()
	unknown := make([]string, 0)
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || !strings.HasPrefix(key, "WK_") {
			continue
		}
		if replacement, ok := removedConfigKeyReplacements[key]; ok {
			return sourceValues{}, fmt.Errorf("%s is no longer supported; use %s", key, replacement)
		}
		if _, ok := known[key]; !ok {
			unknown = append(unknown, key)
			continue
		}
		values.values[key] = strings.TrimSpace(value)
		values.sources[key] = "env"
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return sourceValues{}, fmt.Errorf("unknown config env: %s", strings.Join(unknown, ", "))
	}
	return values, nil
}
