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
	for _, path := range []string{
		"opt/wukongim/bin/wukongim-cgroup-metrics",
		"etc/systemd/system/wukongim-cgroup-metrics.service",
	} {
		if _, err := os.Stat(filepath.Join(root, path)); err != nil {
			t.Fatalf("node cgroup evidence file %s not installed: %v", path, err)
		}
	}
	if _, err := os.Stat(authorized); !os.IsNotExist(err) {
		t.Fatalf("authorized key still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "etc/systemd/system/wkbench-run.service")); !os.IsNotExist(err) {
		t.Fatalf("sim unit installed on node: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "opt/wukongim/bin/wkcloudview")); !os.IsNotExist(err) {
		t.Fatalf("Cloud View binary installed on node: %v", err)
	}
}

func TestInstallSimulatorEnablesCloudViewOnlyWhenRequested(t *testing.T) {
	bundle := buildTestBundle(t)
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			envDir := t.TempDir()
			for _, file := range []string{"sim.env", "analysis.env", "mcp-cert.pem", "mcp-key.pem", "mcp-ca.pem"} {
				if err := os.WriteFile(filepath.Join(envDir, file), []byte("test"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			root := t.TempDir()
			_, err := installBundle(installOptions{
				bundleRoot: bundle, role: "sim", rootPrefix: root, envDir: envDir,
				publicObservation: enabled, authorizedKeysPath: "", noSystemd: true,
			})
			if err != nil {
				t.Fatalf("installBundle() error = %v", err)
			}
			_, unitErr := os.Stat(filepath.Join(root, "etc/systemd/system/wkcloudview.service"))
			_, configErr := os.Stat(filepath.Join(root, "etc/wukongim/cloud-view.json"))
			if enabled && (unitErr != nil || configErr != nil) {
				t.Fatalf("Cloud View files missing: unit=%v config=%v", unitErr, configErr)
			}
			if !enabled && (!os.IsNotExist(unitErr) || !os.IsNotExist(configErr)) {
				t.Fatalf("Cloud View files installed while disabled: unit=%v config=%v", unitErr, configErr)
			}
			if _, err := os.Stat(filepath.Join(root, "etc/systemd/system/wukongim-cgroup-metrics.service")); !os.IsNotExist(err) {
				t.Fatalf("node-only cgroup metrics service installed on simulator: %v", err)
			}
			for _, name := range []string{"scenario.yaml", "effective-node-runtime-contract.json"} {
				source, err := os.ReadFile(filepath.Join(bundle, "config", name))
				if err != nil {
					t.Fatalf("read bundled %s: %v", name, err)
				}
				destination := filepath.Join(root, "etc/wukongim", name)
				installed, err := os.ReadFile(destination)
				if err != nil {
					t.Fatalf("simulator contract file %s not installed: %v", name, err)
				}
				if string(installed) != string(source) {
					t.Fatalf("installed %s differs from sealed bundle", name)
				}
				info, err := os.Stat(destination)
				if err != nil {
					t.Fatal(err)
				}
				if info.Mode().Perm() != 0o640 {
					t.Fatalf("installed %s mode = %o, want 640", name, info.Mode().Perm())
				}
			}
		})
	}
}

func buildTestBundle(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"wukongim", "wkbench", "wkanalysis", "wkcloudview", "prometheus", "node_exporter"} {
		if err := os.WriteFile(filepath.Join(root, "bin", name), []byte(name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	scenario := filepath.Join(t.TempDir(), "scenario.yaml")
	if err := os.WriteFile(scenario, []byte("version: wkbench/v1\nrun:\n  id: test\n  duration: 2h\n  report_dir: /tmp/reports\nobjectives:\n  scale: small\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	spec := deploy.BundleSpec{
		RunID: "run-1", SourceSHA: "0123456789012345678901234567890123456789", ScenarioPath: scenario,
		ScenarioDigest: "sha256:scenario", Duration: 2 * time.Hour,
		PrivateIPv4:         map[string]string{"node-1": "10.0.0.11", "node-2": "10.0.0.12", "node-3": "10.0.0.13", "sim": "10.0.0.20"},
		SimulatorSourceIPv4: []string{"10.0.0.20", "10.0.0.21", "10.0.0.22"},
		PublicViewEnabled:   true,
	}
	if err := deploy.Render(root, spec); err != nil {
		t.Fatal(err)
	}
	if _, err := deploy.Seal(root, spec); err != nil {
		t.Fatal(err)
	}
	return root
}
