// Command wkcloudhost verifies and installs one immutable bundle on a cloud host.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/WuKongIM/WuKongIM/internal/infra/cloudsim/deploy"
)

type installOptions struct {
	bundleRoot         string
	role               string
	rootPrefix         string
	envDir             string
	dataDevice         string
	authorizedKeysPath string
	// publicObservation installs and starts simulator-only public Cloud View assets.
	publicObservation bool
	noSystemd         bool
}

func main() { os.Exit(execute(os.Args[1:], os.Stdout, os.Stderr)) }

func execute(args []string, stdout, stderr io.Writer) int {
	root := newRootCommand(stdout, stderr)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func newRootCommand(stdout, stderr io.Writer) *cobra.Command {
	var options installOptions
	root := &cobra.Command{Use: "wkcloudhost", Short: "Install a verified cloud simulation bundle", SilenceUsage: true, SilenceErrors: true}
	root.SetOut(stdout)
	root.SetErr(stderr)
	install := &cobra.Command{Use: "install", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		manifest, err := installBundle(options)
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, manifest.BundleDigest)
		return nil
	}}
	install.Flags().StringVar(&options.bundleRoot, "bundle", "", "extracted bundle root")
	install.Flags().StringVar(&options.role, "role", "", "node-1, node-2, node-3, or sim")
	install.Flags().StringVar(&options.rootPrefix, "root-prefix", "/", "filesystem root, used only by tests")
	install.Flags().StringVar(&options.envDir, "env-dir", "", "root-readable run capability and TLS files")
	install.Flags().StringVar(&options.dataDevice, "data-device", "", "independent empty data disk block device")
	install.Flags().StringVar(&options.authorizedKeysPath, "authorized-keys", "/home/wukong/.ssh/authorized_keys", "ephemeral deployment authorized_keys path")
	install.Flags().BoolVar(&options.publicObservation, "public-observation", false, "install and start the run-scoped public Cloud View")
	install.Flags().BoolVar(&options.noSystemd, "no-systemd", false, "render only; reserved for tests")
	_ = install.MarkFlagRequired("bundle")
	_ = install.MarkFlagRequired("role")
	_ = install.MarkFlagRequired("env-dir")
	root.AddCommand(install)
	return root
}

