package scripts_test

import (
	"bytes"
	"os"
	"os/exec"
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
				"./scripts/cloud-sim/resolve-alibaba-credential-mode.sh":            test.jobs,
				"steps.alibaba_auth.outputs.mode == 'oidc'":                         test.jobs,
				"uses: aliyun/configure-aliyun-credentials-action@":                 test.jobs,
			} {
				if got := strings.Count(text, fragment); got != want {
					t.Fatalf("%s count = %d, want %d in %s", fragment, got, want, path)
				}
			}
		})
	}
}

func TestCloudSimulationDefaultCostCeilingIsSeventyCNY(t *testing.T) {
	root := repoRoot(t)
	workflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "cloud-sim-provision.yml"))
	if err != nil {
		t.Fatalf("read provision workflow: %v", err)
	}
	if !strings.Contains(string(workflow), "max_total_cost:\n        description: Hard maximum total cost in CNY\n        required: true\n        default: \"70\"") {
		t.Fatal("provision workflow default max_total_cost is not 70 CNY")
	}

	setup, err := os.ReadFile(filepath.Join(root, "scripts", "cloud-sim", "setup.sh"))
	if err != nil {
		t.Fatalf("read setup script: %v", err)
	}
	if !strings.Contains(string(setup), "max_total_cost=70") {
		t.Fatal("setup recommendation does not use the 70 CNY default cost ceiling")
	}
}

func TestResolveAlibabaCredentialMode(t *testing.T) {
	script := filepath.Join(repoRoot(t), "scripts", "cloud-sim", "resolve-alibaba-credential-mode.sh")
	tests := []struct {
		name      string
		id        string
		secret    string
		wantMode  string
		wantError bool
	}{
		{name: "access key", id: "test-id", secret: "test-secret", wantMode: "access-key"},
		{name: "OIDC fallback", wantMode: "oidc"},
		{name: "partial pair", id: "test-id", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			environmentPath := filepath.Join(t.TempDir(), "environment")
			outputPath := filepath.Join(t.TempDir(), "output")
			if err := os.WriteFile(environmentPath, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(outputPath, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			command := exec.CommandContext(t.Context(), "bash", script)
			command.Env = append(os.Environ(),
				"ACCESS_KEY_ID="+test.id,
				"ACCESS_KEY_SECRET="+test.secret,
				"GITHUB_ENV="+environmentPath,
				"GITHUB_OUTPUT="+outputPath,
			)
			output, err := command.CombinedOutput()
			if test.wantError {
				if err == nil {
					t.Fatal("partial AccessKey pair was accepted")
				}
				return
			}
			if err != nil {
				t.Fatalf("resolve credential mode: %v\n%s", err, output)
			}
			mode, err := os.ReadFile(outputPath)
			if err != nil || string(mode) != "mode="+test.wantMode+"\n" {
				t.Fatalf("mode output = %q, %v", mode, err)
			}
			environment, err := os.ReadFile(environmentPath)
			if err != nil {
				t.Fatalf("read environment: %v", err)
			}
			if test.wantMode == "access-key" && string(environment) != "ALIBABA_CLOUD_ACCESS_KEY_ID=test-id\nALIBABA_CLOUD_ACCESS_KEY_SECRET=test-secret\n" {
				t.Fatalf("credential environment = %q", environment)
			}
			if test.wantMode == "oidc" && len(environment) != 0 {
				t.Fatalf("OIDC environment = %q, want empty", environment)
			}
		})
	}
}

func TestExactProviderConfigResolverBindsArtifactAndOptionalLocator(t *testing.T) {
	path := filepath.Join(repoRoot(t), "scripts", "cloud-sim", "resolve-exact-provider-config.sh")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, fragment := range []string{
		`IFS=$'\t' read -r artifact_id artifact_region artifact_account_id_hash`,
		`test "$(jq -er .region "$destination")" = "$artifact_region"`,
		`test "$(jq -er .account_id_hash "$destination")" = "$artifact_account_id_hash"`,
		`locator_name="cloud-sim-locator-${run_id}"`,
		`test "$(jq -er .run_id "$temporary/locator/run-locator.json")" = "$run_id"`,
		`case "$locator_count" in`,
		`0)`,
		`1)`,
		`test "$(jq -er .region "$destination")" = "$expected_region"`,
		`test "$(jq -er .account_id_hash "$destination")" = "$expected_account_id_hash"`,
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("provider resolver missing %q", fragment)
		}
	}
}

