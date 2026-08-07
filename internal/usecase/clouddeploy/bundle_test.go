package clouddeploy_test

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	clouddeployinfra "github.com/WuKongIM/WuKongIM/internal/infra/clouddeploy"
	clouddeploy "github.com/WuKongIM/WuKongIM/internal/usecase/clouddeploy"
)

const (
	testSourceSHA  = "0123456789012345678901234567890123456789"
	testControlSHA = "abcdefabcdefabcdefabcdefabcdefabcdefabcd"
	intentName     = "deployment-intent.json"
	manifestName   = "bundle-manifest.json"
	maxJSONBytes   = 1 << 20
)

func TestSealVerifyAndTamperOfflineBundle(t *testing.T) {
	root := prepareTestPayload(t)
	manifest, err := clouddeploy.Seal(openDirectory(t, root), testSourceSHA, testControlSHA)
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	if len(manifest.BundleDigest) != len("sha256:")+64 || !strings.HasPrefix(manifest.BundleDigest, "sha256:") || manifest.SourceSHA != testSourceSHA || manifest.ControlSHA != testControlSHA {
		t.Fatalf("manifest = %#v", manifest)
	}
	verified, err := clouddeploy.Verify(openDirectory(t, root))
	if err != nil || verified.BundleDigest != manifest.BundleDigest {
		t.Fatalf("Verify() = %#v, %v", verified, err)
	}
	if err := os.WriteFile(filepath.Join(root, "config", "Caddyfile.tmpl"), []byte("tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := clouddeploy.Verify(openDirectory(t, root)); !errors.Is(err, clouddeploy.ErrInvalidBundle) {
		t.Fatalf("Verify(tampered) error = %v", err)
	}
}

func TestSealRejectsUnsupportedArchitectureAndContainerDependency(t *testing.T) {
	for name, mutate := range map[string]func(*testing.T, string){
		"arm binary": func(t *testing.T, root string) {
			writeELF(t, filepath.Join(root, "bin", "wukongim"), 40)
		},
		"container config": func(t *testing.T, root string) {
			if err := os.MkdirAll(filepath.Join(root, "config"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "config", "extra.conf"), []byte("use Docker here\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"secret material": func(t *testing.T, root string) {
			path := filepath.Join(root, "secrets", "deployment-key.pem")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("not-a-real-key\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"root Dockerfile": func(t *testing.T, root string) {
			if err := os.WriteFile(filepath.Join(root, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"root compose file": func(t *testing.T, root string) {
			if err := os.WriteFile(filepath.Join(root, "compose.yaml"), []byte("services: {}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"extra docker binary": func(t *testing.T, root string) {
			writeELF(t, filepath.Join(root, "bin", "docker"), 62)
		},
		"nested allowed binary name": func(t *testing.T, root string) {
			writeELF(t, filepath.Join(root, "bin", "hidden", "wukongim"), 62)
		},
		"root docker launcher": func(t *testing.T, root string) {
			if err := os.WriteFile(filepath.Join(root, "launch-docker.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		},
		"unexpected root launcher": func(t *testing.T, root string) {
			if err := os.WriteFile(filepath.Join(root, "launch.sh"), []byte("#!/bin/sh\ndocker run forbidden\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := prepareTestPayload(t)
			mutate(t, root)
			if _, err := clouddeploy.Seal(openDirectory(t, root), testSourceSHA, testControlSHA); !errors.Is(err, clouddeploy.ErrInvalidBundle) {
				t.Fatalf("Seal() error = %v, want ErrInvalidBundle", err)
			}
		})
	}
}

func TestSealReservesManifestWithinFileLimit(t *testing.T) {
	root := prepareTestPayload(t)
	directory := &fillToLimitDirectory{Directory: openDirectory(t, root)}
	if _, err := clouddeploy.Seal(directory, testSourceSHA, testControlSHA); !errors.Is(err, clouddeploy.ErrInvalidBundle) {
		t.Fatalf("Seal(manifest boundary) error = %v", err)
	}
}

func TestDefaultIntentPinsUbuntuTopologyAndRootSecrets(t *testing.T) {
	intent := clouddeploy.DefaultIntent(testSourceSHA, testControlSHA)
	if intent.OperatingSystem != "ubuntu" || intent.OperatingSystemVersion != "24.04" || intent.Architecture != "amd64" ||
		intent.ServiceNodes != 3 || intent.LoadNodes != 1 || intent.HashSlots != 256 || intent.WorkloadSlotGroups != 12 || intent.Replicas != 3 {
		t.Fatalf("intent = %#v", intent)
	}
	for _, secret := range intent.SecretFiles {
		if secret.Owner != "root" || secret.Mode != 0o600 || !strings.HasPrefix(secret.Path, "/etc/wukongim/secrets/") {
			t.Fatalf("secret = %#v", secret)
		}
	}
}

func TestSealRendersNativeTwelveGroupTemplates(t *testing.T) {
	root := prepareTestPayload(t)
	if _, err := clouddeploy.Seal(openDirectory(t, root), testSourceSHA, testControlSHA); err != nil {
		t.Fatal(err)
	}
	read := func(path string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	nodeConfig := read("config/wukongim.toml.tmpl")
	if !strings.Contains(nodeConfig, "initial_slot_count = 12") || !strings.Contains(nodeConfig, "hash_slot_count = 256") ||
		!strings.Contains(nodeConfig, "slot_replica_n = 3") || !strings.Contains(nodeConfig, "channel_replica_n = 3") ||
		!strings.Contains(nodeConfig, `external_ws_addr = "ws://{{PUBLIC_HTTP_HOST}}"`) {
		t.Fatalf("node template = %s", nodeConfig)
	}
	if !strings.Contains(read("systemd/caddy.service"), "AmbientCapabilities=CAP_NET_BIND_SERVICE") {
		t.Fatal("Caddy cannot bind the public HTTP port as the service user")
	}
	if strings.Contains(read("systemd/node-exporter.service"), "EnvironmentFile=") {
		t.Fatal("node exporter unexpectedly depends on a role-specific secret file")
	}
	coordinator := read("systemd/wkbench-coordinator.service")
	if !strings.Contains(coordinator, "ExecStart=/opt/wukongim/bin/wkbench soak chat-lifecycle ") {
		t.Fatalf("coordinator does not use the registered wkbench command hierarchy: %s", coordinator)
	}
	prometheusConfig := read("config/prometheus.yml.tmpl")
	prometheusUnit := read("systemd/prometheus.service")
	if !strings.Contains(prometheusConfig, "scrape_interval: 15s") ||
		!strings.Contains(prometheusUnit, "--storage.tsdb.retention.time=96h") ||
		!strings.Contains(prometheusUnit, "--storage.tsdb.retention.size=150GB") {
		t.Fatalf("Prometheus contract = %s\n%s", prometheusConfig, prometheusUnit)
	}
	caddy := read("config/Caddyfile.tmpl")
	if strings.Count(caddy, "basic_auth {") != 3 ||
		!strings.Contains(caddy, "{{DEMO_API_UPSTREAMS}}") || !strings.Contains(caddy, "{{DEMO_WS_UPSTREAMS}}") ||
		!strings.Contains(caddy, "{{MANAGER_UPSTREAMS}}") || strings.Contains(caddy, "{{MANAGER_UPSTREAM}}") {
		t.Fatalf("Demo routing/auth contract = %s", caddy)
	}
	for _, demoPath := range []string{"/route", "/user/*", "/channel/*", "/message/*", "/conversation/*", "/conversations/*", "/streammessage/*"} {
		if !strings.Contains(caddy, demoPath) {
			t.Fatalf("Demo routing omits client path %s: %s", demoPath, caddy)
		}
	}
	if strings.Count(caddy, "health_uri /readyz") != 3 || strings.Count(caddy, "health_port 5001") != 2 ||
		strings.Count(caddy, "lb_retry_match {") != 2 || strings.Count(caddy, "method GET") != 2 {
		t.Fatalf("proxy health and safe retry contract = %s", caddy)
	}
	websocketBlock := caddy[strings.Index(caddy, "handle @demo_websocket"):strings.Index(caddy, "@demo_api path")]
	if strings.Contains(websocketBlock, "lb_try_duration") || strings.Contains(websocketBlock, "lb_retry_match") {
		t.Fatalf("WebSocket proxy may replay a connection: %s", websocketBlock)
	}
	analysisUnit := read("systemd/wkanalysis.service")
	if !strings.Contains(analysisUnit, "LoadCredential=analysis-cert.pem:/etc/wukongim/secrets/analysis-cert.pem") ||
		!strings.Contains(analysisUnit, "LoadCredential=analysis-key.pem:/etc/wukongim/secrets/analysis-key.pem") ||
		!strings.Contains(analysisUnit, "WK_ANALYSIS_TLS_KEY_FILE=%d/analysis-key.pem") {
		t.Fatalf("analysis TLS credential contract = %s", analysisUnit)
	}
	processScript := read("scripts/collect-process-metrics.sh")
	processUnit := read("systemd/wukongim-process-metrics.service")
	for _, process := range []string{"wukongim.service", "wkbench-worker@1.service", "wkbench-worker@2.service", "wkbench-worker@3.service", "wkbench-coordinator.service", "prometheus.service", "caddy.service", "wkanalysis.service", "wukongim-process-metrics.service"} {
		if !strings.Contains(processScript, process) {
			t.Fatalf("process collector omits %s", process)
		}
	}
	if !strings.Contains(processScript, "wukongim_process_resident_memory_bytes") ||
		!strings.Contains(processUnit, "Restart=no") {
		t.Fatalf("process observation contract = %s\n%s", processScript, processUnit)
	}
}

func TestSealAndVerifyRejectSymlinksWithoutFollowingThem(t *testing.T) {
	root := prepareTestPayload(t)
	directory := openDirectory(t, root)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("untouched\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, intentName)); err != nil {
		t.Fatal(err)
	}
	if _, err := clouddeploy.Seal(directory, testSourceSHA, testControlSHA); !errors.Is(err, clouddeploy.ErrInvalidBundle) {
		t.Fatalf("Seal(symlink) error = %v", err)
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "untouched\n" {
		t.Fatalf("outside file changed: %q, %v", data, err)
	}

	root = prepareTestPayload(t)
	directory = openDirectory(t, root)
	if _, err := clouddeploy.Seal(directory, testSourceSHA, testControlSHA); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(root, manifestName)
	if err := os.Remove(manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := clouddeploy.Verify(directory); !errors.Is(err, clouddeploy.ErrInvalidBundle) {
		t.Fatalf("Verify(symlink manifest) error = %v", err)
	}
}

func TestSealNormalizesAndVerifyEnforcesScaffoldModes(t *testing.T) {
	root := prepareTestPayload(t)
	path := filepath.Join(root, "config", "Caddyfile.tmpl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("old\n"), 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := clouddeploy.Seal(openDirectory(t, root), testSourceSHA, testControlSHA); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("sealed mode = %v, %v", info, err)
	}
	if err := os.Chmod(filepath.Join(root, "scripts", "verify-base-tools.sh"), 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := clouddeploy.Verify(openDirectory(t, root)); !errors.Is(err, clouddeploy.ErrInvalidBundle) {
		t.Fatalf("Verify(unsafe mode) error = %v", err)
	}
}

func TestVerifyRejectsOversizedManifestTail(t *testing.T) {
	root := prepareTestPayload(t)
	if _, err := clouddeploy.Seal(openDirectory(t, root), testSourceSHA, testControlSHA); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(root, manifestName)
	file, err := os.OpenFile(manifest, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.Write(append(make([]byte, maxJSONBytes), 'x'))
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		t.Fatal(err)
	}
	if _, err := clouddeploy.Verify(openDirectory(t, root)); !errors.Is(err, clouddeploy.ErrInvalidBundle) {
		t.Fatalf("Verify(oversized manifest) error = %v", err)
	}
}

func prepareTestPayload(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	intent := clouddeploy.DefaultIntent(testSourceSHA, testControlSHA)
	for _, name := range intent.OfflineBinaries {
		writeELF(t, filepath.Join(root, "bin", name), 62)
	}
	for _, path := range []string{"assets/manager/index.html", "assets/demo/index.html"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, path), []byte("<!doctype html>\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func openDirectory(t *testing.T, root string) *clouddeployinfra.Directory {
	t.Helper()
	directory, err := clouddeployinfra.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	return directory
}

type fillToLimitDirectory struct {
	clouddeploy.Directory
}

func (d *fillToLimitDirectory) Files(maxFiles int) ([]clouddeploy.FileRecord, error) {
	records, err := d.Directory.Files(maxFiles)
	if err != nil {
		return nil, err
	}
	for index := len(records); index < maxFiles; index++ {
		records = append(records, clouddeploy.FileRecord{
			Path: fmt.Sprintf("assets/demo/filler-%04d.js", index), Mode: 0o644, Size: 1,
			SHA256: strings.Repeat("0", 64),
		})
	}
	return records, nil
}

func writeELF(t *testing.T, path string, machine uint16) {
	t.Helper()
	header := make([]byte, 64)
	copy(header, []byte("\x7fELF"))
	header[4], header[5], header[6], header[7] = 2, 1, 1, 0
	binary.LittleEndian.PutUint16(header[16:18], 2)
	binary.LittleEndian.PutUint16(header[18:20], machine)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, header, 0o755); err != nil {
		t.Fatal(err)
	}
}
