package scripts_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCloudLeaseOIDCSetupWorkflowSeparatesBootstrapAndThreeLiveRoles(t *testing.T) {
	path := filepath.Join(repoRoot(t), ".github", "workflows", "cloud-lease-oidc-setup.yml")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, fragment := range []string{
		"if: github.ref == 'refs/heads/main'",
		"environment: cloud-lease-provision",
		"environment: cloud-lease-observe",
		"environment: cloud-lease-release",
		"--expected-role CloudLeaseProvisioner --policy-kind provisioner",
		"--expected-role CloudLeaseObserver --policy-kind observer",
		"--expected-role CloudLeaseReleaser --policy-kind releaser",
		"ALIBABA_CLOUD_ACCESS_KEY_ID and ALIBABA_CLOUD_ACCESS_KEY_SECRET must be configured together",
		"actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0",
		"aliyun/configure-aliyun-credentials-action@1e5248c8d5d93a8781ac344a68e19a43341e79e6",
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("workflow missing %q", fragment)
		}
	}
	if got := strings.Count(text, "id-token: write"); got != 3 {
		t.Fatalf("id-token write count = %d, want exactly three live role jobs", got)
	}
	if got := strings.Count(text, "ACCESS_KEY_ID: ${{ secrets.ALIBABA_CLOUD_ACCESS_KEY_ID }}"); got != 1 {
		t.Fatalf("AccessKey exposure count = %d, want bootstrap step only", got)
	}
	if strings.Contains(text, "environment: cloud-deployment\n    permissions:\n      id-token: write") {
		t.Fatal("deployment Environment unexpectedly receives Alibaba OIDC permission")
	}
}

func TestCloudLeaseEnvironmentTransformRemovesOnlyHumanReviewRequirement(t *testing.T) {
	fixture := map[string]any{
		"protection_rules": []any{
			map[string]any{"type": "wait_timer", "wait_timer": 17},
			map[string]any{"type": "required_reviewers", "prevent_self_review": true, "reviewers": []any{map[string]any{"id": 42}}},
		},
		"deployment_branch_policy": map[string]any{"protected_branches": false, "custom_branch_policies": true},
		"unrelated_response_field": "preserved-by-server-not-request-body",
	}
	input, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	filter := filepath.Join(repoRoot(t), "scripts", "cloud-lease", "environment-without-reviewers.jq")
	command := exec.CommandContext(t.Context(), "jq", "-f", filter)
	command.Stdin = bytes.NewReader(input)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("jq environment transform: %v", err)
	}
	var result struct {
		WaitTimer              int             `json:"wait_timer"`
		PreventSelfReview      bool            `json:"prevent_self_review"`
		Reviewers              []any           `json:"reviewers"`
		DeploymentBranchPolicy map[string]bool `json:"deployment_branch_policy"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatal(err)
	}
	if result.WaitTimer != 17 || result.PreventSelfReview || len(result.Reviewers) != 0 ||
		result.DeploymentBranchPolicy["protected_branches"] || !result.DeploymentBranchPolicy["custom_branch_policies"] {
		t.Fatalf("transformed environment = %s", output)
	}
}

func TestCloudLeaseGitHubIdentityConfiguratorIsExplicitAndPreservesEnvironmentSettings(t *testing.T) {
	path := filepath.Join(repoRoot(t), "scripts", "cloud-lease", "configure-github-identity.sh")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, fragment := range []string{
		"plan|apply",
		"environment-without-reviewers.jq",
		`include_claim_keys:["repo","context","job_workflow_ref"]`,
		"cloud-lease-provision cloud-lease-observe cloud-lease-release cloud-deployment",
		"gh api --method PUT",
		"WK_CHAT_LIFECYCLE_WRAPPING_PUBLIC_KEY",
		"WK_CHAT_LIFECYCLE_WRAPPING_PRIVATE_KEY",
		"gh variable set",
		"gh secret set",
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("GitHub identity configurator missing %q", fragment)
		}
	}
	if strings.Contains(text, "secret delete") || strings.Contains(text, "ALIBABA_CLOUD_ACCESS_KEY") {
		t.Fatal("GitHub configurator may not read or delete Alibaba AccessKey Secrets")
	}
}

func TestCloudLeaseGitHubIdentityPlanTreats404ResponseBodyAsMissingEnvironment(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "gh.log")
	ghPath := filepath.Join(binDir, "gh")
	stub := `#!/bin/sh
set -eu
printf '%s\n' "$*" >>"$GH_STUB_LOG"
case "${1:-}" in
  auth)
    exit 0
    ;;
  api)
    printf '{"message":"Not Found"}\n'
    printf 'failed: HTTP 404: Not Found\n' >&2
    exit 1
    ;;
  variable)
    printf '[]\n'
    exit 0
    ;;
  secret)
    printf 'secret list must not run for a missing Environment\n' >&2
    exit 99
    ;;
