package reviewagentverify_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
	verify "github.com/WuKongIM/WuKongIM/internal/runtime/reviewagentverify"
)

func TestPolicySelectsMandatoryChecksFromCompletePaths(t *testing.T) {
	t.Parallel()

	policy := testVerificationPolicy()
	tests := []struct {
		name  string
		files []contract.ChangedFile
		risk  verify.RiskSelection
		want  []string
	}{
		{
			name: "go",
			files: []contract.ChangedFile{
				changed("internal/runtime/delivery/queue.go"),
			},
			want: []string{"go-unit", "go-vet"},
		},
		{
			name: "workflow and codeowners",
			files: []contract.ChangedFile{
				changed(".github/workflows/ci.yml"),
				changed(".github/CODEOWNERS"),
			},
			want: []string{"go-unit", "go-vet", "workflow-contracts"},
		},
		{
			name: "review agent javascript",
			files: []contract.ChangedFile{
				changed(".github/review-agent/responses-budget-proxy.mjs"),
			},
			want: []string{
				"go-unit", "go-vet", "review-proxy-contracts",
				"workflow-contracts",
			},
		},
		{
			name: "config and docker",
			files: []contract.ChangedFile{
				changed("wukongim.toml.example"),
				changed("docker/cluster/docker-compose.yml"),
			},
			want: []string{"go-unit", "go-vet", "workflow-contracts"},
		},
		{
			name: "production script",
			files: []contract.ChangedFile{
				changed("scripts/deploy.sh"),
			},
			want: []string{
				"go-unit", "go-vet", "scripts-integration",
			},
		},
		{
			name: "manager web",
			files: []contract.ChangedFile{
				changed("web/src/App.tsx"),
			},
			want: []string{
				"go-unit", "go-vet", "web-build", "web-bundle",
				"web-lint", "web-test", "web-typecheck",
			},
		},
		{
			name: "chat demo",
			files: []contract.ChangedFile{
				changed("demo/chatdemo/src/App.vue"),
			},
			want: []string{
				"demo-build", "demo-bundle", "demo-test", "go-unit",
				"go-vet",
			},
		},
		{
			name: "exclusive docs",
			files: []contract.ChangedFile{
				changed("docs/agents/review-agent.md"),
				changed("docs-site/content/docs/index.mdx"),
			},
			want: []string{"docs-contracts"},
		},
		{
			name: "all docs-site source stays on the exclusive docs fast path",
			files: []contract.ChangedFile{
				changed("docs-site/lib/i18n.ts"),
			},
			want: []string{"docs-contracts"},
		},
		{
			name: "JavaScript quickstart docs add the focused integration gate",
			files: []contract.ChangedFile{
				changed("docs-site/examples/javascript-web-quickstart/src/client/main.ts"),
			},
			want: []string{"docs-contracts", "docs-integration"},
		},
		{
			name: "golden path server contract adds focused integration to Go checks",
			files: []contract.ChangedFile{
				changed("internal/access/api/channel_messagesync.go"),
			},
			want: []string{"docs-integration", "go-unit", "go-vet"},
		},
		{
			name: "golden path runtime dependency adds focused integration to Go checks",
			files: []contract.ChangedFile{
				changed("internal/usecase/message/sync.go"),
			},
			want: []string{"docs-integration", "go-unit", "go-vet"},
		},
		{
			name: "golden path user metadata dependency adds focused integration",
			files: []contract.ChangedFile{
				changed("internal/infra/cluster/user_metadata.go"),
				changed("pkg/cluster/node.go"),
				changed("pkg/cluster/node_meta.go"),
			},
			want: []string{"docs-integration", "go-unit", "go-vet"},
		},
		{
			name: "golden path channel routing dependency adds focused integration",
			files: []contract.ChangedFile{
				changed("pkg/cluster/channels/channel.go"),
			},
			want: []string{"docs-integration", "go-unit", "go-vet"},
		},
		{
			name: "FLOW and governing root rule",
			files: []contract.ChangedFile{
				changed("pkg/channel/FLOW.md"),
				changed("AGENTS.md"),
			},
			want: []string{"flow-doc-contracts", "go-unit", "go-vet"},
		},
		{
			name: "generated FLOW knowledge stays on the exclusive docs path",
			files: []contract.ChangedFile{
				changed("docs/development/FLOW_INDEX.md"),
				changed("docs/development/PROJECT_KNOWLEDGE.md"),
			},
			want: []string{"docs-contracts", "flow-doc-contracts"},
		},
		{
			name: "FLOW check remains additive on a mixed docs change",
			files: []contract.ChangedFile{
				changed("docs/example/FLOW.md"),
				changed("docs/example/guide.md"),
			},
			want: []string{"docs-contracts", "flow-doc-contracts"},
		},
		{
			name: "all Agent context discovery boundaries",
			files: []contract.ChangedFile{
				changed("internal/infra/issueagentgithub/instructions.go"),
				changed("internal/infra/reviewagentgithub/reader.go"),
				changed(".github/issue-agent/prompts/engineer.md"),
			},
			want: []string{
				"flow-doc-contracts", "go-unit", "go-vet", "workflow-contracts",
			},
		},
		{
			name: "rename from production into docs evaluates both names",
			files: []contract.ChangedFile{{
				Path:         "docs/queue.md",
				PreviousPath: "internal/runtime/delivery/queue.go",
				Status:       contract.FileStatusRenamed,
			}},
			want: []string{"go-unit", "go-vet"},
		},
		{
			name: "risk-selected tiers",
			files: []contract.ChangedFile{
				changed("internal/runtime/delivery/queue.go"),
			},
			risk: verify.RiskSelection{
				Race:             true,
				Integration:      true,
				E2E:              true,
				ThreeNodeCluster: true,
			},
			want: []string{
				"go-e2e", "go-integration", "go-race", "go-unit",
				"go-vet", "three-node-cluster",
			},
		},
		{
			name: "root documentation stays on the exclusive fast path",
			files: []contract.ChangedFile{
				changed("README.md"),
				changed("README_CN.md"),
			},
			want: []string{"docs-contracts"},
		},
		{
			name:  "unclassified repository path gets safe default",
			files: []contract.ChangedFile{changed("LICENSE")},
			want:  []string{"go-unit", "go-vet"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := verify.PlanChecks(
				verify.Inventory{
					Complete:      true,
					DeclaredFiles: len(test.files),
					Files:         test.files,
				},
				policy,
				test.risk,
			)
			require.NoError(t, err)
			require.Equal(t, test.want, got)
		})
	}
}

