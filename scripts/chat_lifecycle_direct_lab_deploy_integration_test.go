//go:build integration

package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
mkdir -p "$generation_dir/bundle-root"
printf '{}\n' >"$generation_dir/bundle-root/bundle-manifest.json"
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
jq -n --arg digest "$(jq -r .plan_digest "$WK_CLOUD_DEPLOYMENT_PLAN")" '{passed:true,receipt:{schema:"wukongim.cloud_deployment.receipt/v2",deployment_plan_digest:$digest}}'
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
