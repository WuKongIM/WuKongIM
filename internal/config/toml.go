package config

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type sourceValues struct {
	values  map[string]string
	sources map[string]string
}

func readTOMLValues(path string) (sourceValues, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return sourceValues{}, fmt.Errorf("read %s: %w", path, err)
	}
	var raw map[string]any
	if err := toml.Unmarshal(body, &raw); err != nil {
		return sourceValues{}, fmt.Errorf("parse %s as TOML: %w", path, err)
	}
	flat := map[string]any{}
	flattenTOML("", raw, flat)
	known := schemaByTOMLPath()
	values := map[string]string{}
	sources := map[string]string{}
	unknown := make([]string, 0)
	for path, value := range flat {
		field, ok := known[path]
		if !ok {
			unknown = append(unknown, path)
			continue
		}
		text, err := tomlValueToString(field, value)
		if err != nil {
			return sourceValues{}, err
		}
		values[field.EnvKey] = text
		sources[field.EnvKey] = "toml"
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return sourceValues{}, fmt.Errorf("unknown config key: %s", strings.Join(unknown, ", "))
	}
	return sourceValues{values: values, sources: sources}, nil
}

func flattenTOML(prefix string, value any, out map[string]any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			next := key
			if prefix != "" {
				next = prefix + "." + key
			}
			flattenTOML(next, child, out)
		}
	case []any:
		out[prefix] = typed
	default:
		out[prefix] = typed
	}
}

func tomlValueToString(field fieldSpec, value any) (string, error) {
	switch field.Kind {
	case kindString, kindDuration:
		text, ok := value.(string)
		if !ok {
			return "", fmt.Errorf("parse %s / %s: value must be a string", field.TOMLPath, field.EnvKey)
		}
		return strings.TrimSpace(text), nil
	case kindBool:
		value, ok := value.(bool)
		if !ok {
			return "", fmt.Errorf("parse %s / %s: value must be a bool", field.TOMLPath, field.EnvKey)
		}
		return strconv.FormatBool(value), nil
	case kindInt, kindUint64, kindUint32, kindUint16:
		switch n := value.(type) {
		case int64:
			return strconv.FormatInt(n, 10), nil
		case int:
			return strconv.Itoa(n), nil
		default:
			return "", fmt.Errorf("parse %s / %s: value must be an integer", field.TOMLPath, field.EnvKey)
		}
	case kindFloat:
		switch n := value.(type) {
		case float64:
			return strconv.FormatFloat(n, 'f', -1, 64), nil
		case int64:
			return strconv.FormatInt(n, 10), nil
		default:
			return "", fmt.Errorf("parse %s / %s: value must be a number", field.TOMLPath, field.EnvKey)
		}
	case kindStringList, kindObjectList:
		if text, ok := value.(string); ok {
			return strings.TrimSpace(text), nil
		}
		data, err := json.Marshal(value)
		if err != nil {
			return "", fmt.Errorf("parse %s / %s: %w", field.TOMLPath, field.EnvKey, err)
		}
		return string(data), nil
	default:
		return "", fmt.Errorf("parse %s / %s: unsupported field kind %s", field.TOMLPath, field.EnvKey, field.Kind)
	}
}
