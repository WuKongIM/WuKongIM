package scripts_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCloudSimulationDiagnosisSchemaUsesStrictObjectShapes(t *testing.T) {
	schemaPath := filepath.Join(repoRoot(t), ".github", "cloud-sim", "diagnosis.schema.json")
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	var schema any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("decode diagnosis schema: %v", err)
	}
	var walk func(any, string)
	walk = func(value any, path string) {
		switch typed := value.(type) {
		case map[string]any:
			if propertiesValue, ok := typed["properties"]; ok {
				properties, ok := propertiesValue.(map[string]any)
				if !ok || typed["type"] != "object" || typed["additionalProperties"] != false {
					t.Fatalf("%s is not a strict object schema", path)
				}
				requiredValues, ok := typed["required"].([]any)
				if !ok || len(requiredValues) != len(properties) {
					t.Fatalf("%s does not require every property", path)
				}
				required := make(map[string]bool, len(requiredValues))
				for _, item := range requiredValues {
					name, ok := item.(string)
					if !ok {
						t.Fatalf("%s has a non-string required property", path)
					}
					required[name] = true
				}
				for name := range properties {
					if !required[name] {
						t.Fatalf("%s property %s is optional", path, name)
					}
				}
			}
			if _, hasEnum := typed["enum"]; hasEnum {
				if _, hasType := typed["type"]; !hasType {
					t.Fatalf("%s enum has no explicit type", path)
				}
			}
			if _, hasConst := typed["const"]; hasConst {
				if _, hasType := typed["type"]; !hasType {
					t.Fatalf("%s const has no explicit type", path)
				}
			}
			for name, child := range typed {
				walk(child, path+"/"+name)
			}
		case []any:
			for _, child := range typed {
				walk(child, path)
			}
		}
	}
	walk(schema, "$")
}

func TestCloudSimulationAnalyzeHelpDescribesChatGPTContract(t *testing.T) {
	script := filepath.Join(repoRoot(t), "scripts", "cloud-sim", "analyze.sh")
	command := exec.Command("bash", script, "--help")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("analyze --help: %v\n%s", err, output)
	}
	for _, fragment := range []string{
		"Usage: ./scripts/cloud-sim/analyze.sh RUN_ID",
		"ChatGPT",
		"--diagnostic-focus",
		"--allow-fix-pr",
	} {
		if !strings.Contains(string(output), fragment) {
			t.Fatalf("analyze --help missing %q:\n%s", fragment, output)
		}
	}
	source, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"diagnosis_timeout_seconds", "2700", "wk_start_bounded",
		"trap 'handle_signal 129' HUP", "trap 'handle_signal 130' INT TERM",
		"resolve_codex_bin", "Codex 0.140.0 or newer",
	} {
		if !strings.Contains(string(source), fragment) {
			t.Fatalf("analyze script missing bounded local Codex lifecycle %q", fragment)
		}
	}
}
