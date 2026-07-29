package scripts_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
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
	hardenIssueAgentSchema(schema)
	return schema
}

func hardenIssueAgentSchema(schema *jsonschema.Schema) {
	if schema == nil {
		return
	}
	for name, property := range schema.Properties {
		switch name {
		case "schema_version":
			value := any(1)
			property.Const = &value
		case "repository":
			property.Pattern = `^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`
			setMaxLength(property, 256)
		case "issue_number", "generation", "sequence",
			"expected_previous_checkpoint_id", "run_id", "artifact_run_id",
			"request_run_id", "evidence_run_id",
			"gate_generation", "reserved_seconds":
			setMinimum(property, 1)
		case "pr_number":
			setMinimum(property, 0)
		case "mechanical_rebase_attempts":
			setMinimum(property, 0)
			setMaximum(property, 1)
		case "state", "requested_state":
			property.Enum = stringValues(
				"awaiting_triage", "needs_info", "authorized", "version_pinned",
				"reproducing", "already_fixed", "reproduced", "draft_pr_open",
				"diagnosing", "diagnosed", "fixing", "validating",
				"ready_for_review", "ready_for_human", "merged", "cancelled",
				"superseded", "wontfix",
			)
		case "next_action", "requested_action":
			property.Enum = stringValues(
				"none", "pin_versions", "reproduce", "open_draft_pr",
				"diagnose", "implement_fix", "validate", "request_review",
				"wait_for_human", "reconcile", "create_backport",
			)
		case "phase":
			property.Enum = stringValues(
				"reproduce", "diagnose", "fix", "address_review",
			)
		case "provider":
			property.Enum = stringValues("codex", "deepseek")
		case "status":
			property.Enum = stringValues("success", "failed")
		case "operation":
			property.Enum = stringValues("upsert", "delete")
		case "mode":
			property.Enum = stringValues("100644", "100755")
		case "class":
			property.Enum = stringValues(
				"needs_info", "already_fixed", "product_assertion",
				"test_harness", "worker_infrastructure", "provider",
				"unsafe_scope", "state_conflict", "budget_exhausted",
				"cancelled",
			)
		case "topology":
			property.Enum = stringValues(
				"single-node-cluster", "three-node-cluster",
				"multi-node-cluster",
			)
		case "outcome":
			property.Enum = stringValues("assertion_failed", "passed")
		case "content_base64":
			property.ContentEncoding = "base64"
		}
		if name == "operation_id" ||
			name == "checkpoint_digest" ||
			name == "policy_digest" ||
			name == "prompt_digest" ||
			name == "previous_checkpoint_sha256" ||
			strings.HasSuffix(name, "_sha256") {
			property.Pattern = `^sha256:[0-9a-f]{64}$`
		}
		if (strings.HasSuffix(name, "_sha") &&
			!strings.HasSuffix(name, "_sha256")) ||
			name == "blob_sha" || name == "commit_id" {
			property.Pattern = `^[0-9a-f]{40}$`
		}
		hardenIssueAgentSchema(property)
	}
	for _, child := range schema.Defs {
		hardenIssueAgentSchema(child)
	}
	hardenIssueAgentSchema(schema.Items)
	for _, child := range schema.AnyOf {
		hardenIssueAgentSchema(child)
	}
	for _, child := range schema.OneOf {
		hardenIssueAgentSchema(child)
	}
	for _, child := range schema.AllOf {
		hardenIssueAgentSchema(child)
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