func installBundle(options installOptions) (deploy.Manifest, error) {
	if !validRole(options.role) || strings.TrimSpace(options.envDir) == "" || strings.TrimSpace(options.rootPrefix) == "" {
		return deploy.Manifest{}, deploy.ErrInvalidBundle
	}
	manifest, err := deploy.Verify(options.bundleRoot)
	if err != nil {
		return deploy.Manifest{}, err
	}
	if options.rootPrefix == "/" && os.Geteuid() != 0 {
		return deploy.Manifest{}, errors.New("wkcloudhost install requires root")
	}
	if options.rootPrefix == "/" {
		if err := ensureServiceUser(); err != nil {
			return deploy.Manifest{}, err
		}
		if options.dataDevice == "" {
			return deploy.Manifest{}, errors.New("--data-device is required on a real host")
		}
		if err := prepareDataDisk(options.dataDevice); err != nil {
			return deploy.Manifest{}, err
		}
	}
	paths := []string{"opt/wukongim/bin", "etc/wukongim", "etc/wukongim/tls", "etc/systemd/system", "var/lib/wukongim-cloud", "var/lib/wukongim/textfile"}
	for _, relative := range paths {
		if err := os.MkdirAll(rooted(options.rootPrefix, relative), 0o755); err != nil {
			return deploy.Manifest{}, err
		}
	}
	binaries := []string{"node_exporter"}
	if strings.HasPrefix(options.role, "node-") {
		binaries = append(binaries, "wukongim")
	} else {
		binaries = append(binaries, "wkbench", "wkanalysis", "prometheus")
		if options.publicObservation {
			binaries = append(binaries, "wkcloudview")
		}
	}
	for _, binary := range binaries {
		if err := copyRegular(filepath.Join(options.bundleRoot, "bin", binary), rooted(options.rootPrefix, "opt/wukongim/bin/"+binary), 0o755); err != nil {
			return deploy.Manifest{}, err
		}
	}
	if strings.HasPrefix(options.role, "node-") {
		if err := copyRegular(filepath.Join(options.bundleRoot, "config", options.role+".toml"), rooted(options.rootPrefix, "etc/wukongim/wukongim.toml"), 0o640); err != nil {
			return deploy.Manifest{}, err
		}
		if err := copyRegular(filepath.Join(options.envDir, "node.env"), rooted(options.rootPrefix, "etc/wukongim/node.env"), 0o600); err != nil {
			return deploy.Manifest{}, err
		}
	} else {
		for _, config := range []string{"target.yaml", "workers.yaml", "scenario.yaml", "prometheus.yml"} {
			if err := copyRegular(filepath.Join(options.bundleRoot, "config", config), rooted(options.rootPrefix, "etc/wukongim/"+config), 0o640); err != nil {
				return deploy.Manifest{}, err
			}
		}
		if options.publicObservation {
			if err := copyRegular(filepath.Join(options.bundleRoot, "config", "cloud-view.json"), rooted(options.rootPrefix, "etc/wukongim/cloud-view.json"), 0o640); err != nil {
				return deploy.Manifest{}, err
			}
		}
		for _, env := range []string{"sim.env", "analysis.env"} {
			if err := copyRegular(filepath.Join(options.envDir, env), rooted(options.rootPrefix, "etc/wukongim/"+env), 0o600); err != nil {
				return deploy.Manifest{}, err
			}
		}
		for _, certificate := range []string{"mcp-cert.pem", "mcp-key.pem", "mcp-ca.pem"} {
			if err := copyRegular(filepath.Join(options.envDir, certificate), rooted(options.rootPrefix, "etc/wukongim/tls/"+certificate), 0o600); err != nil {
				return deploy.Manifest{}, err
			}
		}
	}
	for unit, content := range selectedUnits(options.bundleRoot, options.role, options.publicObservation) {
		if err := copyRegular(content, rooted(options.rootPrefix, "etc/systemd/system/"+unit+".service"), 0o644); err != nil {
			return deploy.Manifest{}, err
		}
	}
	metric := fmt.Sprintf("wukongim_cloud_bundle_info{role=%q,digest=%q,source_sha=%q} 1\n", options.role, manifest.BundleDigest, manifest.SourceSHA)
	if err := os.WriteFile(rooted(options.rootPrefix, "var/lib/wukongim/textfile/bundle.prom"), []byte(metric), 0o644); err != nil {
		return deploy.Manifest{}, err
	}
	if options.rootPrefix == "/" {
		if err := runCommand("chown", "-R", "wukongim:wukongim", "/opt/wukongim", "/etc/wukongim", "/var/lib/wukongim-cloud", "/var/lib/wukongim"); err != nil {
			return deploy.Manifest{}, err
		}
	}
	if !options.noSystemd {
		if options.rootPrefix != "/" {
			return deploy.Manifest{}, errors.New("--no-systemd is required with --root-prefix")
		}
		if err := startRoleServices(options.role, options.publicObservation); err != nil {
			return deploy.Manifest{}, err
		}
	}
	if options.authorizedKeysPath != "" {
		if err := os.Remove(rooted(options.rootPrefix, strings.TrimPrefix(options.authorizedKeysPath, "/"))); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return deploy.Manifest{}, err
		}
	}
	return manifest, nil
}

func selectedUnits(bundleRoot, role string, publicObservation bool) map[string]string {
	names := []string{"node-exporter"}
	if strings.HasPrefix(role, "node-") {
		names = append(names, "wukongim")
	} else {
		names = append(names, "wkbench-worker", "wkbench-run", "prometheus", "wkanalysis")
		if publicObservation {
			names = append(names, "wkcloudview")
		}
	}
	result := make(map[string]string, len(names))
	for _, name := range names {
		result[name] = filepath.Join(bundleRoot, "systemd", name+".service")
	}
	return result
}

