package scripts_test

import (
	"os"
	"os/exec"
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

func TestCloudDeploymentActivationBindsRepairGenerationWithoutChangingLeaseIdentity(t *testing.T) {
	path := filepath.Join(repoRoot(t), ".github", "workflows", "cloud-deployment-activate.yml")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, fragment := range []string{
		"deployment_purpose:",
		"deployment_generation:",
		"DEPLOYMENT_PURPOSE: ${{ inputs.deployment_purpose }}",
		"DEPLOYMENT_GENERATION: ${{ inputs.deployment_generation }}",
		`[[ "$DEPLOYMENT_PURPOSE" == "immutable" || "$DEPLOYMENT_PURPOSE" == "repair" ]]`,
		`[[ "$DEPLOYMENT_GENERATION" =~ ^[1-9][0-9]*$ ]]`,
		`--purpose "$DEPLOYMENT_PURPOSE"`,
		`--generation "$DEPLOYMENT_GENERATION"`,
		`install -m 0600 "$lease_receipt" lease-receipt.json`,
		`--lease-receipt lease-receipt.json`,
		`lease_source_sha="$(jq -er .lease_source_sha deployment-plan.json)"`,
		`--source-sha "$lease_source_sha"`,
		`wukongim.cloud_deployment.plan/v2`,
		`wukongim.cloud_deployment.receipt/v2`,
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("repair activation contract missing %q", fragment)
		}
	}
}

