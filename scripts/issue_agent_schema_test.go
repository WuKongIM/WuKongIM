package scripts_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/google/jsonschema-go/jsonschema"
)

func TestIssueAgentSchemas(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		filename string
		id       string
		version  int
		schema   func(*testing.T) *jsonschema.Schema
	}{
		{
			name:     "state",
			filename: "state.schema.json",
			id:       "https://wukongim.github.io/schemas/issue-agent/state-v2.json",
			version:  2,
			schema: func(t *testing.T) *jsonschema.Schema {
				t.Helper()
				return issueAgentSchemaFor[issueagent.IssueAgentState](t, 2)
			},
		},
		{
			name:     "context bundle",
			filename: "context-bundle.schema.json",
			id:       "https://wukongim.github.io/schemas/issue-agent/context-bundle-v2.json",
			version:  2,
			schema: func(t *testing.T) *jsonschema.Schema {
				t.Helper()
				return issueAgentSchemaFor[issueagent.ContextBundle](t, 2)
			},
		},
		{
			name:     "engineer result",
			filename: "engineer-result.schema.json",
			id:       "https://wukongim.github.io/schemas/issue-agent/engineer-result-v2.json",
			version:  2,
			schema: func(t *testing.T) *jsonschema.Schema {
				t.Helper()
				return issueAgentSchemaFor[issueagent.EngineerResult](t, 2)
			},
		},
		{
			name:     "candidate evidence",
			filename: "candidate-evidence.schema.json",
			id:       "https://wukongim.github.io/schemas/issue-agent/candidate-evidence-v2.json",
			version:  2,
			schema: func(t *testing.T) *jsonschema.Schema {
				t.Helper()
				return issueAgentSchemaFor[issueagent.CandidateEvidence](t, 2)
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			schema := test.schema(t)
			schema.Schema = "https://json-schema.org/draft/2020-12/schema"
			schema.ID = test.id
			schema.Title = "WuKongIM Issue Agent " + test.name +
				" v" + strconv.Itoa(test.version)
			generated, err := json.MarshalIndent(schema, "", "  ")
			if err != nil {
				t.Fatalf("marshal %s schema: %v", test.name, err)
			}
			generated = append(generated, '\n')

			path := filepath.Join(repoRoot(t), ".github", "issue-agent", test.filename)
			if os.Getenv("UPDATE_ISSUE_AGENT_SCHEMAS") == "1" {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatalf("create Issue Agent schema directory: %v", err)
				}
				if err := os.WriteFile(path, generated, 0o644); err != nil {
					t.Fatalf("write generated %s schema: %v", test.name, err)
				}
				return
			}
			committed, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read committed %s schema: %v\ngenerated:\n%s", test.name, err, generated)
			}
			if !bytes.Equal(committed, generated) {
				t.Fatalf(
					"%s schema is stale; regenerate from contract types\ncommitted:\n%s\ngenerated:\n%s",
					test.name, committed, generated,
				)
			}
		})
	}
}

func issueAgentSchemaFor[T any](t *testing.T, version int) *jsonschema.Schema {
	t.Helper()
	schema, err := jsonschema.For[T](nil)
	if err != nil {
		t.Fatalf("infer Issue Agent schema: %v", err)
	}
	hardenIssueAgentSchema(schema, version)
	return schema
}

func hardenIssueAgentSchema(schema *jsonschema.Schema, version int) {
	if schema == nil {
		return
	}
	for name, property := range schema.Properties {
		switch name {
		case "schema_version":
			value := any(version)
			property.Const = &value
		case "repository":
			property.Pattern = `^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`
			setMaxLength(property, 256)
		case "issue_number", "sequence":
			setMinimum(property, 1)
		case "state":
			property.Enum = stringValues(
				"triaging", "waiting_for_information",
				"waiting_for_authorization", "engineering",
				"draft", "reviewing", "ready_for_review", "needs_human",
				"completed", "cancelled", "taken_over",
			)
		case "kind":
			property.Enum = stringValues("engineer", "review")
		case "permission":
			property.Enum = stringValues("write", "maintain", "admin")
		case "command":
			property.Enum = stringValues(
				"", "/agent fix", "/agent retry", "/agent cancel",
				"/agent take-over",
			)
		case "risk":
			property.Enum = stringValues("low", "investigation_only", "high")
		case "operation":
			property.Enum = stringValues("upsert", "delete")
		case "mode":
			property.Enum = stringValues("100644", "100755")
		case "outcome":
			property.Enum = stringValues(
				"ready", "needs_human", "already_fixed", "failed",
			)
		case "content_base64":
			property.ContentEncoding = "base64"
		}
		if name == "policy_digest" ||
			name == "prompt_digest" ||
			name == "task_id" ||
			name == "change_set_digest" ||
			name == "stdout_digest" ||
			name == "stderr_digest" ||
			name == "output_schema_digest" ||
			name == "issue_snapshot_digest" ||
			strings.HasSuffix(name, "_sha256") {
			property.Pattern = `^sha256:[0-9a-f]{64}$`
		}
		if (strings.HasSuffix(name, "_sha") &&
			!strings.HasSuffix(name, "_sha256")) ||
			name == "blob_sha" || name == "commit_id" {
			property.Pattern = `^[0-9a-f]{40}$`
		}
		hardenIssueAgentSchema(property, version)
	}
	for _, child := range schema.Defs {
		hardenIssueAgentSchema(child, version)
	}
	hardenIssueAgentSchema(schema.Items, version)
	for _, child := range schema.AnyOf {
		hardenIssueAgentSchema(child, version)
	}
	for _, child := range schema.OneOf {
		hardenIssueAgentSchema(child, version)
	}
	for _, child := range schema.AllOf {
		hardenIssueAgentSchema(child, version)
	}
}

func setMinimum(schema *jsonschema.Schema, value float64) {
	schema.Minimum = &value
}

func setMaximum(schema *jsonschema.Schema, value float64) {
	schema.Maximum = &value
}

func setMaxLength(schema *jsonschema.Schema, value int) {
	schema.MaxLength = &value
}

func stringValues(values ...string) []any {
	result := make([]any, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
}
