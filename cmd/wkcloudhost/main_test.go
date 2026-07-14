package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/infra/cloudsim/deploy"
)

func TestInstallBundleCopiesOnlyRoleFilesAndRevokesBootstrapKey(t *testing.T) {
	bundle := buildTestBundle(t)
	envDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(envDir, "node.env"), []byte("WK_MANAGER_JWT_SECRET=test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	authorized := filepath.Join(root, "home/wukong/.ssh/authorized_keys")
	if err := os.MkdirAll(filepath.Dir(authorized), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(authorized, []byte("ssh-ed25519 test"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := installBundle(installOptions{
		bundleRoot: bundle, role: "node-1", rootPrefix: root, envDir: envDir,
		authorizedKeysPath: "/home/wukong/.ssh/authorized_keys", noSystemd: true,
	})
	if err != nil {
		t.Fatalf("installBundle() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "etc/wukongim/wukongim.toml")); err != nil {
		t.Fatalf("node config not installed: %v", err)
	}
	if _, err := os.Stat(authorized); !os.IsNotExist(err) {
		t.Fatalf("authorized key still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "etc/systemd/system/wkbench-run.service")); !os.IsNotExist(err) {
		t.Fatalf("sim unit installed on node: %v", err)
	}
}

func buildTestBundle(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"wukongim", "wkbench", "wkanalysis", "prometheus", "node_exporter"} {
		if err := os.WriteFile(filepath.Join(root, "bin", name), []byte(name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	scenario := filepath.Join(t.TempDir(), "scenario.yaml")
	if err := os.WriteFile(scenario, []byte("version: wkbench/v1\nrun:\n  id: test\n  duration: 2h\n  report_dir: /tmp/reports\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	spec := deploy.BundleSpec{
		RunID: "run-1", SourceSHA: "0123456789012345678901234567890123456789", ScenarioPath: scenario,
		ScenarioDigest: "sha256:scenario", Duration: 2 * time.Hour,
		PrivateIPv4:         map[string]string{"node-1": "10.0.0.11", "node-2": "10.0.0.12", "node-3": "10.0.0.13", "sim": "10.0.0.20"},
		SimulatorSourceIPv4: []string{"10.0.0.20", "10.0.0.21", "10.0.0.22"},
	}
	if err := deploy.Render(root, spec); err != nil {
		t.Fatal(err)
	}
	if _, err := deploy.Seal(root, spec); err != nil {
		t.Fatal(err)
	}
	return root
}
