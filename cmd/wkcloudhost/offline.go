package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	clouddeployinfra "github.com/WuKongIM/WuKongIM/internal/infra/clouddeploy"
	clouddeploy "github.com/WuKongIM/WuKongIM/internal/usecase/clouddeploy"
)

const maxOfflinePlanBytes = 1 << 20

type offlineInstallOptions struct {
	bundleRoot string
	planPath   string
	role       string
	rootPrefix string
	runtimeDir string
	dataDevice string
	noSystemd  bool
}

func addOfflineHostCommand(root *cobra.Command, stdout io.Writer) {
	var options offlineInstallOptions
	command := &cobra.Command{
		Use: "install-offline", Short: "Install the native four-host offline deployment payload", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			manifest, err := installOfflineHost(options)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(stdout, manifest.BundleDigest)
			return err
		},
	}
	command.Flags().StringVar(&options.bundleRoot, "bundle", "", "extracted verified offline bundle")
	command.Flags().StringVar(&options.planPath, "plan", "", "strict Deployment Plan JSON")
	command.Flags().StringVar(&options.role, "role", "", "service-1, service-2, service-3, or load")
	command.Flags().StringVar(&options.rootPrefix, "root-prefix", "/", "filesystem root, used only by tests")
	command.Flags().StringVar(&options.runtimeDir, "runtime-dir", "", "root-readable runtime credential directory")
	command.Flags().StringVar(&options.dataDevice, "data-device", "", "independent empty data disk block device")
	command.Flags().BoolVar(&options.noSystemd, "no-systemd", false, "render and install without systemd activation")
	for _, name := range []string{"bundle", "plan", "role", "runtime-dir"} {
		_ = command.MarkFlagRequired(name)
	}
	root.AddCommand(command)

	var activateRole string
	activate := &cobra.Command{
		Use: "activate-offline", Short: "Activate one prepared native deployment role", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if os.Geteuid() != 0 {
				return errors.New("wkcloudhost activate-offline requires root")
			}
			if !offlineRole(activateRole) {
				return clouddeploy.ErrInvalidDeployment
			}
			return activateOfflineUnits(activateRole)
		},
	}
	activate.Flags().StringVar(&activateRole, "role", "", "service-1, service-2, service-3, or load")
	_ = activate.MarkFlagRequired("role")
	root.AddCommand(activate)
}

