package scripts_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCloudDeploymentUpstreamRunGate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.json")
	fixture := `{"repository":{"full_name":"WuKongIM/WuKongIM"},"head_repository":{"full_name":"WuKongIM/WuKongIM"},"event":"workflow_dispatch","head_branch":"main","status":"completed","conclusion":"success","path":".github/workflows/cloud-deployment-bundle.yml@refs/heads/main","head_sha":"0123456789012345678901234567890123456789"}`
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(repoRoot(t), "scripts", "cloud-deployment", "validate-upstream-run.sh")
	command := exec.Command("bash", script, path, ".github/workflows/cloud-deployment-bundle.yml")
	command.Env = append(os.Environ(), "WK_GITHUB_REPOSITORY=WuKongIM/WuKongIM")
	output, err := command.CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != "0123456789012345678901234567890123456789" {
		t.Fatalf("valid upstream run = %q, %v", output, err)
	}

	command = exec.Command("bash", script, path, ".github/workflows/cloud-lease-provision.yml")
	command.Env = append(os.Environ(), "WK_GITHUB_REPOSITORY=WuKongIM/WuKongIM")
	if output, err = command.CombinedOutput(); err == nil {
		t.Fatalf("wrong workflow passed: %s", output)
	}
}

func TestCloudDeploymentFailureWriterIsTypedAndBounded(t *testing.T) {
	outputPath := filepath.Join(t.TempDir(), "failure.json")
	script := filepath.Join(repoRoot(t), "scripts", "cloud-deployment", "write-deployment-failure.sh")
	command := exec.Command("bash", script, outputPath, "native_activation_failed", "hosts_prepared", "service-2", "activation failed", "service-2 prepared but inactive")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("write failure = %v, output=%s", err, output)
	}
	encoded, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Passed  bool `json:"passed"`
		Failure struct {
			Schema            string   `json:"schema"`
			Code              string   `json:"code"`
			LastCompletedGate string   `json:"last_completed_gate"`
			HostRole          string   `json:"host_role"`
			Evidence          []string `json:"evidence"`
		} `json:"failure"`
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if got.Passed || got.Failure.Schema != "wukongim.cloud_deployment.failure/v1" ||
		got.Failure.Code != "native_activation_failed" || got.Failure.LastCompletedGate != "hosts_prepared" ||
		got.Failure.HostRole != "service-2" || len(got.Failure.Evidence) != 2 {
		t.Fatalf("failure = %#v", got)
	}
}

func TestCloudDeploymentRuntimeContractReadsEffectiveReplicaCounts(t *testing.T) {
	fixture := `{"node_id":2,"source":"effective_startup_config","requires_restart":true,"groups":[{"items":[{"key":"WK_CLUSTER_HASH_SLOT_COUNT","value":"256","sensitive":false,"redacted":false},{"key":"WK_CLUSTER_INITIAL_SLOT_COUNT","value":"12","sensitive":false,"redacted":false},{"key":"WK_CLUSTER_SLOT_REPLICA_N","value":"3","sensitive":false,"redacted":false},{"key":"WK_CLUSTER_CHANNEL_REPLICA_N","value":"3","sensitive":false,"redacted":false}]}]}`
	filter := filepath.Join(repoRoot(t), "scripts", "cloud-deployment", "deployment-runtime-contract.jq")
	command := exec.Command("jq", "-cer", "--argjson", "node_id", "2", "-f", filter)
	command.Stdin = strings.NewReader(fixture)
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), `"channel_replicas":3`) {
		t.Fatalf("runtime contract = %s, %v", output, err)
	}

	command = exec.Command("jq", "-cer", "--argjson", "node_id", "1", "-f", filter)
	command.Stdin = strings.NewReader(fixture)
	if output, err = command.CombinedOutput(); err == nil {
		t.Fatalf("wrong node identity passed: %s", output)
	}
}
