package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCloudSimulationSSHConfigPinsJumpAndTargetIdentity(t *testing.T) {
	_, current, _, _ := runtime.Caller(0)
	root := filepath.Dir(filepath.Dir(current))
	temporary := t.TempDir()
	keyPath := filepath.Join(temporary, "bootstrap identity")
	configPath := filepath.Join(temporary, "ssh-config")
	if err := os.WriteFile(keyPath, []byte("test-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	canonicalKeyPath, err := filepath.EvalSymlinks(keyPath)
	if err != nil {
		t.Fatal(err)
	}

	command := exec.CommandContext(t.Context(), "bash", filepath.Join(root, "scripts/cloud-sim/write-ssh-config.sh"))
	command.Env = append(os.Environ(),
		"WK_CLOUD_SIM_PUBLIC_IP=203.0.113.20",
		"WK_CLOUD_NODE1_IP=10.42.0.11",
		"WK_CLOUD_NODE2_IP=10.42.0.12",
		"WK_CLOUD_NODE3_IP=10.42.0.13",
		"WK_CLOUD_SSH_KEY="+keyPath,
		"WK_CLOUD_SSH_CONFIG="+configPath,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("write SSH config: %v\n%s", err, output)
	}
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"Host wukong-sim-jump",
		"HostName 203.0.113.20",
		`IdentityFile "` + canonicalKeyPath + `"`,
		"IdentitiesOnly yes",
		"Host 10.42.0.11 10.42.0.12 10.42.0.13",
		"ProxyJump wukong-sim-jump",
	} {
		if !strings.Contains(string(content), fragment) {
			t.Fatalf("SSH config missing %q:\n%s", fragment, content)
		}
	}
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("SSH config mode = %o, want 600", got)
	}

	for _, test := range []struct {
		host      string
		hostname  string
		proxyJump string
	}{
		{host: "wukong-sim-jump", hostname: "203.0.113.20"},
		{host: "10.42.0.11", hostname: "10.42.0.11", proxyJump: "wukong-sim-jump"},
	} {
		resolved := exec.CommandContext(t.Context(), "ssh", "-G", "-F", configPath, test.host)
		output, err := resolved.CombinedOutput()
		if err != nil {
			t.Fatalf("resolve SSH config for %s: %v\n%s", test.host, err, output)
		}
		for _, setting := range []string{
			"hostname " + test.hostname,
			"user wukong",
			"identityfile " + canonicalKeyPath,
			"identitiesonly yes",
		} {
			if !strings.Contains(string(output), setting+"\n") {
				t.Fatalf("resolved SSH config for %s missing %q:\n%s", test.host, setting, output)
			}
		}
		if test.proxyJump != "" && !strings.Contains(string(output), "proxyjump "+test.proxyJump+"\n") {
			t.Fatalf("resolved SSH config for %s missing proxy jump %q:\n%s", test.host, test.proxyJump, output)
		}
	}
}

func TestCloudSimulationSSHConfigRejectsConfigInjection(t *testing.T) {
	_, current, _, _ := runtime.Caller(0)
	root := filepath.Dir(filepath.Dir(current))
	temporary := t.TempDir()
	keyPath := filepath.Join(temporary, "key")
	if err := os.WriteFile(keyPath, []byte("test-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), "bash", filepath.Join(root, "scripts/cloud-sim/write-ssh-config.sh"))
	command.Env = append(os.Environ(),
		"WK_CLOUD_SIM_PUBLIC_IP=203.0.113.20\nProxyCommand evil",
		"WK_CLOUD_NODE1_IP=10.42.0.11",
		"WK_CLOUD_NODE2_IP=10.42.0.12",
		"WK_CLOUD_NODE3_IP=10.42.0.13",
		"WK_CLOUD_SSH_KEY="+keyPath,
		"WK_CLOUD_SSH_CONFIG="+filepath.Join(temporary, "ssh-config"),
	)
	if err := command.Run(); err == nil {
		t.Fatal("SSH config accepted a non-IPv4 host value")
	}
}
