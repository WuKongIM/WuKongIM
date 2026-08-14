package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// RedactDiagnosticTOML parses startup TOML, validates every key against the
// public schema, and replaces fields marked DiagnosticSensitive. It returns no
// bytes when parsing, schema validation, or encoding fails so callers cannot
// accidentally publish a partially redacted configuration.
func RedactDiagnosticTOML(body []byte) ([]byte, error) {
	var raw map[string]any
	if err := toml.Unmarshal(body, &raw); err != nil {
		// Parser diagnostics may include source excerpts. Diagnostic config can
		// contain credentials, so expose only a stable content-free error.
		return nil, fmt.Errorf("parse diagnostic config as TOML")
	}

	flat := make(map[string]any)
	flattenTOML("", raw, flat)
	known := schemaByTOMLPath()
	unknown := unknownDiagnosticTOMLPaths("", raw, known)
	for path, value := range flat {
		field, ok := known[path]
		if !ok {
			continue
		}
		if _, err := tomlValueToString(field, value); err != nil {
			return nil, err
		}
	}
	if len(unknown) != 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("unknown config key: %s", strings.Join(unknown, ", "))
	}

	for _, field := range SchemaFields() {
		if !field.DiagnosticSensitive {
			continue
		}
		if _, present := flat[field.TOMLPath]; !present {
			continue
		}
		replacement := any("******")
		if field.Kind == string(kindObjectList) || field.Kind == string(kindStringList) {
			replacement = []any{}
		}
		if !replaceTOMLPath(raw, strings.Split(field.TOMLPath, "."), replacement) {
			return nil, fmt.Errorf("redact diagnostic config key %s: parsed path is unavailable", field.TOMLPath)
		}
	}

	redacted, err := toml.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("encode redacted diagnostic config: %w", err)
	}
	return redacted, nil
}

func unknownDiagnosticTOMLPaths(prefix string, value any, known map[string]fieldSpec) []string {
	if table, ok := value.(map[string]any); ok {
		if prefix != "" {
			knownPrefix := false
			for path := range known {
				if strings.HasPrefix(path, prefix+".") {
					knownPrefix = true
					break
				}
			}
			if !knownPrefix {
				return []string{prefix}
			}
		}
		unknown := make([]string, 0)
		for key, child := range table {
			path := key
			if prefix != "" {
				path = prefix + "." + key
			}
			unknown = append(unknown, unknownDiagnosticTOMLPaths(path, child, known)...)
		}
		return unknown
	}
	if _, ok := known[prefix]; !ok {
		return []string{prefix}
	}
	return nil
}

func replaceTOMLPath(current map[string]any, path []string, replacement any) bool {
	if len(path) == 0 {
		return false
	}
	if len(path) == 1 {
		if _, ok := current[path[0]]; !ok {
			return false
		}
		current[path[0]] = replacement
		return true
	}
	next, ok := current[path[0]].(map[string]any)
	if !ok {
		return false
	}
	return replaceTOMLPath(next, path[1:], replacement)
}
