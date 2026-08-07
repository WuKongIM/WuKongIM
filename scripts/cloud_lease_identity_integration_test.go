//go:build integration

package scripts_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCloudLeaseGitHubIdentityApplyCreatesAndVerifiesFirstSetup(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	stateDir := t.TempDir()
	logPath := filepath.Join(stateDir, "gh.log")
	ghPath := filepath.Join(binDir, "gh")
	writeCloudLeaseIdentityGHStub(t, ghPath)
	command := exec.CommandContext(t.Context(), "bash", filepath.Join(root, "scripts", "cloud-lease", "configure-github-identity.sh"), "apply", "WuKongIM/WuKongIM")
	command.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GH_STUB_LOG="+logPath,
		"GH_STUB_STATE="+stateDir,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("first identity apply: %v\n%s", err, output)
	}
	var result struct {
		Repository             string   `json:"repository"`
		OIDCSubject            []string `json:"oidc_subject"`
		Environments           []string `json:"environments"`
		WrappingPublicVariable string   `json:"wrapping_public_variable"`
		WrappingPrivateSecret  string   `json:"wrapping_private_secret"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("decode apply result %q: %v", output, err)
	}
	if result.Repository != "WuKongIM/WuKongIM" || len(result.OIDCSubject) != 3 || len(result.Environments) != 4 ||
		result.WrappingPublicVariable != "WK_CHAT_LIFECYCLE_WRAPPING_PUBLIC_KEY" ||
		result.WrappingPrivateSecret != "WK_CHAT_LIFECYCLE_WRAPPING_PRIVATE_KEY" {
		t.Fatalf("apply result = %+v", result)
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(log), "api --method PUT "); got != 5 {
		t.Fatalf("identity apply PUT count = %d, want OIDC plus four Environments:\n%s", got, log)
	}
	if got := strings.Count(string(log), "variable set WK_CHAT_LIFECYCLE_WRAPPING_PUBLIC_KEY"); got != 1 {
		t.Fatalf("wrapping public-key write count = %d:\n%s", got, log)
	}
	if got := strings.Count(string(log), "secret set WK_CHAT_LIFECYCLE_WRAPPING_PRIVATE_KEY"); got != 1 {
		t.Fatalf("wrapping private-key write count = %d:\n%s", got, log)
	}
	if strings.Contains(string(log), "workflow run") || strings.Contains(string(log), "paid_authorization") {
		t.Fatalf("identity apply escaped into workflow or paid mutation:\n%s", log)
	}
	planCommand := exec.CommandContext(t.Context(), "bash", filepath.Join(root, "scripts", "cloud-lease", "configure-github-identity.sh"), "plan", "WuKongIM/WuKongIM")
	planCommand.Env = command.Env
	planOutput, err := planCommand.CombinedOutput()
	if err != nil {
		t.Fatalf("idempotent identity plan: %v\n%s", err, planOutput)
	}
	var plan struct {
		Changes []string `json:"changes"`
	}
	if err := json.Unmarshal(planOutput, &plan); err != nil || len(plan.Changes) != 0 {
		t.Fatalf("idempotent identity plan = %q / %+v / %v, want no changes", planOutput, plan, err)
	}
}

func TestCloudLeaseIdentitySetupRunsFirstBootstrapEndToEnd(t *testing.T) {
	output, log := runCloudLeaseIdentitySetup(t)
	var result struct {
		Repository    string `json:"repository"`
		SetupID       string `json:"setup_id"`
		WorkflowRunID int    `json:"workflow_run_id"`
		Status        string `json:"status"`
	}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("decode identity setup result %q: %v", output, err)
	}
	if result.Repository != "WuKongIM/WuKongIM" || result.SetupID == "" || result.WorkflowRunID != 4242 || result.Status != "verified" {
		t.Fatalf("identity setup result = %+v", result)
	}
	if got := strings.Count(log, "workflow run cloud-lease-oidc-setup.yml"); got != 1 {
		t.Fatalf("OIDC setup dispatch count = %d:\n%s", got, log)
	}
	for _, required := range []string{
		"--ref main",
		"force_reconcile=false",
		"run watch 4242",
		"run download 4242",
		"variable set ALIBABA_CLOUD_LEASE_REGION",
		"variable set ALIBABA_CLOUD_LEASE_ACCOUNT_ID_HASH",
		"variable set ALIBABA_CLOUD_LEASE_OIDC_PROVIDER_ARN",
		"variable set ALIBABA_CLOUD_LEASE_OIDC_AUDIENCE",
		"variable set ALIBABA_CLOUD_LEASE_PROVISIONER_ROLE_ARN",
		"variable set ALIBABA_CLOUD_LEASE_OBSERVER_ROLE_ARN",
		"variable set ALIBABA_CLOUD_LEASE_RELEASER_ROLE_ARN",
	} {
		if !strings.Contains(log, required) {
			t.Fatalf("first identity setup missing %q:\n%s", required, log)
		}
	}
	if strings.Contains(log, "cloud-lease-provision.yml") || strings.Contains(log, "paid_authorization") {
		t.Fatalf("identity setup escaped into paid workflow:\n%s", log)
	}
}

func TestCloudLeaseIdentitySetupForcedRetryUsesDistinctCorrelationID(t *testing.T) {
	output, log := runCloudLeaseIdentitySetup(t, "GH_STUB_FAIL_FIRST_WATCH=true")
	if !strings.Contains(output, "retrying once") ||
		(!strings.Contains(output, `"status":"verified"`) && !strings.Contains(output, `"status": "verified"`)) {
		t.Fatalf("forced setup retry did not reach verified result:\n%s", output)
	}
	var setupIDs []string
	for _, line := range strings.Split(log, "\n") {
		if !strings.HasPrefix(line, "workflow run cloud-lease-oidc-setup.yml ") {
			continue
		}
		for _, field := range strings.Fields(line) {
			if strings.HasPrefix(field, "setup_id=") {
				setupIDs = append(setupIDs, strings.TrimPrefix(field, "setup_id="))
			}
		}
	}
	if len(setupIDs) != 2 || setupIDs[0] == setupIDs[1] {
		t.Fatalf("setup retry correlation IDs = %v, want two distinct IDs:\n%s", setupIDs, log)
	}
	if !strings.Contains(log, "force_reconcile=false") || !strings.Contains(log, "force_reconcile=true") ||
		strings.Count(log, "run watch 4242") != 2 || strings.Count(log, "run download 4242") != 1 {
		t.Fatalf("setup retry sequence is incomplete:\n%s", log)
	}
	if got := strings.Count(log, "api --method PUT "); got != 5 {
		t.Fatalf("forced retry repeated first-time GitHub writes: PUT count = %d\n%s", got, log)
	}
	if strings.Count(log, "variable set WK_CHAT_LIFECYCLE_WRAPPING_PUBLIC_KEY") != 1 ||
		strings.Count(log, "secret set WK_CHAT_LIFECYCLE_WRAPPING_PRIVATE_KEY") != 1 {
		t.Fatalf("forced retry rotated the wrapping identity:\n%s", log)
	}
}

func runCloudLeaseIdentitySetup(t *testing.T, extraEnv ...string) (string, string) {
	t.Helper()
	root := repoRoot(t)
	binDir := t.TempDir()
	stateDir := t.TempDir()
	logPath := filepath.Join(stateDir, "gh.log")
	writeCloudLeaseIdentityGHStub(t, filepath.Join(binDir, "gh"))
	command := exec.CommandContext(t.Context(), "bash", filepath.Join(root, "scripts", "cloud-lease", "setup-identity.sh"), "WuKongIM/WuKongIM")
	command.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GH_STUB_LOG="+logPath,
		"GH_STUB_STATE="+stateDir,
	)
	command.Env = append(command.Env, extraEnv...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("first identity setup: %v\n%s", err, output)
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	return string(output), string(log)
}

func writeCloudLeaseIdentityGHStub(t *testing.T, path string) {
	t.Helper()
	stub := `#!/bin/sh
set -eu
printf '%s\n' "$*" >>"$GH_STUB_LOG"
state_key() {
  case "$1" in
    */actions/oidc/customization/sub) printf 'oidc\n' ;;
    */environments/*) printf 'environment-%s\n' "${1##*/}" ;;
    *) printf 'unexpected endpoint: %s\n' "$1" >&2; exit 97 ;;
  esac
}
case "${1:-}" in
  auth)
    exit 0
    ;;
  api)
    if [ "${2:-}" = --method ]; then
      [ "${3:-}" = PUT ] || exit 96
      endpoint="${4:-}"
      key="$(state_key "$endpoint")"
      : >"$GH_STUB_STATE/$key"
      printf '{}\n'
      exit 0
    fi
    endpoint="${2:-}"
    key="$(state_key "$endpoint")"
    if [ ! -f "$GH_STUB_STATE/$key" ]; then
      printf '{"message":"Not Found"}\n'
      printf 'failed: HTTP 404: Not Found\n' >&2
      exit 1
    fi
    case "$key" in
      oidc)
        printf '{"use_default":false,"use_immutable_subject":false,"include_claim_keys":["repo","context","job_workflow_ref"]}\n'
        ;;
      environment-*)
        printf '{"protection_rules":[]}\n'
        ;;
    esac
    exit 0
    ;;
  variable)
    case "${2:-}" in
      list)
        printf '['
        separator=
        for variable_file in "$GH_STUB_STATE"/variable-*; do
          [ -f "$variable_file" ] || continue
          name="${variable_file##*/variable-}"
          value="$(cat "$variable_file")"
          printf '%s{"name":"%s","value":"%s"}' "$separator" "$name" "$value"
          separator=,
        done
        printf ']\n'
        ;;
      set)
        name="${3:-}"
        shift 3
        value=
        while [ "$#" -gt 0 ]; do
          if [ "$1" = --body ]; then
            value="${2:-}"
            break
          fi
          shift
        done
        [ -n "$name" ] && [ -n "$value" ] || exit 95
        printf '%s\n' "$value" >"$GH_STUB_STATE/variable-$name"
        ;;
      *) exit 94 ;;
    esac
    exit 0
    ;;
  secret)
    case "${2:-}" in
      list)
        if [ -f "$GH_STUB_STATE/secret-WK_CHAT_LIFECYCLE_WRAPPING_PRIVATE_KEY" ]; then
          printf '[{"name":"WK_CHAT_LIFECYCLE_WRAPPING_PRIVATE_KEY"}]\n'
        else
          printf '[]\n'
        fi
        ;;
      set)
        [ "${3:-}" = WK_CHAT_LIFECYCLE_WRAPPING_PRIVATE_KEY ] || exit 93
        : >"$GH_STUB_STATE/secret-WK_CHAT_LIFECYCLE_WRAPPING_PRIVATE_KEY"
        ;;
      *) exit 92 ;;
    esac
    exit 0
    ;;
  workflow)
    [ "${2:-}" = run ] && [ "${3:-}" = cloud-lease-oidc-setup.yml ] || exit 90
    exit 0
    ;;
  run)
    case "${2:-}" in
      list)
        printf '4242\n'
        ;;
      watch)
        if [ "${GH_STUB_FAIL_FIRST_WATCH:-false}" = true ] && [ ! -f "$GH_STUB_STATE/watch-failed" ]; then
          : >"$GH_STUB_STATE/watch-failed"
          exit 1
        fi
        ;;
      download)
        shift 2
        output_dir=
        while [ "$#" -gt 0 ]; do
          if [ "$1" = --dir ]; then
            output_dir="${2:-}"
            break
          fi
          shift
        done
        [ -n "$output_dir" ] || exit 89
        mkdir -p "$output_dir"
        printf '%s\n' '{"schema":"wukongim.cloud_lease.oidc_bootstrap/v1","result":{"region":"cn-hangzhou","account_id_hash":"sha256-account","oidc_provider_arn":"acs:ram::123:oidc-provider/github","oidc_audience":"wukongim-cloud-lease","provisioner_role_arn":"acs:ram::123:role/CloudLeaseProvisioner","observer_role_arn":"acs:ram::123:role/CloudLeaseObserver","releaser_role_arn":"acs:ram::123:role/CloudLeaseReleaser"}}' >"$output_dir/cloud-lease-oidc-output.json"
        ;;
      *) exit 88 ;;
    esac
    exit 0
    ;;
esac
printf 'unexpected gh command: %s\n' "$*" >&2
exit 91
`
	if err := os.WriteFile(path, []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
}
