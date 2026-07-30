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
			want: []string{"go-unit", "workflow-contracts"},
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
				"web-build", "web-bundle", "web-lint", "web-test",
				"web-typecheck",
			},
		},
		{
			name: "chat demo",
			files: []contract.ChangedFile{
				changed("demo/chatdemo/src/App.vue"),
			},
			want: []string{"demo-build", "demo-bundle", "demo-test"},
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
			"web-lint": {}, "web-test": {}, "web-typecheck": {},
			"web-build": {}, "web-bundle": {}, "demo-test": {},
			"demo-build": {}, "demo-bundle": {}, "go-race": {},
			"go-integration": {}, "go-e2e": {}, "three-node-cluster": {},
		},
		PathRules: []verify.PathRule{
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
				Prefixes:  []string{"docs/", "docs-site/"},
				Suffixes:  []string{".md", ".mdx"},
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