func installOfflineHost(options offlineInstallOptions) (clouddeploy.Manifest, error) {
	if strings.TrimSpace(options.rootPrefix) == "" || strings.TrimSpace(options.runtimeDir) == "" {
		return clouddeploy.Manifest{}, clouddeploy.ErrInvalidDeployment
	}
	directory, err := clouddeployinfra.Open(options.bundleRoot)
	if err != nil {
		return clouddeploy.Manifest{}, err
	}
	manifest, err := clouddeploy.Verify(directory)
	if err != nil {
		return clouddeploy.Manifest{}, err
	}
	plan, err := readOfflinePlan(options.planPath)
	if err != nil || clouddeploy.ValidatePlan(plan, manifest, time.Now().UTC()) != nil {
		return clouddeploy.Manifest{}, clouddeploy.ErrInvalidDeployment
	}
	host, ok := offlineHost(plan, options.role)
	if !ok {
		return clouddeploy.Manifest{}, clouddeploy.ErrInvalidDeployment
	}
	if options.rootPrefix == "/" {
		if os.Geteuid() != 0 {
			return clouddeploy.Manifest{}, errors.New("wkcloudhost install-offline requires root")
		}
		if err := ensureServiceUser(); err != nil {
			return clouddeploy.Manifest{}, err
		}
		if options.dataDevice == "" {
			return clouddeploy.Manifest{}, errors.New("--data-device is required on a real host")
		}
		if err := prepareDataDisk(options.dataDevice); err != nil {
			return clouddeploy.Manifest{}, err
		}
	}
	for _, relative := range []string{
		"opt/wukongim/bin", "opt/wukongim/scripts", "opt/wukongim/assets", "etc/wukongim/secrets",
		"etc/systemd/system", "var/lib/wukongim-cloud", "var/lib/wukongim/textfile",
	} {
		if err := os.MkdirAll(rooted(options.rootPrefix, relative), 0o755); err != nil {
			return clouddeploy.Manifest{}, err
		}
	}
	templates, err := offlineTemplates(options.bundleRoot)
	if err != nil {
		return clouddeploy.Manifest{}, err
	}
	rendered, err := clouddeploy.RenderHostFiles(plan, options.role, templates)
	if err != nil {
		return clouddeploy.Manifest{}, err
	}
	for _, file := range rendered {
		if err := writeOfflineFile(rooted(options.rootPrefix, file.Path), file.Content, fs.FileMode(file.Mode)); err != nil {
			return clouddeploy.Manifest{}, err
		}
	}
	for _, binary := range offlineBinaries(options.role) {
		if err := copyRegular(filepath.Join(options.bundleRoot, "bin", binary), rooted(options.rootPrefix, "opt/wukongim/bin/"+binary), 0o755); err != nil {
			return clouddeploy.Manifest{}, err
		}
	}
	for _, script := range []string{"collect-evidence.sh", "collect-process-metrics.sh", "run-chat-lifecycle-stage.sh", "verify-base-tools.sh", "wait-coordinator-dependencies.sh"} {
		if err := copyRegular(filepath.Join(options.bundleRoot, "scripts", script), rooted(options.rootPrefix, "opt/wukongim/scripts/"+script), 0o755); err != nil {
			return clouddeploy.Manifest{}, err
		}
	}
	for _, unit := range offlineUnits(options.role) {
		if err := copyRegular(filepath.Join(options.bundleRoot, "systemd", unit), rooted(options.rootPrefix, "etc/systemd/system/"+unit), 0o644); err != nil {
			return clouddeploy.Manifest{}, err
		}
	}
	for _, secret := range offlineSecrets(options.role) {
		if err := copyRegular(filepath.Join(options.runtimeDir, secret), rooted(options.rootPrefix, "etc/wukongim/secrets/"+secret), 0o600); err != nil {
			return clouddeploy.Manifest{}, err
		}
	}
	if options.role == "load" {
		for _, asset := range []string{"manager", "demo"} {
			if err := copyOfflineTree(filepath.Join(options.bundleRoot, "assets", asset), rooted(options.rootPrefix, "opt/wukongim/assets/"+asset)); err != nil {
				return clouddeploy.Manifest{}, err
			}
		}
	}
	marker := fmt.Sprintf("%s\n", host.DataDiskID)
	if err := writeOfflineFile(rooted(options.rootPrefix, "var/lib/wukongim-cloud/.wukongim-data-disk-id"), []byte(marker), 0o640); err != nil {
		return clouddeploy.Manifest{}, err
	}
	metric := fmt.Sprintf("wukongim_cloud_bundle_info{role=%q,digest=%q,source_sha=%q} 1\n", options.role, manifest.BundleDigest, manifest.SourceSHA)
	if err := writeOfflineFile(rooted(options.rootPrefix, "var/lib/wukongim/textfile/bundle.prom"), []byte(metric), 0o644); err != nil {
		return clouddeploy.Manifest{}, err
	}
	if options.rootPrefix == "/" {
		if err := runCommand("chown", "-R", "root:root", "/opt/wukongim", "/etc/wukongim"); err != nil {
			return clouddeploy.Manifest{}, err
		}
		if err := runCommand("chown", "-R", "wukongim:wukongim", "/var/lib/wukongim-cloud", "/var/lib/wukongim"); err != nil {
			return clouddeploy.Manifest{}, err
		}
		for _, path := range []string{"/etc/wukongim/wukongim.toml", "/etc/wukongim/prometheus.yml", "/etc/wukongim/Caddyfile", "/etc/wukongim/chat-lifecycle.yaml", "/etc/wukongim/chat-lifecycle-rehearsal.yaml", "/etc/wukongim/analysis-scenario.yaml"} {
			if _, statErr := os.Stat(path); statErr == nil {
				if err := runCommand("chown", "root:wukongim", path); err != nil {
					return clouddeploy.Manifest{}, err
				}
			}
		}
	}
	if !options.noSystemd {
		if options.rootPrefix != "/" {
			return clouddeploy.Manifest{}, errors.New("--no-systemd is required with --root-prefix")
		}
		if err := activateOfflineUnits(options.role); err != nil {
			return clouddeploy.Manifest{}, err
		}
	}
	return manifest, nil
}

