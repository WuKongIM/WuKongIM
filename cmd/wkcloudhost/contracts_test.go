package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/infra/cloudsim/deploy"
	clouddeploy "github.com/WuKongIM/WuKongIM/internal/usecase/clouddeploy"
)

func TestExecuteCommandContract(t *testing.T) {
	t.Run("help advertises both installation modes", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		if code := execute([]string{"--help"}, &stdout, &stderr); code != 0 {
			t.Fatalf("execute(--help) = %d, stderr = %q", code, stderr.String())
		}
		for _, command := range []string{"install", "install-offline", "activate-offline"} {
			if !strings.Contains(stdout.String(), command) {
				t.Fatalf("help omits %q:\n%s", command, stdout.String())
			}
		}
	})

	t.Run("required flags fail through the CLI boundary", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		if code := execute([]string{"install"}, &stdout, &stderr); code != 1 {
			t.Fatalf("execute(install) = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "required flag") {
			t.Fatalf("stderr = %q, want required flag error", stderr.String())
		}
		if stdout.Len() != 0 {
			t.Fatalf("stdout = %q, want empty", stdout.String())
		}
	})

	t.Run("successful install prints the verified digest", func(t *testing.T) {
		bundle := buildTestBundle(t)
		manifest, err := deploy.Verify(bundle)
		if err != nil {
			t.Fatal(err)
		}
		envDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(envDir, "node.env"), []byte("WK_MANAGER_JWT_SECRET=test\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		code := execute([]string{
			"install", "--bundle", bundle, "--role", "node-2", "--env-dir", envDir,
			"--root-prefix", t.TempDir(), "--no-systemd", "--authorized-keys", "",
		}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("execute(install) = %d, stderr = %q", code, stderr.String())
		}
		if got := strings.TrimSpace(stdout.String()); got != manifest.BundleDigest {
			t.Fatalf("stdout digest = %q, want %q", got, manifest.BundleDigest)
		}
	})

	t.Run("offline command forwards validation failures", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := execute([]string{
			"install-offline", "--bundle", filepath.Join(t.TempDir(), "missing"),
			"--plan", filepath.Join(t.TempDir(), "missing-plan.json"), "--role", "service-1",
			"--runtime-dir", t.TempDir(), "--root-prefix", t.TempDir(), "--no-systemd",
		}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("execute(install-offline) = %d, want 1", code)
		}
		if stderr.Len() == 0 || stdout.Len() != 0 {
			t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
		}
	})
}

func TestInstallBundleRejectsTamperingWithoutWriting(t *testing.T) {
	bundle := buildTestBundle(t)
	if err := os.WriteFile(filepath.Join(bundle, "bin", "wukongim"), []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}
	envDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(envDir, "node.env"), []byte("test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()

	_, err := installBundle(installOptions{
		bundleRoot: bundle, role: "node-1", rootPrefix: root, envDir: envDir, noSystemd: true,
	})
	if err == nil {
		t.Fatal("installBundle() accepted a tampered immutable bundle")
	}
	entries, readErr := os.ReadDir(root)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("installBundle() wrote %d entries before rejecting tampering", len(entries))
	}
}

func TestInstallBundleValidatesOptionsAndRootFilesystem(t *testing.T) {
	if _, err := installBundle(installOptions{role: "unknown", rootPrefix: t.TempDir(), envDir: t.TempDir(), noSystemd: true}); !errors.Is(err, deploy.ErrInvalidBundle) {
		t.Fatalf("installBundle(invalid role) error = %v", err)
	}

	bundle := buildTestBundle(t)
	rootFile := filepath.Join(t.TempDir(), "root-file")
	if err := os.WriteFile(rootFile, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installBundle(installOptions{
		bundleRoot: bundle, role: "node-1", rootPrefix: rootFile, envDir: t.TempDir(), noSystemd: true,
	}); err == nil {
		t.Fatal("installBundle() accepted a regular file as root prefix")
	}
}

func TestAppendFstabOnceIsIdempotent(t *testing.T) {
	entry := "UUID=test /var/lib/wukongim-cloud ext4 defaults,nofail,nodev,nosuid,noatime 0 2"
	path := filepath.Join(t.TempDir(), "fstab")
	if err := appendFstabOnce(path, entry); err != nil {
		t.Fatal(err)
	}
	if err := appendFstabOnce(path, entry); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), entry+"\n"; got != want {
		t.Fatalf("fstab = %q, want %q", got, want)
	}

	spacedPath := filepath.Join(t.TempDir(), "fstab")
	if err := os.WriteFile(spacedPath, []byte("  "+entry+"  \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := appendFstabOnce(spacedPath, entry); err != nil {
		t.Fatal(err)
	}
	spaced, err := os.ReadFile(spacedPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(spaced), "UUID=test") != 1 {
		t.Fatalf("existing entry duplicated: %q", spaced)
	}
}

func TestAppendFstabOncePropagatesFilesystemErrors(t *testing.T) {
	if err := appendFstabOnce(t.TempDir(), "entry"); err == nil {
		t.Fatal("appendFstabOnce(directory) succeeded")
	}
	path := filepath.Join(t.TempDir(), "missing", "fstab")
	if err := appendFstabOnce(path, "entry"); err == nil {
		t.Fatal("appendFstabOnce(missing parent) succeeded")
	}
}

func TestCopyRegularPreservesContentAndRejectsNonRegularSources(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.WriteFile(source, []byte("replacement"), 0o644); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "nested", "destination")
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("long stale content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyRegular(source, destination, 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "replacement" || info.Mode().Perm() != 0o600 {
		t.Fatalf("copied content = %q, mode = %o", data, info.Mode().Perm())
	}

	for name, makeSource := range map[string]func(string) error{
		"directory": func(path string) error { return os.Mkdir(path, 0o755) },
		"symlink":   func(path string) error { return os.Symlink(source, path) },
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), name)
			if err := makeSource(path); err != nil {
				t.Fatal(err)
			}
			if err := copyRegular(path, filepath.Join(t.TempDir(), "out"), 0o600); !errors.Is(err, deploy.ErrInvalidBundle) {
				t.Fatalf("copyRegular() error = %v, want invalid bundle", err)
			}
		})
	}

	if err := copyRegular(source, t.TempDir(), 0o600); err == nil {
		t.Fatal("copyRegular() overwrote a destination directory")
	}
	parentFile := filepath.Join(root, "parent-file")
	if err := os.WriteFile(parentFile, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyRegular(source, filepath.Join(parentFile, "child"), 0o600); err == nil {
		t.Fatal("copyRegular() succeeded below a regular file")
	}
}

