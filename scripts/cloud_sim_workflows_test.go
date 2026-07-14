package scripts_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCloudSimulationWorkflowsPreferAccessKeyAndRetainOIDCFallback(t *testing.T) {
	tests := []struct {
		workflow string
		jobs     int
	}{
		{workflow: "cloud-sim-provision.yml", jobs: 1},
		{workflow: "cloud-sim-cleanup.yml", jobs: 1},
		{workflow: "cloud-sim-analyze.yml", jobs: 2},
	}

	for _, test := range tests {
		t.Run(test.workflow, func(t *testing.T) {
			path := filepath.Join(repoRoot(t), ".github", "workflows", test.workflow)
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read workflow: %v", err)
			}
			text := string(content)
			for fragment, want := range map[string]int{
				"name: Resolve Alibaba credential mode":                             test.jobs,
				"id: alibaba_auth":                                                  test.jobs,
				"ACCESS_KEY_ID: ${{ secrets.ALIBABA_CLOUD_ACCESS_KEY_ID }}":         test.jobs,
				"ACCESS_KEY_SECRET: ${{ secrets.ALIBABA_CLOUD_ACCESS_KEY_SECRET }}": test.jobs,
				"steps.alibaba_auth.outputs.mode == 'oidc'":                         test.jobs,
				"uses: aliyun/configure-aliyun-credentials-action@":                 test.jobs,
			} {
				if got := strings.Count(text, fragment); got != want {
					t.Fatalf("%s count = %d, want %d in %s", fragment, got, want, path)
				}
			}
			for _, fragment := range []string{
				"mode=access-key",
				"mode=oidc",
				"Both ALIBABA_CLOUD_ACCESS_KEY_ID and ALIBABA_CLOUD_ACCESS_KEY_SECRET must be configured together.",
			} {
				if got := strings.Count(text, fragment); got != test.jobs {
					t.Fatalf("%s count = %d, want %d in %s", fragment, got, test.jobs, path)
				}
			}
		})
	}
}

func TestCloudSimulationWorkflowsPersistAndReuseDiscoveredProviderConfig(t *testing.T) {
	workflowDir := filepath.Join(repoRoot(t), ".github", "workflows")
	read := func(name string) string {
		t.Helper()
		content, err := os.ReadFile(filepath.Join(workflowDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(content)
	}

	provision := read("cloud-sim-provision.yml")
	for _, fragment := range []string{
		"default: cn-hangzhou",
		"description: Optional exact commit reachable from trusted main; empty uses current main",
		"source_sha: ${{ steps.source.outputs.source_sha }}",
		`source_sha="$(git rev-parse origin/main)"`,
		"AUTH_MODE: ${{ steps.alibaba_auth.outputs.mode }}",
		`if [[ "$AUTH_MODE" == "oidc" && -n "$LEGACY_PROVIDER_CONFIG_JSON" ]]`,
		"discover-config --region \"$ALIBABA_REGION\" >provider.json",
		"name: cloud-sim-provider-config-${{ needs.build.outputs.run_id }}",
		"retention-days: 90",
	} {
		if !strings.Contains(provision, fragment) {
			t.Fatalf("provision workflow missing %q", fragment)
		}
	}
	if strings.Contains(provision, "\n      PROVIDER_CONFIG_JSON: ${{ vars.ALIBABA_CLOUD_SIM_CONFIG_JSON }}") {
		t.Fatal("provision workflow still requires the setup-created provider config variable")
	}

	cleanup := read("cloud-sim-cleanup.yml")
	for _, fragment := range []string{
		"actions: read",
		"cloud-sim-provider-config-${RUN_ID}",
		`startswith("cloud-sim-provider-config-")`,
		"provider-configs",
	} {
		if !strings.Contains(cleanup, fragment) {
			t.Fatalf("cleanup workflow missing %q", fragment)
		}
	}

	analyze := read("cloud-sim-analyze.yml")
	if got := strings.Count(analyze, "cloud-sim-provider-config-${RUN_ID}"); got != 2 {
		t.Fatalf("analysis provider config lookup count = %d, want 2", got)
	}
	if strings.Contains(analyze, "\n      PROVIDER_CONFIG_JSON: ${{ vars.ALIBABA_CLOUD_SIM_CONFIG_JSON }}") {
		t.Fatal("analysis workflow still requires the setup-created provider config variable")
	}
}