func readOfflinePlan(path string) (clouddeploy.DeploymentPlan, error) {
	file, err := os.Open(path)
	if err != nil {
		return clouddeploy.DeploymentPlan{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() <= 0 || info.Size() > maxOfflinePlanBytes {
		return clouddeploy.DeploymentPlan{}, clouddeploy.ErrInvalidDeployment
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxOfflinePlanBytes+1))
	decoder.DisallowUnknownFields()
	var plan clouddeploy.DeploymentPlan
	if err := decoder.Decode(&plan); err != nil {
		return clouddeploy.DeploymentPlan{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return clouddeploy.DeploymentPlan{}, clouddeploy.ErrInvalidDeployment
	}
	return plan, nil
}

func offlineTemplates(bundleRoot string) (map[string]string, error) {
	paths := map[string]string{
		"wukongim.toml": "config/wukongim.toml.tmpl", "prometheus.yml": "config/prometheus.yml.tmpl",
		"Caddyfile": "config/Caddyfile.tmpl", "chat-lifecycle.yaml": "config/chat-lifecycle.yaml",
		"chat-lifecycle-rehearsal.yaml": "config/chat-lifecycle-rehearsal.yaml",
	}
	result := make(map[string]string, len(paths))
	for name, relative := range paths {
		data, err := os.ReadFile(filepath.Join(bundleRoot, relative))
		if err != nil {
			return nil, err
		}
		result[name] = string(data)
	}
	return result, nil
}

func offlineHost(plan clouddeploy.DeploymentPlan, role string) (clouddeploy.HostPlan, bool) {
	for _, host := range plan.Hosts {
		if host.Role == role {
			return host, true
		}
	}
	return clouddeploy.HostPlan{}, false
}

func offlineRole(role string) bool {
	return role == "service-1" || role == "service-2" || role == "service-3" || role == "load"
}

func offlineBinaries(role string) []string {
	if strings.HasPrefix(role, "service-") {
		return []string{"wukongim", "wkbench", "node_exporter", "wkcloudbundle", "wkcloudhost"}
	}
	return []string{"caddy", "node_exporter", "prometheus", "wkanalysis", "wkbench", "wkcloudbundle", "wkcloudgate", "wkcloudhost"}
}

func offlineUnits(role string) []string {
	common := []string{"node-exporter.service", "wukongim-process-metrics.service", "wukongim-evidence.service", "wukongim-evidence.timer"}
	if strings.HasPrefix(role, "service-") {
		return append([]string{"wukongim.service", "wkbench-host-metrics.service"}, common...)
	}
	return append([]string{"wkbench-host-metrics.service", "wkbench-worker@.service", "wkbench-coordinator.service", "wkbench-formal.service", "wkbench-rehearsal.service", "prometheus.service", "wkanalysis.service", "caddy.service"}, common...)
}

func offlineSecrets(role string) []string {
	if strings.HasPrefix(role, "service-") {
		return []string{"node.env"}
	}
	return []string{"load.env", "analysis.env", "analysis-cert.pem", "analysis-key.pem"}
}

func writeOfflineFile(path string, content []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err == nil && !info.Mode().IsRegular() {
		return clouddeploy.ErrInvalidDeployment
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(content)
	closeErr := file.Close()
	if writeErr == nil {
		writeErr = os.Chmod(path, mode)
	}
	return errors.Join(writeErr, closeErr)
}

func copyOfflineTree(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil || relative == "." {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return clouddeploy.ErrInvalidBundle
		}
		if entry.IsDir() {
			return os.MkdirAll(filepath.Join(destination, relative), 0o755)
		}
		return copyRegular(path, filepath.Join(destination, relative), 0o644)
	})
}

func activateOfflineUnits(role string) error {
	if err := runCommand("systemctl", "daemon-reload"); err != nil {
		return err
	}
	units := offlineUnits(role)
	if role == "load" {
		units = make([]string, 0, len(offlineUnits(role))+2)
		for _, unit := range offlineUnits(role) {
			if unit != "wkbench-worker@.service" && unit != "wkbench-coordinator.service" && unit != "wkbench-formal.service" && unit != "wkbench-rehearsal.service" {
				units = append(units, unit)
			}
		}
		units = append(units, "wkbench-worker@1.service", "wkbench-worker@2.service", "wkbench-worker@3.service")
	}
	args := append([]string{"enable", "--now"}, units...)
	return runCommand("systemctl", args...)
}