func startRoleServices(role string, publicObservation bool) error {
	if err := runCommand("systemctl", "daemon-reload"); err != nil {
		return err
	}
	names := []string{"node-exporter.service"}
	if strings.HasPrefix(role, "node-") {
		names = append(names, "wukongim.service")
	} else {
		names = append(names, "prometheus.service", "wkbench-worker.service", "wkanalysis.service")
		if publicObservation {
			names = append(names, "wkcloudview.service")
		}
	}
	args := append([]string{"enable", "--now"}, names...)
	return runCommand("systemctl", args...)
}

func ensureServiceUser() error {
	if err := exec.Command("id", "-u", "wukongim").Run(); err == nil {
		return nil
	}
	return runCommand("useradd", "--system", "--home", "/var/lib/wukongim-cloud", "--shell", "/usr/sbin/nologin", "wukongim")
}

func prepareDataDisk(device string) error {
	if !strings.HasPrefix(device, "/dev/") {
		return errors.New("data device must be under /dev")
	}
	info, err := os.Stat(device)
	if err != nil || info.Mode()&os.ModeDevice == 0 {
		return errors.New("data device must be an existing block device")
	}
	if mount, mounted, err := mountedAt(device); err != nil {
		return err
	} else if mounted {
		if mount != "/var/lib/wukongim-cloud" {
			return fmt.Errorf("data device is already mounted at %s", mount)
		}
		return ensureDataDiskFstab(device)
	}
	filesystem, err := exec.Command("blkid", "-s", "TYPE", "-o", "value", device).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 2 {
			return fmt.Errorf("inspect data device filesystem: %w", err)
		}
		if err := runCommand("mkfs.ext4", "-F", device); err != nil {
			return err
		}
		filesystem, err = exec.Command("blkid", "-s", "TYPE", "-o", "value", device).Output()
		if err != nil {
			return fmt.Errorf("verify data device filesystem: %w", err)
		}
	}
	if strings.TrimSpace(string(filesystem)) != "ext4" {
		return errors.New("data disk contains an unexpected filesystem")
	}
	if err := os.MkdirAll("/var/lib/wukongim-cloud", 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir("/var/lib/wukongim-cloud")
	if err != nil || len(entries) != 0 {
		return errors.New("data disk mount point must be empty")
	}
	if err := runCommand("mount", "-o", "nodev,nosuid,noatime", device, "/var/lib/wukongim-cloud"); err != nil {
		return err
	}
	return ensureDataDiskFstab(device)
}

func mountedAt(device string) (string, bool, error) {
	output, err := exec.Command("findmnt", "-rn", "-S", device, "-o", "TARGET").Output()
	if err == nil {
		mount := strings.TrimSpace(string(output))
		if mount == "" {
			return "", false, errors.New("findmnt returned an empty target")
		}
		return mount, true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return "", false, nil
	}
	return "", false, fmt.Errorf("inspect data device mount: %w", err)
}

func ensureDataDiskFstab(device string) error {
	uuidBytes, err := exec.Command("blkid", "-s", "UUID", "-o", "value", device).Output()
	if err != nil || strings.TrimSpace(string(uuidBytes)) == "" {
		return errors.New("data disk UUID is unavailable")
	}
	entry := fmt.Sprintf("UUID=%s /var/lib/wukongim-cloud ext4 defaults,nofail,nodev,nosuid,noatime 0 2", strings.TrimSpace(string(uuidBytes)))
	return appendFstabOnce("/etc/fstab", entry)
}

func appendFstabOnce(path, entry string) error {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == entry {
			return nil
		}
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = fmt.Fprintln(file, entry)
	return err
}

func copyRegular(source, destination string, mode fs.FileMode) error {
	info, err := os.Lstat(source)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("copy regular file %s: %w", source, errors.Join(err, deploy.ErrInvalidBundle))
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr == nil {
		copyErr = os.Chmod(destination, mode)
	}
	return errors.Join(copyErr, closeErr)
}

func runCommand(name string, args ...string) error {
	command := exec.Command(name, args...)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func rooted(root, relative string) string { return filepath.Join(root, filepath.FromSlash(relative)) }

func validRole(role string) bool {
	return role == "node-1" || role == "node-2" || role == "node-3" || role == "sim"
}
