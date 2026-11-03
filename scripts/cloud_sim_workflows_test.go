package scripts_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
		`gh api --paginate --slurp`,
		`[.[] | .artifacts[]? | select(.expired == false)] | length`,
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