esac
printf 'unexpected gh command: %s\n' "$*" >&2
exit 98
`
	if err := os.WriteFile(ghPath, []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), "bash", filepath.Join(root, "scripts", "cloud-lease", "configure-github-identity.sh"), "plan", "WuKongIM/WuKongIM")
	command.Env = append(os.Environ(), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"), "GH_STUB_LOG="+logPath)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("identity plan with missing Environments: %v\n%s", err, output)
	}
	var plan struct {
		Repository string   `json:"repository"`
		Changes    []string `json:"changes"`
	}
	if err := json.Unmarshal(output, &plan); err != nil {
		t.Fatalf("decode plan %q: %v", output, err)
	}
	want := []string{
		"oidc_subject",
		"environment:cloud-lease-provision:create",
		"environment:cloud-lease-observe:create",
		"environment:cloud-lease-release:create",
		"environment:cloud-deployment:create",
		"chat_lifecycle_wrapping_key",
	}
	if plan.Repository != "WuKongIM/WuKongIM" || strings.Join(plan.Changes, "\n") != strings.Join(want, "\n") {
		t.Fatalf("plan = %+v, want repository and changes %+v", plan, want)
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(log), "secret list") {
		t.Fatalf("missing Environment unexpectedly triggered Secret lookup:\n%s", log)
	}
}

func TestCloudLeaseOIDCSetupLiveVerifiesChatLifecycleWrappingKey(t *testing.T) {
	workflow := string(readWorkflow(t, "cloud-lease-oidc-setup.yml"))
	for _, required := range []string{
		"environment: cloud-deployment",
		"vars.WK_CHAT_LIFECYCLE_WRAPPING_PUBLIC_KEY",
		"secrets.WK_CHAT_LIFECYCLE_WRAPPING_PRIVATE_KEY",
		"ssh-keygen -y",
		"expected-normalized.pub",
		"awk 'NR == 1 && NF >= 2",
		"expected_fingerprint=\"$(ssh-keygen -lf \"$RUNNER_TEMP/expected-normalized.pub\" | awk '{print $2}')\"",
		"actual_fingerprint=\"$(ssh-keygen -lf \"$RUNNER_TEMP/actual.pub\" | awk '{print $2}')\"",
		`[[ "$expected_fingerprint" == "$actual_fingerprint" ]]`,
		"wrapping key mismatch (expected fingerprint $expected_fingerprint, actual fingerprint $actual_fingerprint)",
	} {
		if !strings.Contains(workflow, required) {
			t.Fatalf("wrapping-key live verification is missing %q", required)
		}
	}
	if strings.Contains(workflow, `cmp -s "$RUNNER_TEMP/expected-normalized.pub" "$RUNNER_TEMP/actual.pub"`) {
		t.Fatal("wrapping-key verification rejects equivalent public-key text with different comments")
	}
}

func TestCloudLeaseIdentitySetupPublishesOnlyNonSecretVariablesAfterLiveChecks(t *testing.T) {
	path := filepath.Join(repoRoot(t), "scripts", "cloud-lease", "setup-identity.sh")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, fragment := range []string{
		"configure-github-identity.sh\" apply",
		"gh workflow run cloud-lease-oidc-setup.yml",
		"gh run watch",
		"exec \"$0\" --force \"$repository\"",
		"cloud-lease-oidc-output.json",
		"ALIBABA_CLOUD_LEASE_PROVISIONER_ROLE_ARN",
		"ALIBABA_CLOUD_LEASE_OBSERVER_ROLE_ARN",
		"ALIBABA_CLOUD_LEASE_RELEASER_ROLE_ARN",
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("identity setup missing %q", fragment)
		}
	}
	if strings.Contains(text, "secret delete") || strings.Contains(text, "variable delete") {
		t.Fatal("identity setup must leave existing Secrets and unrelated Variables untouched")
	}
}

func TestCloudLeaseLifecycleToolsUseOnlyTheirExactOIDCRoles(t *testing.T) {
	workflowDir := filepath.Join(repoRoot(t), ".github", "workflows")
	read := func(name string) string {
		t.Helper()
		content, err := os.ReadFile(filepath.Join(workflowDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(content)
	}
	provision := read("cloud-lease-provision.yml")
	observe := read("cloud-lease-observe.yml")
	release := read("cloud-lease-release.yml")
	for name, contract := range map[string]struct {
		text        string
		environment string
		role        string
		permissions string
	}{
		"provision": {provision, "environment: cloud-lease-provision", "ALIBABA_CLOUD_LEASE_PROVISIONER_ROLE_ARN", "permissions:\n  contents: read\n  actions: read\n  id-token: write"},
		"observe":   {observe, "environment: cloud-lease-observe", "ALIBABA_CLOUD_LEASE_OBSERVER_ROLE_ARN", "permissions:\n  contents: read\n  id-token: write"},
		"release":   {release, "environment: cloud-lease-release", "ALIBABA_CLOUD_LEASE_RELEASER_ROLE_ARN", "permissions:\n  contents: read\n  id-token: write"},
	} {
		if !strings.Contains(contract.text, contract.environment) || !strings.Contains(contract.text, contract.role) ||
			!strings.Contains(contract.text, contract.permissions) {
			t.Fatalf("%s workflow does not use its exact OIDC boundary", name)
		}
		for _, forbidden := range []string{"ALIBABA_CLOUD_ACCESS_KEY_ID", "ALIBABA_CLOUD_ACCESS_KEY_SECRET", "pull_request:", "push:"} {
			if strings.Contains(contract.text, forbidden) {
				t.Fatalf("%s workflow unexpectedly contains %q", name, forbidden)
			}
		}
		if name != "release" && strings.Contains(contract.text, "schedule:") {
			t.Fatalf("%s workflow unexpectedly contains a schedule", name)
		}
	}
	if !strings.Contains(provision, "paid_authorization=create-paid-cloud-lease") ||
		!strings.Contains(provision, "WK_ALIBABA_LIFECYCLE_MUTATION_AUTHORIZATION: create-and-delete-paid-cloud-lease") ||
		!strings.Contains(provision, `.repository == $repository`) || !strings.Contains(provision, `.request_id == $request_id`) {
		t.Fatal("provision workflow lacks exact paid-mutation gates")
	}
	if strings.Contains(observe, "WK_ALIBABA_LIFECYCLE_MUTATION_AUTHORIZATION: create-and-delete-paid-cloud-lease") ||
		!strings.Contains(observe, "test -z \"${WK_ALIBABA_LIFECYCLE_MUTATION_AUTHORIZATION:-}\"") ||
		!strings.Contains(observe, `.selector.repository == $repository`) {
		t.Fatal("observer workflow has mutation authority")
	}
	if !strings.Contains(release, "release_authorization=release-tagged-cloud-lease") ||
		!strings.Contains(release, "if: always()") || strings.Contains(release, "inputs.repository") ||
		!strings.Contains(release, `cron: "3,18,33,48 * * * *"`) ||
		!strings.Contains(release, `--repository "$GITHUB_REPOSITORY"`) || !strings.Contains(release, `.selector.repository == $repository`) {
		t.Fatal("release workflow lacks exact authority or residual evidence upload")
	}
}
