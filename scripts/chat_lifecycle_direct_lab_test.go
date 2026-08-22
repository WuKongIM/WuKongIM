package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeDirectLabExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestChatLifecycleDirectLabPreflightNeverTouchesProviderWithoutTemporaryCredentials(t *testing.T) {
	root := repoRoot(t)
	directory := t.TempDir()
	marker := filepath.Join(directory, "provider-called")
	cloudTool := filepath.Join(directory, "wkcloudlease")
	writeDirectLabExecutable(t, cloudTool, `#!/usr/bin/env bash
set -euo pipefail
: >"$WK_TEST_PROVIDER_MARKER"
exit 99
`)

	command := exec.Command("bash", filepath.Join(root, "scripts", "chat-lifecycle", "direct-lab.sh"), "preflight")
	command.Dir = root
	command.Env = append(os.Environ(),
		"WK_CHAT_LAB_CLOUD_TOOL="+cloudTool,
		"WK_TEST_PROVIDER_MARKER="+marker,
		"ALIBABA_CLOUD_ACCESS_KEY_ID=",
		"ALIBABA_CLOUD_ACCESS_KEY_SECRET=",
		"ALIBABA_CLOUD_SECURITY_TOKEN=",
		"WK_ALIBABA_LIFECYCLE_MUTATION_AUTHORIZATION=",
	)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("preflight succeeded without temporary Alibaba credentials: %s", output)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("preflight contacted the provider: %v", statErr)
	}
	for _, want := range []string{
		"ALIBABA_CLOUD_ACCESS_KEY_ID",
		"ALIBABA_CLOUD_ACCESS_KEY_SECRET",
		"ALIBABA_CLOUD_SECURITY_TOKEN",
		"WK_ALIBABA_CLOUD_SHELL_EPHEMERAL_AUTHORIZATION",
		"create-and-delete-paid-cloud-lease",
	} {
		if !strings.Contains(string(output), want) {
			t.Fatalf("preflight output missing %q: %s", want, output)
		}
	}
}