func TestPolicyRejectsIncompleteInventory(t *testing.T) {
	t.Parallel()

	_, err := verify.PlanChecks(
		verify.Inventory{
			Complete:      false,
			DeclaredFiles: 2,
			Files:         []contract.ChangedFile{changed("README.md")},
		},
		testVerificationPolicy(),
		verify.RiskSelection{},
	)
	require.EqualError(t, err, "changed-file inventory is incomplete")
}

func testVerificationPolicy() verify.Policy {
	return verify.Policy{
		MaxChangedFiles: 5000,
		TrustedChecks: map[string]verify.CheckPlan{
			"go-unit": {}, "go-vet": {}, "scripts-integration": {},
			"workflow-contracts": {}, "docs-contracts": {},
			"docs-integration":       {},
			"flow-doc-contracts":     {},
			"review-proxy-contracts": {},
			"web-lint":               {}, "web-test": {}, "web-typecheck": {},
			"web-build": {}, "web-bundle": {}, "demo-test": {},
			"demo-build": {}, "demo-bundle": {}, "go-race": {},
			"go-integration": {}, "go-e2e": {}, "three-node-cluster": {},
		},
		PathRules: []verify.PathRule{
			{
				Name: "documentation golden path integration",
				Paths: []string{
					"internal/access/api/channel_messagesync.go",
					"internal/infra/cluster/user_metadata.go",
					"pkg/cluster/node.go",
					"pkg/cluster/node_meta.go",
				},
				Prefixes: []string{
					"docs-site/examples/javascript-web-quickstart/",
					"internal/usecase/message/",
					"pkg/cluster/channels/",
				},
				Checks: []string{"docs-integration"},
				Always: true,
			},
			{
				Name: "flow documents",
				Paths: []string{
					"AGENTS.md",
					".github/issue-agent/prompts/engineer.md",
					".github/issue-agent/prompts/review.md",
					"docs/development/FLOW_INDEX.md",
					"docs/development/PROJECT_KNOWLEDGE.md",
					"internal/infra/issueagentgithub/instructions.go",
					"internal/infra/issueagentgithub/instructions_test.go",
					"internal/infra/reviewagentgithub/reader.go",
					"internal/infra/reviewagentgithub/reader_budget_test.go",
					"internal/infra/reviewagentgithub/reader_test.go",
					"internal/runtime/reviewagentverify/instructions.go",
					"internal/runtime/reviewagentverify/context_test.go",
				},
				Prefixes: []string{""},
				Suffixes: []string{"FLOW.md"},
				Checks:   []string{"flow-doc-contracts"},
				Always:   true,
			},
			{
				Name:     "flow tooling",
				Prefixes: []string{"pkg/flowdoc/", "scripts/flowcheck/"},
				Checks:   []string{"flow-doc-contracts"},
				Always:   true,
			},
			{
				Name:     "repository default",
				Prefixes: []string{""},
				Checks:   []string{"go-unit", "go-vet"},
			},
			{
				Name: "go",
				Prefixes: []string{
					"cmd/", "internal/", "pkg/", "scripts/", "docker/",
				},
				Suffixes: []string{".go", "go.mod", "go.sum"},
				Checks:   []string{"go-unit", "go-vet"},
			},
			{
				Name:     "workflow",
				Prefixes: []string{".github/"},
				Checks:   []string{"go-unit", "workflow-contracts"},
			},
			{
				Name:     "review agent javascript",
				Prefixes: []string{".github/review-agent/"},
				Suffixes: []string{".mjs"},
				Checks: []string{
					"go-unit", "review-proxy-contracts", "workflow-contracts",
				},
			},
			{
				Name:   "codeowners",
				Paths:  []string{".github/CODEOWNERS"},
				Checks: []string{"go-unit", "workflow-contracts"},
			},
			{
				Name:     "configuration",
				Paths:    []string{"wukongim.toml.example"},
				Suffixes: []string{".toml"},
				Checks:   []string{"go-unit", "go-vet"},
			},
			{
				Name:     "docker",
				Prefixes: []string{"docker/"},
				Checks:   []string{"go-unit", "go-vet", "workflow-contracts"},
			},
			{
				Name:     "production scripts",
				Prefixes: []string{"scripts/"},
				Suffixes: []string{".sh"},
				Checks: []string{
					"go-unit", "go-vet", "scripts-integration",
				},
			},
			{
				Name:     "web",
				Prefixes: []string{"web/"},
				Checks: []string{
					"web-lint", "web-test", "web-typecheck", "web-build",
					"web-bundle",
				},
			},
			{
				Name:     "demo",
				Prefixes: []string{"demo/chatdemo/"},
				Checks: []string{
					"demo-test", "demo-build", "demo-bundle",
				},
			},
			{
				Name:      "documentation-only",
				Paths:     []string{"README.md", "README_CN.md"},
				Prefixes:  []string{"docs/", "docs-site/"},
				Suffixes:  []string{},
				Checks:    []string{"docs-contracts"},
				Exclusive: true,
			},
		},
	}
}

func changed(path string) contract.ChangedFile {
	return contract.ChangedFile{
		Path:   path,
		Status: contract.FileStatusModified,
	}
}