func TestSelectProviderConfigArtifactsBoundsDownloadsByAccountAndRegion(t *testing.T) {
	hashA := strings.Repeat("a", 64)
	hashB := strings.Repeat("b", 64)
	input := strings.Join([]string{
		"101\tcloud-sim-provider-config--gh-101-1--cn-hangzhou--" + hashA,
		"100\tcloud-sim-provider-config--gh-100-1--cn-hangzhou--" + hashA,
		"99\tcloud-sim-provider-config--gh-99-1--cn-shanghai--" + hashA,
		"98\tcloud-sim-provider-config--gh-98-1--cn-hangzhou--" + hashB,
		"97\tcloud-sim-provider-config-gh-legacy-1",
		"96\tcloud-sim-provider-config-gh-legacy-0",
		"95\tunrelated-artifact",
	}, "\n") + "\n"

	selector := filepath.Join(repoRoot(t), "scripts", "cloud-sim", "select-provider-config-artifacts.sh")
	command := exec.CommandContext(t.Context(), "bash", selector)
	command.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("select provider configs: %v\n%s", err, stderr.String())
	}
	if got, want := stdout.String(), "101\n99\n98\n"; got != want {
		t.Fatalf("selected artifact IDs = %q, want %q", got, want)
	}
}

func TestSelectExactProviderConfigArtifactAcceptsBindingAwareName(t *testing.T) {
	hash := strings.Repeat("a", 64)
	input := "101\tcloud-sim-provider-config--gh-101-1--cn-hangzhou--" + hash + "\n" +
		"100\tcloud-sim-provider-config--gh-100-1--cn-hangzhou--" + hash + "\n"
	selector := filepath.Join(repoRoot(t), "scripts", "cloud-sim", "select-exact-provider-config-artifact.sh")
	command := exec.CommandContext(t.Context(), "bash", selector, "gh-101-1")
	command.Stdin = strings.NewReader(input)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("select exact provider config: %v", err)
	}
	boundSelection := "101\tcn-hangzhou\tsha256:" + hash + "\n"
	if got, want := string(output), boundSelection; got != want {
		t.Fatalf("selected artifact binding = %q, want %q", got, want)
	}

	command = exec.CommandContext(t.Context(), "bash", selector, "gh-101-1")
	command.Stdin = strings.NewReader(input + "99\tcloud-sim-provider-config-gh-101-1\n")
	output, err = command.Output()
	if err != nil || string(output) != boundSelection {
		t.Fatalf("unbound artifact affected exact selection: output=%q err=%v", output, err)
	}

	command = exec.CommandContext(t.Context(), "bash", selector, "gh-legacy-1")
	command.Stdin = strings.NewReader("99\tcloud-sim-provider-config-gh-legacy-1\n")
	if err := command.Run(); err == nil {
		t.Fatal("unbound provider config artifact was accepted")
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
		"ref: ${{ github.sha }}",
		"go build -trimpath -o wkcloudsim ./cmd/wkcloudsim",
		"discover-config --region \"$ALIBABA_REGION\" >provider.json",
		"./scripts/cloud-sim/write-ssh-config.sh",
		"source ./scripts/cloud-sim/ssh-retry.sh",
		`echo "bootstrap_deadline_epoch=$((until_epoch - 180))" >>"$GITHUB_OUTPUT"`,
		"WK_CLOUD_SSH_DEADLINE_EPOCH: ${{ steps.ingress.outputs.bootstrap_deadline_epoch }}",
		"ssh_opts=(-F cloud-sim-ssh-config)",
		"cloud_ssh_retry sim-ready 30 5 ssh",
		"cloud_ssh_retry sim-upload 3 5 scp",
		`cloud_ssh_retry "${role}-ready" 12 5 ssh`,
		`cloud_ssh_retry "${role}-upload" 3 5 scp`,
		`cloud_ssh_retry_capture "${role}-data-device" 3 5 ssh`,
		`cloud_ssh_retry "${role}-install" 3 5 ssh`,
		"cloud_ssh_retry_capture sim-data-device 3 5 ssh",
		"cloud_ssh_retry sim-install 3 5 ssh",
		"id: preserve_failed_provisioning",
		"released=true",
		`trap 'printf "released=%s\n" "$released" >>"$GITHUB_OUTPUT"' EXIT`,
		"steps.preserve_failed_provisioning.outputs.released != 'true'",
		"steps.preserve_failed_provisioning.outputs.released != 'true' && steps.create.outputs.sim_public != ''",
		"ssh_opts=(-F cloud-sim-ssh-config -o ConnectTimeout=5)",
		"name: cloud-sim-provider-config--${{ needs.build.outputs.run_id }}--${{ inputs.region }}--${{ steps.provider_config.outputs.account_hash_hex }}",
		"retention-days: 90",
	} {
		if !strings.Contains(provision, fragment) {
			t.Fatalf("provision workflow missing %q", fragment)
		}
	}
	if strings.Contains(provision, "\n      PROVIDER_CONFIG_JSON: ${{ vars.ALIBABA_CLOUD_SIM_CONFIG_JSON }}") {
		t.Fatal("provision workflow still requires the setup-created provider config variable")
	}
	if strings.Contains(provision, "bundle/bin/wkcloudsim --provider") {
		t.Fatal("provision workflow uses the selected deployment revision as its cloud control plane")
	}
	if strings.Contains(provision, `ProxyJump=wukong@${SIM_PUBLIC}`) {
		t.Fatal("provision workflow uses a ProxyJump whose connection does not pin the bootstrap identity")
	}
	transferStart := strings.Index(provision, "      - name: Transfer and install the same verified bundle on all hosts")
	transferEnd := strings.Index(provision, "      - name: Prove Bootstrap Gate")
	if transferStart < 0 || transferEnd <= transferStart {
		t.Fatal("provision workflow does not retain the bounded transfer step")
	}
	transferStep := provision[transferStart:transferEnd]
	if !strings.Contains(transferStep, "WK_CLOUD_SSH_DEADLINE_EPOCH: ${{ steps.ingress.outputs.bootstrap_deadline_epoch }}") {
		t.Fatal("provision transfer step does not receive the bounded SSH deadline")
	}

	cleanup := read("cloud-sim-cleanup.yml")
	for _, fragment := range []string{
		"actions: read",
		"resolve-exact-provider-config.sh",
		"select-provider-config-artifacts.sh",
		"provider-configs",
	} {
		if !strings.Contains(cleanup, fragment) {
			t.Fatalf("cleanup workflow missing %q", fragment)
		}
	}

	analyze := read("cloud-sim-analyze.yml")
	if got := strings.Count(analyze, "resolve-exact-provider-config.sh"); got != 2 {
		t.Fatalf("analysis provider config selector count = %d, want 2", got)
	}
	if got := strings.Count(analyze, "timeout 90s ./wkcloudsim --provider alibaba --provider-config provider.json open-analysis"); got != 2 {
		t.Fatalf("bounded analysis ingress count = %d, want 2", got)
	}
	if got := strings.Count(analyze, "timeout 90s ./wkcloudsim --provider alibaba --provider-config provider.json close-analysis"); got != 3 {
		t.Fatalf("bounded analysis ingress cleanup count = %d, want 3", got)
	}
	if !strings.Contains(analyze, `curl --fail --silent --show-error --connect-timeout 5 --max-time 10 --proto '=https' --tlsv1.2 https://api.ipify.org`) {
		t.Fatal("analysis runner public-IP discovery is not bounded")
	}
	if strings.Contains(analyze, "\n      PROVIDER_CONFIG_JSON: ${{ vars.ALIBABA_CLOUD_SIM_CONFIG_JSON }}") {
		t.Fatal("analysis workflow still requires the setup-created provider config variable")
	}
}

func TestCloudSimulationBootstrapGateReadsTLSAsServiceUser(t *testing.T) {
	path := filepath.Join(repoRoot(t), "scripts", "cloud-sim", "collect-bootstrap-gate.sh")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	if !strings.Contains(text, `ssh_sim "sudo -u wukongim curl`) {
		t.Fatal("Bootstrap Gate TLS self-check does not read the service-owned CA as wukongim")
	}
	if strings.Contains(text, `ssh_sim "curl --fail --silent --show-error --max-time 10 --resolve`) {
		t.Fatal("Bootstrap Gate TLS self-check reads the private service CA as the SSH deployment user")
	}
}