func TestChatLifecycleDirectLabPreflightAcceptsExplicitUnregisteredCloudShellCredential(t *testing.T) {
	root := repoRoot(t)
	directory := t.TempDir()
	cloudTool := filepath.Join(directory, "wkcloudlease")
	writeDirectLabExecutable(t, cloudTool, "#!/usr/bin/env bash\nexit 99\n")
	for _, tool := range []string{
		"git", "go", "ssh", "scp", "ssh-keygen", "ssh-agent", "ssh-add", "openssl",
		"curl", "tar", "python3", "sha256sum", "htpasswd",
	} {
		writeDirectLabExecutable(t, filepath.Join(directory, tool), "#!/usr/bin/env bash\nexit 0\n")
	}
	writeDirectLabExecutable(t, filepath.Join(directory, "bun"), "#!/usr/bin/env bash\nprintf '%s\\n' 1.3.11\n")
	writeDirectLabExecutable(t, filepath.Join(directory, "yarn"), "#!/usr/bin/env bash\nprintf '%s\\n' 1.22.22\n")
	writeDirectLabExecutable(t, filepath.Join(directory, "jq"), `#!/usr/bin/env bash
printf '%s\n' '{"schema":"wukongim.chat_lifecycle.direct_lab_preflight/v1","ready":true,"provider_contacted":false,"credential_kind":"cloud_shell_ephemeral_unregistered"}'
`)

	command := exec.Command("bash", filepath.Join(root, "scripts", "chat-lifecycle", "direct-lab.sh"), "preflight")
	command.Dir = root
	command.Env = append(os.Environ(),
		"PATH="+directory+":"+os.Getenv("PATH"),
		"WK_CHAT_LAB_CLOUD_TOOL="+cloudTool,
		"ALIBABA_CLOUD_ACCESS_KEY_ID=ephemeral-id",
		"ALIBABA_CLOUD_ACCESS_KEY_SECRET=ephemeral-secret",
		"ALIBABA_CLOUD_SECURITY_TOKEN=",
		"WK_ALIBABA_CLOUD_SHELL_EPHEMERAL_AUTHORIZATION=unregistered-one-hour-cloud-shell",
		"WK_ALIBABA_LIFECYCLE_MUTATION_AUTHORIZATION=create-and-delete-paid-cloud-lease",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("preflight rejected a verified Cloud Shell credential: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), `"credential_kind":"cloud_shell_ephemeral_unregistered"`) {
		t.Fatalf("preflight credential kind = %s", output)
	}
}

func TestChatLifecycleDirectLabStartRejectsAnotherUnreleasedRequestBeforeProviderContact(t *testing.T) {
	root := repoRoot(t)
	directory := t.TempDir()
	existingID := "chat-20260823T000001Z-01020304"
	existingDirectory := filepath.Join(directory, existingID)
	if err := os.MkdirAll(existingDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(existingDirectory, "state.json"), []byte(`{"schema":"wukongim.chat_lifecycle.direct_lab_state/v1","request_id":"`+existingID+`","state":"active"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(directory, "provider-called")
	cloudTool := filepath.Join(directory, "wkcloudlease")
	writeDirectLabExecutable(t, cloudTool, `#!/usr/bin/env bash
set -euo pipefail
: >"$WK_TEST_PROVIDER_MARKER"
exit 99
`)

	command := exec.Command("bash", filepath.Join(root, "scripts", "chat-lifecycle", "direct-lab.sh"), "start", "chat-20260823T000002Z-05060708")
	command.Dir = root
	command.Env = append(os.Environ(),
		"WK_CHAT_LAB_STATE_ROOT="+directory,
		"WK_CHAT_LAB_CLOUD_TOOL="+cloudTool,
		"WK_CHAT_LAB_PAID_AUTHORIZATION=create-paid-cloud-lease",
		"WK_CHAT_LAB_ALLOW_DIRTY_FOR_TESTS=true",
		"WK_TEST_PROVIDER_MARKER="+marker,
		"ALIBABA_CLOUD_ACCESS_KEY_ID=test-id",
		"ALIBABA_CLOUD_ACCESS_KEY_SECRET=test-secret",
		"ALIBABA_CLOUD_SECURITY_TOKEN=test-token",
		"WK_ALIBABA_LIFECYCLE_MUTATION_AUTHORIZATION=create-and-delete-paid-cloud-lease",
	)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("start accepted a second unresolved request: %s", output)
	}
	if !strings.Contains(string(output), existingID) {
		t.Fatalf("start did not identify the unresolved request: %s", output)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("rejected start contacted the provider: %v", statErr)
	}
}

func TestChatLifecycleDirectLabStopReleasesExactSelectorAndPersistsZeroProof(t *testing.T) {
	root := repoRoot(t)
	directory := t.TempDir()
	requestID := "chat-20260823T010203Z-a1b2c3d4"
	requestDirectory := filepath.Join(directory, requestID)
	if err := os.MkdirAll(requestDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	selector := `{"schema":"wukongim.cloud_lease.selector/v1","selector":{"lease_id":"lease-direct","request_id":"` + requestID + `","provider":"alibaba","region":"cn-hangzhou","repository":"WuKongIM/WuKongIM","plan_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}`
	if err := os.WriteFile(filepath.Join(requestDirectory, "release-selector.json"), []byte(selector), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(requestDirectory, "state.json"), []byte(`{"schema":"wukongim.chat_lifecycle.direct_lab_state/v1","request_id":"`+requestID+`","state":"active"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cloudTool := filepath.Join(directory, "wkcloudlease")
	writeDirectLabExecutable(t, cloudTool, `#!/usr/bin/env bash
set -euo pipefail
[[ "$1" == release && "$2" == --selector ]]
jq -n --slurpfile selector "$3" '{schema:"wukongim.cloud_lease.release/v1",result:{state:"released",residual_resources:0,zero_inventory:{selector:$selector[0].selector,account_id_hash:("sha256:"+("b"*64)),observed_at:"2026-08-23T01:03:00Z",scopes:["cn-hangzhou"]}}}'
`)

	command := exec.Command("bash", filepath.Join(root, "scripts", "chat-lifecycle", "direct-lab.sh"), "stop", requestID)
	command.Dir = root
	command.Env = append(os.Environ(),
		"WK_CHAT_LAB_STATE_ROOT="+directory,
		"WK_CHAT_LAB_CLOUD_TOOL="+cloudTool,
		"ALIBABA_CLOUD_ACCESS_KEY_ID=test-id",
		"ALIBABA_CLOUD_ACCESS_KEY_SECRET=test-secret",
		"ALIBABA_CLOUD_SECURITY_TOKEN=test-token",
		"WK_ALIBABA_LIFECYCLE_MUTATION_AUTHORIZATION=create-and-delete-paid-cloud-lease",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("stop failed: %v\n%s", err, output)
	}
	zeroProof := filepath.Join(requestDirectory, "zero-inventory.json")
	if body, err := os.ReadFile(zeroProof); err != nil || !strings.Contains(string(body), `"lease_id": "lease-direct"`) {
		t.Fatalf("zero proof = %s, %v", body, err)
	}
	state, err := os.ReadFile(filepath.Join(requestDirectory, "state.json"))
	if err != nil || !strings.Contains(string(state), `"state": "released"`) {
		t.Fatalf("state = %s, %v", state, err)
	}
}

func TestChatLifecycleDirectLabStartBuildsAndQuotesBeforePaidAcquire(t *testing.T) {
	root := repoRoot(t)
	directory := t.TempDir()
	requestID := "chat-20260823T020304Z-b1c2d3e4"
	callLog := filepath.Join(directory, "calls")
	bundleBuilder := filepath.Join(directory, "build-bundle")
	writeDirectLabExecutable(t, bundleBuilder, `#!/usr/bin/env bash
set -euo pipefail
printf 'build\n' >>"$WK_TEST_CALL_LOG"
[[ "$1" == --source-sha && "$3" == --output-dir ]]
mkdir -p "$4"
printf '{"schema":"wukongim.cloud_deployment.bundle_manifest/v1","source_sha":"%s","control_sha":"%s","bundle_digest":"sha256:%064d"}\n' "$2" "$2" 1 >"$4/bundle-manifest-output.json"
: >"$4/cloud-deployment-bundle.tar.gz"
`)
	chatTool := filepath.Join(directory, "wkchatlifecycle")
	writeDirectLabExecutable(t, chatTool, `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$1" >>"$WK_TEST_CALL_LOG"
case "$1" in
  materialize)
    printf '{"schema":"wukongim.chat_lifecycle.run_plan/v1","lease_plan":{"schema":"wukongim.cloud_lease/v1","lease_id":"lease-direct","request_id":"%s","repository":"WuKongIM/WuKongIM","provider":"alibaba","region":"cn-hangzhou"},"bootstrap_access":{"public_keys":["ssh-ed25519 fake"]}}\n' "$WK_TEST_REQUEST_ID"
    ;;
  selector-from-plan|selector)
    printf '{"schema":"wukongim.cloud_lease.selector/v1","selector":{"lease_id":"lease-direct","request_id":"%s","provider":"alibaba","region":"cn-hangzhou","repository":"WuKongIM/WuKongIM","plan_digest":"%064d"}}\n' "$WK_TEST_REQUEST_ID" 2
    ;;
  *) exit 98 ;;
esac
`)
	cloudTool := filepath.Join(directory, "wkcloudlease")
	writeDirectLabExecutable(t, cloudTool, `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$1" >>"$WK_TEST_CALL_LOG"
case "$1" in
  quote)
    printf '{"schema":"wukongim.cloud_lease.quote/v1","quote":{"request_id":"%s","plan_digest":"%064d","capacity_available":true,"quota_available":true,"selection":{"instance_type":"ecs.test"},"zone":"cn-hangzhou-a"}}\n' "$WK_TEST_REQUEST_ID" 2
    ;;
  acquire)
    printf '{"schema":"wukongim.cloud_lease.receipt/v1","receipt":{"lease_id":"lease-direct","request_id":"%s","provider":"alibaba","region":"cn-hangzhou","repository":"WuKongIM/WuKongIM","plan_digest":"%064d","state":"active","resources":[]}}\n' "$WK_TEST_REQUEST_ID" 2
    ;;
  *) exit 97 ;;
esac
`)

	command := exec.Command("bash", filepath.Join(root, "scripts", "chat-lifecycle", "direct-lab.sh"), "start", requestID)
	command.Dir = root
	command.Env = append(os.Environ(),
		"WK_CHAT_LAB_STATE_ROOT="+directory,
		"WK_CHAT_LAB_CLOUD_TOOL="+cloudTool,
		"WK_CHAT_LAB_CHAT_TOOL="+chatTool,
		"WK_CHAT_LAB_BUNDLE_BUILDER="+bundleBuilder,
		"WK_CHAT_LAB_PAID_AUTHORIZATION=create-paid-cloud-lease",
		"WK_CHAT_LAB_ALLOW_DIRTY_FOR_TESTS=true",
		"WK_TEST_CALL_LOG="+callLog,
		"WK_TEST_REQUEST_ID="+requestID,
		"ALIBABA_CLOUD_ACCESS_KEY_ID=test-id",
		"ALIBABA_CLOUD_ACCESS_KEY_SECRET=test-secret",
		"ALIBABA_CLOUD_SECURITY_TOKEN=test-token",
		"WK_ALIBABA_LIFECYCLE_MUTATION_AUTHORIZATION=create-and-delete-paid-cloud-lease",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("start failed: %v\n%s", err, output)
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(calls), "build\nmaterialize\nquote\nselector-from-plan\nacquire\nselector\n"; got != want {
		t.Fatalf("operation order = %q, want %q", got, want)
	}
	requestDirectory := filepath.Join(directory, requestID)
	for _, name := range []string{"run-plan.json", "quote.json", "receipt.json", "release-selector.json", "diagnostic_ed25519", "deployment_ed25519"} {
		info, statErr := os.Stat(filepath.Join(requestDirectory, name))
		if statErr != nil {
			t.Fatalf("missing %s: %v", name, statErr)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("%s mode = %o, want private", name, info.Mode().Perm())
		}
	}
	state, err := os.ReadFile(filepath.Join(requestDirectory, "state.json"))
	if err != nil || !strings.Contains(string(state), `"state": "active"`) {
		t.Fatalf("state = %s, %v", state, err)
	}
}

func TestChatLifecycleLocalBundleBuilderUsesExactLocalRevisionWithoutGitHubActions(t *testing.T) {
	root := repoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "scripts", "cloud-deployment", "build-local-bundle.sh"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{
		"git clone --shared --no-checkout",
		"checkout --detach",
		"bun install --frozen-lockfile",
		"yarn install --frozen-lockfile",
		"GOOS=linux GOARCH=amd64",
		"wkcloudbundle-host",
		"PROMETHEUS_LINUX_AMD64_SHA256",
		"NODE_EXPORTER_LINUX_AMD64_SHA256",
		"CADDY_LINUX_AMD64_SHA256",
		"seal-offline",
		"verify-offline",
		"cloud-deployment-bundle.tar.gz",
		"printf '%s  %s\\n' \"$archive_sha256\" cloud-deployment-bundle.tar.gz",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("local bundle builder missing %q", want)
		}
	}
	for _, forbidden := range []string{"gh workflow", "actions/download-artifact", "GITHUB_TOKEN", "GH_TOKEN"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("local bundle builder contains GitHub orchestration %q", forbidden)
		}
	}
}

func TestChatLifecycleDirectLabDeployActivatesAndGatesOneLocalGeneration(t *testing.T) {
	root := repoRoot(t)
	directory := t.TempDir()
	requestID := "chat-20260823T030405Z-c1d2e3f4"
	requestDirectory := filepath.Join(directory, requestID)
	if err := os.MkdirAll(requestDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"state.json":   `{"schema":"wukongim.chat_lifecycle.direct_lab_state/v1","request_id":"` + requestID + `","source_sha":"` + strings.Repeat("a", 40) + `","state":"active","generation":0}`,
		"receipt.json": `{"schema":"wukongim.cloud_lease.receipt/v1","receipt":{"lease_id":"lease-direct","request_id":"` + requestID + `","state":"active"}}`,
	} {
		if err := os.WriteFile(filepath.Join(requestDirectory, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"diagnostic_ed25519", "diagnostic_ed25519.pub", "deployment_ed25519", "deployment_ed25519.pub"} {
		if err := os.WriteFile(filepath.Join(requestDirectory, name), []byte("test-key"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	callLog := filepath.Join(directory, "calls")
	makeTool := func(name, body string) string {
		path := filepath.Join(directory, name)
		writeDirectLabExecutable(t, path, "#!/usr/bin/env bash\nset -euo pipefail\n"+body)
		return path
	}
	builder := makeTool("builder", `printf 'build\n' >>"$WK_TEST_CALL_LOG"
mkdir -p "$4"
printf '{"bundle_digest":"sha256:%064d"}\n' 3 >"$4/bundle-manifest-output.json"
: >"$4/cloud-deployment-bundle.tar.gz"
`)
	preparer := makeTool("preparer", `printf 'prepare\n' >>"$WK_TEST_CALL_LOG"
generation_dir="$WK_CHAT_LAB_GENERATION_DIR"
printf '{"schema":"wukongim.cloud_deployment.plan/v2","plan_digest":"sha256:%064d","generation":1,"hosts":[{"role":"service-1","private_address":"10.0.0.1"},{"role":"service-2","private_address":"10.0.0.2"},{"role":"service-3","private_address":"10.0.0.3"},{"role":"load","public_address":"203.0.113.4"}]}' 4 >"$generation_dir/deployment-plan.json"
: >"$generation_dir/runtime-node.tar.gz"
: >"$generation_dir/runtime-load.tar.gz"
: >"$generation_dir/readiness-credentials"
`)
	sshWriter := makeTool("ssh-writer", `printf 'ssh-config\n' >>"$WK_TEST_CALL_LOG"
: >"$WK_CLOUD_SSH_CONFIG"
`)
	activator := makeTool("activator", `printf 'activate\n' >>"$WK_TEST_CALL_LOG"
`)
	readiness := makeTool("readiness", `printf 'readiness\n' >>"$WK_TEST_CALL_LOG"
printf '{"schema":"wukongim.cloud_deployment.readiness/v1"}\n' >"$WK_CLOUD_READINESS_OUTPUT"
`)
	gate := makeTool("gate", `printf 'gate\n' >>"$WK_TEST_CALL_LOG"
printf '{"passed":true,"receipt":{"schema":"wukongim.cloud_deployment.receipt/v2"}}\n'
`)

	command := exec.Command("bash", filepath.Join(root, "scripts", "chat-lifecycle", "direct-lab.sh"), "deploy", requestID)
	command.Dir = root
	command.Env = append(os.Environ(),
		"WK_CHAT_LAB_STATE_ROOT="+directory,
		"WK_CHAT_LAB_BUNDLE_BUILDER="+builder,
		"WK_CHAT_LAB_DEPLOY_PREPARER="+preparer,
		"WK_CHAT_LAB_SSH_CONFIG_WRITER="+sshWriter,
		"WK_CHAT_LAB_ACTIVATOR="+activator,
		"WK_CHAT_LAB_READINESS="+readiness,
		"WK_CHAT_LAB_GATE_TOOL="+gate,
		"WK_CHAT_LAB_BUNDLE_TOOL="+gate,
		"WK_CHAT_LAB_ALLOW_DIRTY_FOR_TESTS=true",
		"WK_CHAT_LAB_CLOUD_TOOL="+gate,
		"WK_CHAT_LAB_CHAT_TOOL="+gate,
		"WK_TEST_CALL_LOG="+callLog,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("deploy failed: %v\n%s", err, output)
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(calls), "build\nprepare\nssh-config\nactivate\nreadiness\ngate\n"; got != want {
		t.Fatalf("deploy operation order = %q, want %q", got, want)
	}
	state, err := os.ReadFile(filepath.Join(requestDirectory, "state.json"))
	if err != nil || !strings.Contains(string(state), `"state": "deployed"`) ||
		!strings.Contains(string(state), `"generation": 1`) ||
		!strings.Contains(string(state), `"bundle_digest": "sha256:`+strings.Repeat("0", 63)+`3"`) {
		t.Fatalf("state = %s, %v", state, err)
	}
}

func TestChatLifecycleLocalRuntimePreparationKeepsCredentialsLocal(t *testing.T) {
	root := repoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "scripts", "cloud-deployment", "prepare-local-runtime.sh"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{
		"deployment-plan",
		"verify-offline",
		"WK_ANALYSIS_GITHUB_OIDC_ENABLED=false",
		"runtime-node.tar.gz",
		"runtime-load.tar.gz",
		"readiness-credentials",
		"access.json",
		"chmod 0600",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("local runtime preparation missing %q", want)
		}
	}
	for _, forbidden := range []string{"GITHUB_TOKEN", "GH_TOKEN", "actions/upload-artifact", "seal-access"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("local runtime preparation contains remote handoff %q", forbidden)
		}
	}
}

func TestChatLifecycleDirectLabRunStopsOnStallAndKeepsLeaseForDiagnosis(t *testing.T) {
	root := repoRoot(t)
	directory := t.TempDir()
	requestID := "chat-20260823T040506Z-d1e2f3a4"
	requestDirectory := filepath.Join(directory, requestID)
	generationDirectory := filepath.Join(requestDirectory, "generations", "1")
	if err := os.MkdirAll(generationDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{
		filepath.Join(requestDirectory, "state.json"):              `{"schema":"wukongim.chat_lifecycle.direct_lab_state/v1","request_id":"` + requestID + `","lease_id":"lease-direct","source_sha":"` + strings.Repeat("a", 40) + `","bundle_digest":"sha256:` + strings.Repeat("b", 64) + `","state":"deployed","generation":1}`,
		filepath.Join(requestDirectory, "deployment-ssh-config"):   "Host wukong-load\n",
		filepath.Join(generationDirectory, "deployment-plan.json"): `{"schema":"wukongim.cloud_deployment.plan/v2"}`,
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	callLog := filepath.Join(directory, "calls")
	stageStarter := filepath.Join(directory, "stage-starter")
	writeDirectLabExecutable(t, stageStarter, `#!/usr/bin/env bash
set -euo pipefail
printf 'stage-start\n' >>"$WK_TEST_CALL_LOG"
printf '{"schema":"wukongim.chat_lifecycle.run_start/v1","stage":"rehearsal","started_at":"2026-08-23T04:05:06Z","expected_end_at":"2026-08-23T06:05:06Z","run_hash":"sha256:%064d","assignment_hash":"sha256:%064d","generation":1}\n' 1 2 >"$WK_CHAT_LAB_RUN_START_OUTPUT"
`)
	chatTool := filepath.Join(directory, "wkchatlifecycle")
	writeDirectLabExecutable(t, chatTool, `#!/usr/bin/env bash
set -euo pipefail
[[ "$1" == repair-begin ]]
printf 'repair-begin\n' >>"$WK_TEST_CALL_LOG"
printf '{"schema":"wukongim.chat_lifecycle.repair_state/v1"}\n'
`)
	monitor := filepath.Join(directory, "repair-monitor")
	writeDirectLabExecutable(t, monitor, `#!/usr/bin/env bash
set -euo pipefail
printf 'monitor\n' >>"$WK_TEST_CALL_LOG"
printf '{"schema":"wukongim.chat_lifecycle.repair_step/v1","decision":{"action":"stop_and_diagnose","reason":"send_progress_stalled","observed_at":"2026-08-23T04:05:21Z"}}\n' >"$WK_CHAT_REPAIR_OUTPUT_DIR/repair-decision.json"
printf '{"schema":"wukongim.chat_lifecycle.repair_diagnosis/v1","reason":"send_progress_stalled"}\n' >"$WK_CHAT_REPAIR_OUTPUT_DIR/repair-diagnosis.json"
exit 10
`)

	command := exec.Command("bash", filepath.Join(root, "scripts", "chat-lifecycle", "direct-lab.sh"), "run", requestID)
	command.Dir = root
	command.Env = append(os.Environ(),
		"WK_CHAT_LAB_STATE_ROOT="+directory,
		"WK_CHAT_LAB_CHAT_TOOL="+chatTool,
		"WK_CHAT_LAB_STAGE_STARTER="+stageStarter,
		"WK_CHAT_LAB_REPAIR_MONITOR="+monitor,
		"WK_TEST_CALL_LOG="+callLog,
	)
	output, err := command.CombinedOutput()
	if err == nil || command.ProcessState.ExitCode() != 10 {
		t.Fatalf("run exit = %v, code=%d\n%s", err, command.ProcessState.ExitCode(), output)
	}
	if calls, readErr := os.ReadFile(callLog); readErr != nil || string(calls) != "stage-start\nrepair-begin\nmonitor\n" {
		t.Fatalf("run calls = %q, %v", calls, readErr)
	}
	state, readErr := os.ReadFile(filepath.Join(requestDirectory, "state.json"))
	if readErr != nil || !strings.Contains(string(state), `"state": "diagnosis_ready"`) || !strings.Contains(string(state), `"reason": "send_progress_stalled"`) {
		t.Fatalf("state = %s, %v", state, readErr)
	}
	if _, statErr := os.Stat(filepath.Join(requestDirectory, "zero-inventory.json")); !os.IsNotExist(statErr) {
		t.Fatalf("failed short run released or fabricated zero proof: %v", statErr)
	}
}

func TestChatLifecycleDirectLabDiagnoseUsesOnlyLocalSSHCollector(t *testing.T) {
	root := repoRoot(t)
	directory := t.TempDir()
	requestID := "chat-20260823T050607Z-e1f2a3b4"
	requestDirectory := filepath.Join(directory, requestID)
	if err := os.MkdirAll(requestDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(requestDirectory, "state.json"), []byte(`{"schema":"wukongim.chat_lifecycle.direct_lab_state/v1","request_id":"`+requestID+`","state":"diagnosis_ready","generation":2,"reason":"online_stalled"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(requestDirectory, "deployment-ssh-config"), []byte("Host wukong-load\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	providerMarker := filepath.Join(directory, "provider-called")
	collector := filepath.Join(directory, "collector")
	writeDirectLabExecutable(t, collector, `#!/usr/bin/env bash
set -euo pipefail
mkdir -p "$WK_CHAT_LAB_DIAGNOSIS_DIR"
printf '{"schema":"wukongim.chat_lifecycle.local_diagnosis/v1","classification":"product_or_harness"}\n' >"$WK_CHAT_LAB_DIAGNOSIS_DIR/summary.json"
`)
	cloud := filepath.Join(directory, "cloud")
	writeDirectLabExecutable(t, cloud, `#!/usr/bin/env bash
: >"$WK_TEST_PROVIDER_MARKER"
exit 99
`)
	command := exec.Command("bash", filepath.Join(root, "scripts", "chat-lifecycle", "direct-lab.sh"), "diagnose", requestID)
	command.Dir = root
	command.Env = append(os.Environ(),
		"WK_CHAT_LAB_STATE_ROOT="+directory,
		"WK_CHAT_LAB_DIAGNOSIS_COLLECTOR="+collector,
		"WK_CHAT_LAB_CLOUD_TOOL="+cloud,
		"WK_TEST_PROVIDER_MARKER="+providerMarker,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("diagnose failed: %v\n%s", err, output)
	}
	if _, statErr := os.Stat(providerMarker); !os.IsNotExist(statErr) {
		t.Fatalf("diagnose contacted provider mutation tool: %v", statErr)
	}
	if !strings.Contains(string(output), `"classification": "product_or_harness"`) {
		t.Fatalf("diagnose output = %s", output)
	}
}
