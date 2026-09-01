//go:build integration

package scripts_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDockerImageRuntimeContract(t *testing.T) {
	if os.Getenv("WK_DOCKER_IMAGE_INTEGRATION") != "1" {
		t.Skip("set WK_DOCKER_IMAGE_INTEGRATION=1 to build and validate the Docker image runtime contract")
	}

	runHeavyShellScriptTestInParallel(t)
	root := repoRoot(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	image := "wukongim-image-contract:" + suffix
	container := "wukongim-image-contract-" + suffix
	volume := "wukongim-image-contract-" + suffix

	runDockerContract(t, root, "build", "--pull", "--tag", image, ".")
	t.Cleanup(func() {
		removeDockerContractObject(root, "image", "rm", image)
	})

	var config struct {
		User        string `json:"User"`
		StopSignal  string `json:"StopSignal"`
		Healthcheck *struct {
			Test []string `json:"Test"`
		} `json:"Healthcheck"`
	}
	inspect := runDockerContract(t, root, "image", "inspect", image, "--format", "{{json .Config}}")
	if err := json.Unmarshal([]byte(strings.TrimSpace(inspect)), &config); err != nil {
		t.Fatalf("decode image config: %v\n%s", err, inspect)
	}
	if config.User != "10001:10001" {
		t.Fatalf("image user = %q, want 10001:10001", config.User)
	}
	if config.StopSignal != "SIGTERM" {
		t.Fatalf("image stop signal = %q, want SIGTERM", config.StopSignal)
	}
	if config.Healthcheck == nil || !strings.Contains(strings.Join(config.Healthcheck.Test, " "), "http://127.0.0.1:5001/readyz") {
		t.Fatalf("image healthcheck = %#v, want /readyz", config.Healthcheck)
	}

	runDockerContract(t, root, "volume", "create", volume)
	t.Cleanup(func() {
		removeDockerContractObject(root, "volume", "rm", volume)
	})

	configPath := filepath.Join(root, "wukongim.toml.example")
	runDockerContract(t, root,
		"run", "-d",
		"--name", container,
		"--hostname", "wukongim-node1",
		"--env", "WK_NODE_DATA_DIR=/var/lib/wukongim",
		"--env", "WK_CLUSTER_LISTEN_ADDR=0.0.0.0:7000",
		"--env", `WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
		"--env", "WK_CLUSTER_JOIN_TOKEN=docker-image-contract-token",
		"--env", "WK_API_LISTEN_ADDR=0.0.0.0:5001",
		"--env", "WK_MANAGER_LISTEN_ADDR=0.0.0.0:5301",
		"--env", "WK_MANAGER_JWT_SECRET=docker-image-contract-manager-secret",
		"--env", `WK_MANAGER_USERS=[{"username":"admin","password":"docker-image-contract-password","permissions":[{"resource":"*","actions":["*"]}]}]`,
		"--env", "WK_LOG_DIR=/var/lib/wukongim/logs",
		"--env", "WK_PROMETHEUS_DATA_DIR=/var/lib/wukongim/prometheus",
		"--env", "WK_PLUGIN_DIR=/var/lib/wukongim/plugins",
		"--env", "WK_PLUGIN_SOCKET_PATH=/run/wukongim/plugin.sock",
		"--env", "WK_PLUGIN_SANDBOX_DIR=/var/lib/wukongim/plugin-sandbox",
		"--env", "WK_PLUGIN_STATE_DIR=/var/lib/wukongim/plugin-state",
		"--mount", "type=bind,src="+configPath+",dst=/etc/wukongim/wukongim.toml,readonly",
		"--mount", "type=volume,src="+volume+",dst=/var/lib/wukongim",
		"--tmpfs", "/run/wukongim:rw,noexec,nosuid,uid=10001,gid=10001,mode=0750,size=16m",
		image,
	)
	t.Cleanup(func() {
		removeDockerContractObject(root, "rm", "--force", container)
	})

	deadline := time.Now().Add(45 * time.Second)
	for {
		status := strings.TrimSpace(runDockerContract(t, root, "inspect", container, "--format", "{{.State.Health.Status}}"))
		if status == "healthy" {
			break
		}
		if status == "unhealthy" || time.Now().After(deadline) {
			logs := runDockerContract(t, root, "logs", "--tail", "100", container)
			t.Fatalf("container health = %q\n%s", status, logs)
		}
		time.Sleep(500 * time.Millisecond)
	}

	runDockerContract(t, root, "exec", container, "sh", "-c", strings.Join([]string{
		`test "$(id -u):$(id -g)" = "10001:10001"`,
		`test -r /etc/wukongim/wukongim.toml`,
		`test -w /var/lib/wukongim`,
		`test -w /run/wukongim`,
		`test -S /run/wukongim/plugin.sock`,
		`wget -q --spider -T 5 http://127.0.0.1:5001/readyz`,
	}, " && "))

	runDockerContract(t, root, "stop", "--time", "30", container)
	exitCode := strings.TrimSpace(runDockerContract(t, root, "inspect", container, "--format", "{{.State.ExitCode}}"))
	if exitCode != "0" {
		logs := runDockerContract(t, root, "logs", "--tail", "100", container)
		t.Fatalf("container exit code = %s, want 0\n%s", exitCode, logs)
	}
}

func runDockerContract(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("docker", args...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}

func removeDockerContractObject(dir string, args ...string) {
	command := exec.Command("docker", args...)
	command.Dir = dir
	_ = command.Run()
}