func TestCloudDeploymentUpstreamProvenanceUsesGitHubCLITransport(t *testing.T) {
	path := filepath.Join(repoRoot(t), ".github", "workflows", "cloud-deployment-activate.yml")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	start := strings.Index(text, "- name: Authenticate exact successful upstream workflow runs")
	end := strings.Index(text, "- name: Download exact Lease Receipt")
	if start < 0 || end <= start {
		t.Fatal("activation workflow provenance step is missing or unordered")
	}
	provenance := text[start:end]
	if !strings.Contains(provenance, `gh api "/repos/${GITHUB_REPOSITORY}/actions/runs/${run_id}" >"${name}-run.json"`) {
		t.Fatal("upstream provenance must use the authenticated GitHub CLI API transport")
	}
	for _, forbidden := range []string{"curl ", "GITHUB_API_URL"} {
		if strings.Contains(provenance, forbidden) {
			t.Fatalf("upstream provenance retains runner-dependent transport %q", forbidden)
		}
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
		"wkbench-host-metrics.service wkbench-worker@1.service wkbench-worker@2.service wkbench-worker@3.service",
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
		"load-stage-runner", "sudo install -o root -g root -m 0755 /home/wkdeploy/bundle/scripts/run-chat-lifecycle-stage.sh /opt/wukongim/scripts/run-chat-lifecycle-stage.sh",
		"install-orchestrator-compat-user.sh", "${role}-orchestrator-compat", "load-orchestrator-compat",
		"install-frozen-worker-health-compat.sh", "load-worker-health-compat",
		"install-frozen-stage-process-compat.sh", "load-stage-process-compat",
		"prime-frozen-orchestrator-stage.sh", "load-orchestrator-stage-prime",
		"${role}-quiesce", "for unit in node-exporter.service wukongim.service wkbench-host-metrics.service",
		"load-quiesce", "for unit in node-exporter.service wkbench-host-metrics.service wkbench-worker@1.service wkbench-worker@2.service wkbench-worker@3.service wkbench-coordinator.service wkbench-formal.service wkbench-rehearsal.service prometheus.service wkanalysis.service caddy.service",
		`sudo systemctl cat "$unit" >/dev/null 2>&1 || continue; sudo systemctl stop "$unit" || exit $?`,
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

func TestCloudDeploymentRepairGenerationResetsOnlyFixedDataRootsAfterQuiesce(t *testing.T) {
	path := filepath.Join(repoRoot(t), "scripts", "cloud-deployment", "activate-hosts.sh")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, fragment := range []string{
		`deployment_purpose="$(jq -er '.purpose' "$WK_CLOUD_DEPLOYMENT_PLAN")"`,
		`deployment_generation="$(jq -er '.generation' "$WK_CLOUD_DEPLOYMENT_PLAN")"`,
		`[[ "$deployment_purpose" == repair && "$deployment_generation" -gt 1 ]]`,
		`/var/lib/wukongim-cloud/wukongim`,
		`/var/lib/wukongim-cloud/workers`,
		`/var/lib/wukongim-cloud/reports`,
		`/var/lib/wukongim-cloud/prometheus`,
		`/var/lib/wukongim-cloud/evidence`,
		`test ! -L "$root"`,
		`find -P "$root" -xdev -mindepth 1 -delete`,
		`test -z "$(find -P "$root" -xdev -mindepth 1 -print -quit)"`,
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("repair generation reset missing %q", fragment)
		}
	}
	if strings.Index(text, "${role}-quiesce") > strings.Index(text, "${role}-repair-reset") {
		t.Fatal("service repair reset must run only after the service units are quiesced")
	}
	if strings.Index(text, "load-quiesce") > strings.Index(text, "load-repair-reset") {
		t.Fatal("load repair reset must run only after the load units are quiesced")
	}
}

func TestCloudDeploymentHostQuiesceToleratesOnlyMissingFreshHostUnits(t *testing.T) {
	path := filepath.Join(repoRoot(t), "scripts", "cloud-deployment", "activate-hosts.sh")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)

	commands := map[string]string{
		"service": extractSingleQuotedCommandAfter(t, text, `cloud_ssh_retry "${role}-quiesce"`),
		"load":    extractSingleQuotedCommandAfter(t, text, "cloud_ssh_retry load-quiesce"),
	}
	fakeBin := t.TempDir()
	fakeSudo := filepath.Join(fakeBin, "sudo")
	if err := os.WriteFile(fakeSudo, []byte(`#!/usr/bin/env bash
if [[ "$1" != "systemctl" ]]; then
  exit 97
fi
case "$2" in
  cat) exit "${FAKE_SYSTEMCTL_CAT_STATUS:-0}" ;;
  stop) exit "${FAKE_SYSTEMCTL_STOP_STATUS:-0}" ;;
  *) exit 98 ;;
esac
`), 0o755); err != nil {
		t.Fatal(err)
	}

	for role, command := range commands {
		t.Run(role+"_fresh_host", func(t *testing.T) {
			cmd := exec.Command("bash", "-c", command)
			cmd.Env = append(os.Environ(),
				"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
				"FAKE_SYSTEMCTL_CAT_STATUS=5",
				"FAKE_SYSTEMCTL_STOP_STATUS=5",
			)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("fresh-host quiesce must ignore only absent units: %v\n%s", err, output)
			}
		})

		t.Run(role+"_stop_failure", func(t *testing.T) {
			cmd := exec.Command("bash", "-c", command)
			cmd.Env = append(os.Environ(),
				"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
				"FAKE_SYSTEMCTL_CAT_STATUS=0",
				"FAKE_SYSTEMCTL_STOP_STATUS=5",
			)
			if err := cmd.Run(); err == nil {
				t.Fatal("quiesce must preserve a real failure stopping an installed unit")
			}
		})
	}
}

func extractSingleQuotedCommandAfter(t *testing.T, text, marker string) string {
	t.Helper()
	markerIndex := strings.Index(text, marker)
	if markerIndex < 0 {
		t.Fatalf("activation script missing marker %q", marker)
	}
	tail := text[markerIndex+len(marker):]
	start := strings.Index(tail, "'")
	if start < 0 {
		t.Fatalf("activation command after %q is not single quoted", marker)
	}
	tail = tail[start+1:]
	end := strings.Index(tail, "'")
	if end < 0 {
		t.Fatalf("activation command after %q has no closing quote", marker)
	}
	return tail[:end]
}

