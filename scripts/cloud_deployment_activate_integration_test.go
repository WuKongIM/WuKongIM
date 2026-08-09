//go:build integration

package scripts_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCloudDeploymentActivateHostsWithFakeSSH(t *testing.T) {
	for _, fixture := range []struct {
		name        string
		failTool    string
		failHost    string
		failCommand string
		wantCode    string
		wantGate    string
		wantRole    string
	}{
		{name: "success", wantGate: "services_active"},
		{name: "transfer", failTool: "scp", wantCode: "bundle_transfer_failed", wantGate: "plan_validated", wantRole: "load"},
		{name: "verification", failHost: "10.42.0.12", failCommand: "verify-offline", wantCode: "bundle_digest_mismatch", wantGate: "bundle_transferred", wantRole: "service-2"},
		{name: "orchestrator compatibility", failHost: "10.42.0.12", failCommand: "orchestrator-compat", wantCode: "credential_materialization_failed", wantGate: "bundle_verified", wantRole: "service-2"},
		{name: "repair quiesce", failHost: "10.42.0.12", failCommand: "systemctl stop", wantCode: "data_disk_mount_invalid", wantGate: "bundle_verified", wantRole: "service-2"},
		{name: "preparation", failHost: "10.42.0.13", failCommand: "install-offline", wantCode: "data_disk_mount_invalid", wantGate: "bundle_verified", wantRole: "service-3"},
		{name: "config normalization", failHost: "10.42.0.11", failCommand: "normalize-config", wantCode: "data_disk_mount_invalid", wantGate: "bundle_verified", wantRole: "service-1"},
		{name: "activation", failHost: "wukong-load", failCommand: "activate-offline", wantCode: "native_activation_failed", wantGate: "hosts_prepared", wantRole: "load"},
		{name: "credential cleanup", failHost: "10.42.0.12", failCommand: "rm -rf /home/wkdeploy/run-secrets /home/wkdeploy/runtime-node.tar.gz", wantCode: "credential_cleanup_failed", wantGate: "services_active", wantRole: "service-2"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			root := t.TempDir()
			fakeBin := filepath.Join(root, "bin")
			if err := os.Mkdir(fakeBin, 0o755); err != nil {
				t.Fatal(err)
			}
			writeFakeDeploymentCommand(t, fakeBin, "ssh", `#!/usr/bin/env bash
set -euo pipefail
joined="$*"
if [[ "$joined" == *"root_source="* ]]; then printf '/dev/fake-data\n'; exit 0; fi
if [[ -n "${WK_FAKE_FAIL_COMMAND:-}" && "$WK_FAKE_FAIL_COMMAND" == normalize-config && "$joined" == *"${WK_FAKE_FAIL_HOST:-}"* && "$joined" == *"sed -i"* ]]; then exit 42; fi
if [[ -n "${WK_FAKE_FAIL_COMMAND:-}" && "$WK_FAKE_FAIL_COMMAND" == orchestrator-compat && "$joined" == *"${WK_FAKE_FAIL_HOST:-}"* && "$joined" == *"sudo bash /home/wkdeploy/install-orchestrator-compat-user.sh"* ]]; then exit 42; fi
if [[ -n "${WK_FAKE_FAIL_HOST:-}" && -n "${WK_FAKE_FAIL_COMMAND:-}" && "$WK_FAKE_FAIL_COMMAND" != orchestrator-compat && "$joined" == *"$WK_FAKE_FAIL_HOST"* && "$joined" == *"$WK_FAKE_FAIL_COMMAND"* ]]; then exit 42; fi
exit 0
`)
			writeFakeDeploymentCommand(t, fakeBin, "scp", `#!/usr/bin/env bash
set -euo pipefail
if [[ "${WK_FAKE_FAIL_TOOL:-}" == scp ]]; then exit 42; fi
exit 0
`)
			writeFakeDeploymentCommand(t, fakeBin, "ssh-add", "#!/usr/bin/env bash\nexit 0\n")
			writeFakeDeploymentCommand(t, fakeBin, "timeout", `#!/usr/bin/env bash
set -euo pipefail
while [[ "${1:-}" == --* ]]; do shift; done
shift
exec "$@"
`)
			writeFakeDeploymentCommand(t, fakeBin, "ssh-agent", `#!/usr/bin/env bash
if [[ "${1:-}" == -s ]]; then
  echo 'SSH_AUTH_SOCK=/tmp/fake-agent.sock; export SSH_AUTH_SOCK;'
  echo 'SSH_AGENT_PID=999999; export SSH_AGENT_PID;'
fi
exit 0
`)

			for _, name := range []string{"cloud-deployment-bundle.tar.gz", "runtime-node.tar.gz", "runtime-load.tar.gz", "deployment-key", "deployment-ssh-config"} {
				if err := os.WriteFile(filepath.Join(root, name), []byte("fixture\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			planPath := filepath.Join(root, "deployment-plan.json")
			plan := `{"topology":{"physical_hash_slots":256,"logical_slot_groups":12,"slot_replicas":3,"channel_replicas":3},"hosts":[{"role":"service-1","private_address":"10.42.0.11"},{"role":"service-2","private_address":"10.42.0.12"},{"role":"service-3","private_address":"10.42.0.13"},{"role":"load","private_address":"10.42.0.20"}]}`
			if err := os.WriteFile(planPath, []byte(plan), 0o600); err != nil {
				t.Fatal(err)
			}
			failurePath := filepath.Join(root, "failure.json")
			gatePath := filepath.Join(root, "last-gate.txt")
			command := exec.Command("bash", filepath.Join(repoRoot(t), "scripts", "cloud-deployment", "activate-hosts.sh"))
			command.Env = append(os.Environ(),
				"PATH="+fakeBin+":"+os.Getenv("PATH"),
				"RUNNER_TEMP="+root,
				"WK_CLOUD_DEPLOYMENT_PLAN="+planPath,
				"WK_CLOUD_BUNDLE_ARCHIVE="+filepath.Join(root, "cloud-deployment-bundle.tar.gz"),
				"WK_CLOUD_RUNTIME_NODE_ARCHIVE="+filepath.Join(root, "runtime-node.tar.gz"),
				"WK_CLOUD_RUNTIME_LOAD_ARCHIVE="+filepath.Join(root, "runtime-load.tar.gz"),
				"WK_CLOUD_SSH_CONFIG="+filepath.Join(root, "deployment-ssh-config"),
				"WK_CLOUD_SSH_KEY="+filepath.Join(root, "deployment-key"),
				"WK_CLOUD_FAILURE_OUTPUT="+failurePath,
				"WK_CLOUD_LAST_GATE_OUTPUT="+gatePath,
				"WK_CLOUD_SSH_DEADLINE_EPOCH="+strconv.FormatInt(time.Now().Add(time.Minute).Unix(), 10),
				"WK_FAKE_FAIL_TOOL="+fixture.failTool,
				"WK_FAKE_FAIL_HOST="+fixture.failHost,
				"WK_FAKE_FAIL_COMMAND="+fixture.failCommand,
			)
			output, err := command.CombinedOutput()
			if fixture.wantCode == "" {
				if err != nil {
					t.Fatalf("activate-hosts error = %v, output=%s", err, output)
				}
				gate, readErr := os.ReadFile(gatePath)
				if readErr != nil || strings.TrimSpace(string(gate)) != fixture.wantGate {
					t.Fatalf("last gate = %q, %v", gate, readErr)
				}
				return
			}
			if err == nil {
				t.Fatalf("activate-hosts unexpectedly passed, output=%s", output)
			}
			encoded, readErr := os.ReadFile(failurePath)
			if readErr != nil {
				t.Fatal(readErr)
			}
			var got struct {
				Failure struct {
					Code              string `json:"code"`
					LastCompletedGate string `json:"last_completed_gate"`
					HostRole          string `json:"host_role"`
				} `json:"failure"`
			}
			if err := json.Unmarshal(encoded, &got); err != nil {
				t.Fatal(err)
			}
			if got.Failure.Code != fixture.wantCode || got.Failure.LastCompletedGate != fixture.wantGate || got.Failure.HostRole != fixture.wantRole {
				t.Fatalf("failure = %#v, output=%s", got.Failure, output)
			}
		})
	}
}

func writeFakeDeploymentCommand(t *testing.T, directory, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}