func TestReadOfflinePlanIsStrict(t *testing.T) {
	validPath := filepath.Join(t.TempDir(), "plan.json")
	validJSON := `{"schema":"wkclouddeploy/v1","lease_id":"lease-1","hosts":[{"role":"load","data_disk_id":"disk-1"}]}`
	if err := os.WriteFile(validPath, []byte(validJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := readOfflinePlan(validPath)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Schema != "wkclouddeploy/v1" || plan.LeaseID != "lease-1" || len(plan.Hosts) != 1 || plan.Hosts[0].Role != "load" {
		t.Fatalf("decoded plan = %#v", plan)
	}

	for name, content := range map[string]string{
		"empty":         "",
		"malformed":     "{",
		"unknown field": `{"schema":"wkclouddeploy/v1","unexpected":true}`,
		"trailing value": `{"schema":"wkclouddeploy/v1"}
{"schema":"second"}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "plan.json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := readOfflinePlan(path); err == nil {
				t.Fatal("readOfflinePlan() accepted invalid input")
			}
		})
	}
	if _, err := readOfflinePlan(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("readOfflinePlan(missing) succeeded")
	}
}

func TestOfflineHostRoleContract(t *testing.T) {
	plan := clouddeploy.DeploymentPlan{Hosts: []clouddeploy.HostPlan{
		{Role: "service-2", InstanceID: "i-service-2", DataDiskID: "d-service-2"},
		{Role: "load", InstanceID: "i-load", DataDiskID: "d-load"},
	}}
	host, ok := offlineHost(plan, "load")
	if !ok || host.InstanceID != "i-load" || host.DataDiskID != "d-load" {
		t.Fatalf("offlineHost(load) = %#v, %v", host, ok)
	}
	if _, ok := offlineHost(plan, "service-3"); ok {
		t.Fatal("offlineHost(service-3) found an unplanned host")
	}
	for _, role := range []string{"service-1", "service-2", "service-3", "load"} {
		if !offlineRole(role) {
			t.Fatalf("offlineRole(%q) = false", role)
		}
	}
	for _, role := range []string{"", "service", "service-4", "sim"} {
		if offlineRole(role) {
			t.Fatalf("offlineRole(%q) = true", role)
		}
	}
}

func TestOfflineRolePayloadContract(t *testing.T) {
	serviceBinaries := []string{"wukongim", "wkbench", "node_exporter", "wkcloudbundle", "wkcloudhost"}
	loadSecrets := []string{"load.env", "analysis.env", "analysis-cert.pem", "analysis-key.pem"}
	if got := offlineBinaries("service-1"); !reflect.DeepEqual(got, serviceBinaries) {
		t.Fatalf("offlineBinaries(service-1) = %v", got)
	}
	if got := offlineSecrets("load"); !reflect.DeepEqual(got, loadSecrets) {
		t.Fatalf("offlineSecrets(load) = %v", got)
	}
	loadUnits := offlineUnits("load")
	for _, unit := range []string{"wkbench-worker@.service", "wkbench-coordinator.service", "prometheus.service", "caddy.service"} {
		if !containsString(loadUnits, unit) {
			t.Fatalf("offlineUnits(load) omits %q: %v", unit, loadUnits)
		}
	}
}

func TestInstallOfflineHostRejectsInvalidPlanAndRoleWithoutWriting(t *testing.T) {
	if _, err := installOfflineHost(offlineInstallOptions{}); !errors.Is(err, clouddeploy.ErrInvalidDeployment) {
		t.Fatalf("installOfflineHost(empty options) error = %v", err)
	}

	now := time.Now().UTC()
	bundle, manifest := buildOfflineTestBundle(t)
	runtimeDir := t.TempDir()

	t.Run("expired plan", func(t *testing.T) {
		past := now.Add(-200 * time.Hour)
		plan, err := clouddeploy.BuildPlan(offlineLease(past, manifest), manifest, past)
		if err != nil {
			t.Fatal(err)
		}
		planPath := filepath.Join(t.TempDir(), "plan.json")
		writeOfflineJSON(t, planPath, plan)
		root := t.TempDir()
		_, err = installOfflineHost(offlineInstallOptions{
			bundleRoot: bundle, planPath: planPath, role: "service-1", rootPrefix: root,
			runtimeDir: runtimeDir, noSystemd: true,
		})
		if !errors.Is(err, clouddeploy.ErrInvalidDeployment) {
			t.Fatalf("installOfflineHost(expired plan) error = %v", err)
		}
		assertEmptyDirectory(t, root)
	})

	t.Run("role absent from plan", func(t *testing.T) {
		plan, err := clouddeploy.BuildPlan(offlineLease(now, manifest), manifest, now)
		if err != nil {
			t.Fatal(err)
		}
		planPath := filepath.Join(t.TempDir(), "plan.json")
		writeOfflineJSON(t, planPath, plan)
		root := t.TempDir()
		_, err = installOfflineHost(offlineInstallOptions{
			bundleRoot: bundle, planPath: planPath, role: "service-4", rootPrefix: root,
			runtimeDir: runtimeDir, noSystemd: true,
		})
		if !errors.Is(err, clouddeploy.ErrInvalidDeployment) {
			t.Fatalf("installOfflineHost(absent role) error = %v", err)
		}
		assertEmptyDirectory(t, root)
	})
}

func TestOfflineTemplatesRequireCompleteBundle(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config", "wukongim.toml.tmpl"), []byte("template"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := offlineTemplates(root); err == nil {
		t.Fatal("offlineTemplates() accepted an incomplete bundle")
	}
}

func TestWriteOfflineFileTruncatesAndRejectsLinks(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "nested", "config")
	if err := writeOfflineFile(path, []byte("long initial content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeOfflineFile(path, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new" || info.Mode().Perm() != 0o600 {
		t.Fatalf("content = %q, mode = %o", data, info.Mode().Perm())
	}

	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("protected"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := writeOfflineFile(link, []byte("replacement"), 0o600); !errors.Is(err, clouddeploy.ErrInvalidDeployment) {
		t.Fatalf("writeOfflineFile(symlink) error = %v", err)
	}
	protected, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(protected) != "protected" {
		t.Fatalf("symlink target changed to %q", protected)
	}

	if err := writeOfflineFile(t.TempDir(), []byte("replacement"), 0o600); !errors.Is(err, clouddeploy.ErrInvalidDeployment) {
		t.Fatalf("writeOfflineFile(directory) error = %v", err)
	}
	parentFile := filepath.Join(root, "parent-file")
	if err := os.WriteFile(parentFile, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeOfflineFile(filepath.Join(parentFile, "child"), nil, 0o600); err == nil {
		t.Fatal("writeOfflineFile() succeeded below a regular file")
	}
}

func TestCopyOfflineTreeCopiesAssetsAndRejectsLinks(t *testing.T) {
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("index"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "nested", "asset.js"), []byte("asset"), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "assets")
	if err := copyOfflineTree(source, destination); err != nil {
		t.Fatal(err)
	}
	for relative, want := range map[string]string{"index.html": "index", "nested/asset.js": "asset"} {
		path := filepath.Join(destination, filepath.FromSlash(relative))
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != want || info.Mode().Perm() != 0o644 {
			t.Fatalf("%s content = %q, mode = %o", relative, data, info.Mode().Perm())
		}
	}

	linkedSource := t.TempDir()
	if err := os.WriteFile(filepath.Join(linkedSource, "target"), []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(linkedSource, "target"), filepath.Join(linkedSource, "link")); err != nil {
		t.Fatal(err)
	}
	if err := copyOfflineTree(linkedSource, filepath.Join(t.TempDir(), "out")); !errors.Is(err, clouddeploy.ErrInvalidBundle) {
		t.Fatalf("copyOfflineTree(symlink) error = %v", err)
	}
	if err := copyOfflineTree(filepath.Join(t.TempDir(), "missing"), filepath.Join(t.TempDir(), "out")); err == nil {
		t.Fatal("copyOfflineTree(missing) succeeded")
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func assertEmptyDirectory(t *testing.T, path string) {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("%s contains %d entries", path, len(entries))
	}
}
