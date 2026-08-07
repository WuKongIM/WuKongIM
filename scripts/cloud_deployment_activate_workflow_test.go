package scripts_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCloudDeploymentActivationHasSSHAuthorityOnly(t *testing.T) {
	path := filepath.Join(repoRoot(t), ".github", "workflows", "cloud-deployment-activate.yml")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, fragment := range []string{
		"permissions:\n  contents: read\n  actions: read",
		"environment: cloud-deployment",
		"WK_CHAT_LIFECYCLE_WRAPPING_PRIVATE_KEY",
		"open-deployment-identity",
		"validate-upstream-run.sh lease-run.json .github/workflows/cloud-lease-provision.yml",
		"validate-upstream-run.sh bundle-run.json .github/workflows/cloud-deployment-bundle.yml",
		"trusted-deployment-tools/wkcloudbundle",
		`test "$(jq -er .control_sha bundle-manifest.json)" = "$BUNDLE_WORKFLOW_HEAD_SHA"`,
		"trusted-deployment-tools/wkcloudgate\" deployment-plan",
		"scripts/cloud-deployment/activate-hosts.sh",
		"write-deployment-failure.sh deployment-failure-state.json",
		"scripts/cloud-deployment/collect-readiness.sh",
		"trusted-deployment-tools/wkcloudgate\" deployment-gate",
		`manager_user="operator-$(openssl rand -hex 12)"`,
		`demo_user="$manager_user"`,
		`{username:$manager_user,password:$manager_password,permissions:[{resource:"*",actions:["r"]}]}`,
		"WK_CHAT_LEASE_EXPIRES_AT=%s",
		"WK_CHAT_LEASE_CREATED_AT=%s",
		"WK_CHAT_BUDGET_LIMIT_MICROS=%s",
		"WK_CHAT_BUDGET_OPERATIONAL_STOP_MICROS=%s",
		"WK_CHAT_BUDGET_COMMITTED_MICROS=%s",
		"WK_CHAT_BUDGET_ESTIMATED_MICROS=%s",
		"WK_CHAT_BUDGET_LINE_ITEMS_BASE64=%s",
		"wukongim.cloud_deployment.failure/v1",
		"Upload typed Deployment Receipt or failure evidence",
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("activation workflow missing %q", fragment)
		}
	}
	for _, forbidden := range []string{
		"id-token: write", "ALIBABA_CLOUD", "wkcloudlease", " quote ", " acquire ", " release ",
		"create-and-delete-paid-cloud-lease", "docker", "containerd", "podman", "schedule:", "push:", "pull_request:",
		`test "$lease_head_sha" = "$GITHUB_SHA"`, `test "$bundle_head_sha" = "$GITHUB_SHA"`,
	} {
		if strings.Contains(strings.ToLower(text), strings.ToLower(forbidden)) {
			t.Fatalf("activation workflow unexpectedly contains %q", forbidden)
		}
	}
	for _, fixedCredential := range []string{`demo_user=demo`, `username:"viewer"`} {
		if strings.Contains(text, fixedCredential) {
			t.Fatalf("activation workflow retains fixed UI credential %q", fixedCredential)
		}
	}
	uploadStart := strings.Index(text, "- name: Upload typed Deployment Receipt or failure evidence")
	cleanupStart := strings.Index(text, "- name: Remove runner credentials")
	if uploadStart < 0 || cleanupStart <= uploadStart {
		t.Fatal("activation workflow upload/cleanup phases are missing or unordered")
	}
	upload := text[uploadStart:cleanupStart]
	for _, secretArtifact := range []string{"runtime-node", "runtime-load", "deployment-key", "readiness-credentials", "GITHUB_ENV"} {
		if strings.Contains(upload, secretArtifact) {
			t.Fatalf("activation workflow uploads plaintext credential material %q", secretArtifact)
		}
	}
	if strings.Contains(text, `>>"$GITHUB_ENV"`) || !strings.Contains(text, "source readiness-credentials") ||
		!strings.Contains(text, "rm -f deployment-key readiness-credentials") {
		t.Fatal("activation workflow does not scope and remove UI readiness credentials")
	}
}

func TestCloudDeploymentReadinessCollectorIsBoundedAndUsesPrivateOrigins(t *testing.T) {
	path := filepath.Join(repoRoot(t), "scripts", "cloud-deployment", "collect-readiness.sh")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, fragment := range []string{
		"clock_offset_milliseconds", "data_filesystem_bytes", "system_filesystem_bytes",
		"/manager/nodes", "/manager/nodes/${node_id}/config", "/manager/slots", "/manager/controller/tasks?limit=50",
		"healthy_slot_replica_sets", "logical_slot_groups:$groups", "ready_workers",
		"runtime_config_nodes", "slot_replicas:$slot_replicas", "channel_replicas:$channel_replicas",
		"wkbench validate chat-lifecycle", "prometheus_targets_up", "demo_ready", "analysis_ready",
		"WK_CLOUD_MANAGER_USER", `(.permissions == [{resource:"*",actions:["r"]}])`,
		`[[ "$WK_CLOUD_MANAGER_USER" == "$WK_CLOUD_DEMO_USER" ]]`, "demo_asset=", `http://${load_public}${demo_asset}`,
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("readiness collector missing %q", fragment)
		}
	}
	if strings.Contains(strings.ToLower(text), "docker") {
		t.Fatal("readiness collector depends on Docker")
	}
	if strings.Contains(text, "slot_replicas:3") || strings.Contains(text, "channel_replicas:3") {
		t.Fatal("readiness collector asserts replica counts instead of observing effective node config")
	}
}

func TestCloudDeploymentHostActivationUsesExactTypedPhases(t *testing.T) {
	path := filepath.Join(repoRoot(t), "scripts", "cloud-deployment", "activate-hosts.sh")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	ordered := []string{
		"complete_gate bundle_transferred",
		"complete_gate bundle_verified",
		"complete_gate hosts_prepared",
		"complete_gate services_active",
	}
	previous := -1
	for _, fragment := range ordered {
		index := strings.Index(text, fragment)
		if index <= previous {
			t.Fatalf("activation phase %q is absent or out of order", fragment)
		}
		previous = index
	}
	for _, fragment := range []string{
		"bundle_transfer_failed plan_validated", "bundle_digest_mismatch bundle_transferred",
		"data_disk_mount_invalid bundle_verified", "native_activation_failed hosts_prepared",
		"credential_cleanup_failed services_active",
		"install-offline", "--no-systemd", "activate-offline",
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("activation script missing %q", fragment)
		}
	}
}

func TestCloudDeploymentInvokedShellHelpersAreExecutable(t *testing.T) {
	for _, name := range []string{
		"activate-hosts.sh", "collect-readiness.sh", "validate-upstream-run.sh",
		"write-deployment-failure.sh", "write-ssh-config.sh",
	} {
		path := filepath.Join(repoRoot(t), "scripts", "cloud-deployment", name)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o111 == 0 {
			t.Fatalf("workflow-invoked helper %s is not executable", name)
		}
	}
}