func TestCloudDeploymentInvokedShellHelpersAreExecutable(t *testing.T) {
	for _, name := range []string{
		"activate-hosts.sh", "collect-readiness.sh", "validate-upstream-run.sh",
		"install-orchestrator-compat-user.sh", "install-frozen-worker-health-compat.sh",
		"install-frozen-stage-process-compat.sh",
		"prime-frozen-orchestrator-stage.sh",
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

func TestCloudDeploymentFrozenWorkerHealthCompatibilityIsNarrow(t *testing.T) {
	path := filepath.Join(repoRoot(t), "scripts", "cloud-deployment", "install-frozen-worker-health-compat.sh")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, fragment := range []string{
		"4daf86e4a88478ccdecd9675acee8414810413be",
		"b3a93b9f5f0ca88462ea9f77e910afdc8601c8ea24b4e1fe52916d416907118c",
		"7624a9237b0d40583eedd4447a01714b312cd1e957561e1f55e74fe424f7836b",
		"5d9c417ddb91a670a8336e775e93a064aca8f95b332b0876a8932d2ebf2ab6ed",
		`[[ "$control_sha" == "$frozen_orchestrator_control" ]] || exit 0`,
		`[[ "${WK_BENCH_WORKER_TOKEN:-}" =~ ^[0-9a-f]{64}$ ]]`,
		`-H "Authorization: Bearer ${WK_BENCH_WORKER_TOKEN}"`,
		`[[ "$target_sha256" == "$legacy_sha256" || "$target_sha256" == "$authenticated_sha256" || "$target_sha256" == "$prestart_process_wait_sha256" ]]`,
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("frozen worker-health compatibility helper missing %q", fragment)
		}
	}
}

func TestCloudDeploymentFrozenStageProcessCompatibilityIsNarrow(t *testing.T) {
	path := filepath.Join(repoRoot(t), "scripts", "cloud-deployment", "install-frozen-stage-process-compat.sh")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, fragment := range []string{
		"4daf86e4a88478ccdecd9675acee8414810413be",
		"5f2b8469d3f027cc693d9ce15f60a38006b677a86e24c483e07b55440a209fde",
		"23598d4a8f2d76a7abbf3b211b1dd61be47f57a27773f9d4619b730569289df2",
		`[[ "$control_sha" == "$frozen_orchestrator_control" ]] || exit 0`,
		`[[ "$(sha256sum "$unit_path" | awk '{print $1}')" == "$expected_unit_sha256" ]]`,
		`stage_unit="wkbench-${stage}.service"`,
		`wukongim_process_up{unit=\"" unit "\"}`,
		`up == 1 && cpu == 1 && memory == 1`,
		`exec "${command[@]}"`,
		"91-frozen-stage-process-evidence.conf",
		`ExecStart=${wrapper} ${stage}`,
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("frozen stage-process compatibility helper missing %q", fragment)
		}
	}
}

func TestCloudDeploymentFrozenOrchestratorStagePrimingIsNarrow(t *testing.T) {
	path := filepath.Join(repoRoot(t), "scripts", "cloud-deployment", "prime-frozen-orchestrator-stage.sh")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, fragment := range []string{
		"4daf86e4a88478ccdecd9675acee8414810413be",
		`[[ "$control_sha" == "$frozen_orchestrator_control" ]] || exit 0`,
		`capture("-(?<stage>rehearsal|formal)-[1-9][0-9]*$")`,
		`if systemctl is-active --quiet "$unit"`,
		"ExecStart=/bin/false", `systemctl start "$unit" >/dev/null 2>&1 || true`,
		`for ((probe = 0; probe < 50; probe++))`, `systemctl is-failed --quiet "$unit"`,
		"90-frozen-orchestrator-reset-prime.conf",
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("frozen orchestrator stage priming helper missing %q", fragment)
		}
	}
}

func TestCloudDeploymentFrozenOrchestratorCompatibilityIsNarrow(t *testing.T) {
	path := filepath.Join(repoRoot(t), "scripts", "cloud-deployment", "install-orchestrator-compat-user.sh")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, fragment := range []string{
		"4daf86e4a88478ccdecd9675acee8414810413be",
		`[[ "$control_sha" == "$frozen_orchestrator_control" ]] || exit 0`,
		"source_home=/home/wkdeploy", `.ssh/authorized_keys`, "ssh-ed25519",
		"wukong ALL=(ALL) NOPASSWD:ALL", "/usr/sbin/visudo -cf",
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("orchestrator compatibility helper missing %q", fragment)
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
