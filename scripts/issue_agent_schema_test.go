package scripts_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
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
		schema   func(*testing.T) *jsonschema.Schema
	}{
		{
			name:     "checkpoint",
			filename: "checkpoint.schema.json",
			id:       "https://wukongim.github.io/schemas/issue-agent/checkpoint-v1.json",
			schema: func(t *testing.T) *jsonschema.Schema {
				t.Helper()
				return issueAgentSchemaFor[issueagent.CheckpointEnvelope](t)
			},
		},
		{
			name:     "task",
			filename: "task.schema.json",
			id:       "https://wukongim.github.io/schemas/issue-agent/task-v1.json",
			schema: func(t *testing.T) *jsonschema.Schema {
				t.Helper()
				return issueAgentSchemaFor[issueagent.TaskEnvelope](t)
			},
		},
		{
			name:     "result",
			filename: "result.schema.json",
			id:       "https://wukongim.github.io/schemas/issue-agent/result-v1.json",
			schema: func(t *testing.T) *jsonschema.Schema {
				t.Helper()
				return issueAgentSchemaFor[issueagent.AgentResult](t)
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
			schema.Title = "WuKongIM Issue Agent " + test.name + " v1"
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

func issueAgentSchemaFor[T any](t *testing.T) *jsonschema.Schema {
	t.Helper()
	schema, err := jsonschema.For[T](nil)
	if err != nil {
		t.Fatalf("infer Issue Agent schema: %v", err)
	}
	return schema
}
