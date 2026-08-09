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
		`jq -er .receipt.request_id "$lease_receipt"`,
		`jq -er .receipt.lease_id "$lease_receipt"`,
		`jq -er .receipt.provenance.source_sha "$lease_receipt"`,
		`jq -er .receipt.plan_digest "$lease_receipt"`,
		`lease_plan_digest="$(jq -er '.lease_plan_digest | select(test("^sha256:[0-9a-f]{64}$")) | sub("^sha256:"; "")' deployment-plan.json)"`,
		`--plan-digest "$lease_plan_digest"`,
		`plan_digest="$(jq -er '.plan_digest | select(test("^sha256:[0-9a-f]{64}$"))' deployment-plan.json)"`,
		"trusted-deployment-tools/wkcloudgate\" deployment-plan",
		`--bootstrap-pubkey "$deployment_public_key"`,
		`--bootstrap-pubkey "$CODEX_DIAGNOSTIC_PUBKEY"`,
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
		`jq -er .request_id "$lease_receipt"`, `jq -er .lease_id "$lease_receipt"`,
		`jq -er .provenance.source_sha "$lease_receipt"`, `jq -er .plan_digest "$lease_receipt"`,
		`--plan-digest "$(jq -er .lease_plan_digest deployment-plan.json)"`,
		`plan_digest="$(jq -er .plan_digest deployment-plan.json)"`,
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
		"chronyc waitsync 1 1.0", "chronyc -c tracking", `$14 == "Normal"`, `$5 * 1000`,
		"/manager/nodes", "/manager/nodes/${node_id}/config", "/manager/slots", "/manager/controller/tasks?limit=50",
		"healthy_slot_replica_sets", "logical_slot_groups:$groups", "ready_workers",
		"runtime_config_nodes", "slot_replicas:$slot_replicas", "channel_replicas:$channel_replicas",
		"wkbench validate chat-lifecycle", "prometheus_targets_up", "demo_ready", "analysis_ready",
		`grep -c \"^WK_BENCH_WORKER_TOKEN=\" /etc/wukongim/secrets/load.env`,
		`-H \"Authorization: Bearer \$worker_token\"`,
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
		"install-offline", "--no-systemd", "activate-offline", "${role}-normalize-config",
		"${role}-quiesce", "sudo systemctl stop node-exporter.service wukongim.service wkbench-host-metrics.service",
		"load-quiesce", "sudo systemctl stop node-exporter.service wkbench-host-metrics.service wkbench-worker@1.service wkbench-worker@2.service wkbench-worker@3.service wkbench-coordinator.service wkbench-formal.service wkbench-rehearsal.service prometheus.service wkanalysis.service caddy.service",
		`if test \"\$first\" = 'mode = \"release\"' && test -z \"\$second\"; then sudo sed -i '1,2d' /etc/wukongim/wukongim.toml; fi`,
		`test \"\$(sudo sed -n '1p' /etc/wukongim/wukongim.toml)\" = '[node]'`,
		`if ! sudo grep -qxF '[log]' /etc/wukongim/wukongim.toml`,
		`dir = \\\"/var/lib/wukongim-cloud/logs\\\"`,
		`sudo grep -cxF '[log]' /etc/wukongim/wukongim.toml`,
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

func TestCloudDeploymentUsesTheProvisionedBootstrapUser(t *testing.T) {
	root := repoRoot(t)
	sshConfig, err := os.ReadFile(filepath.Join(root, "scripts", "cloud-deployment", "write-ssh-config.sh"))
	if err != nil {
		t.Fatal(err)
	}
	activation, err := os.ReadFile(filepath.Join(root, "scripts", "cloud-deployment", "activate-hosts.sh"))
	if err != nil {
		t.Fatal(err)
	}
	cloudInit, err := os.ReadFile(filepath.Join(root, "internal", "infra", "cloudlease", "alibaba", "lifecycle_openapi.go"))
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"SSH config": string(sshConfig), "activation": string(activation), "cloud-init": string(cloudInit),
	} {
		if !strings.Contains(content, "wkdeploy") {
			t.Fatalf("%s does not use the provisioned bootstrap user", name)
		}
	}
	for name, content := range map[string]string{"SSH config": string(sshConfig), "activation": string(activation)} {
		if strings.Contains(content, "User wukong") || strings.Contains(content, "wukong@") || strings.Contains(content, "/home/wukong/") {
			t.Fatalf("%s still uses the nonexistent deployment user", name)
		}
	}
}
