package scripts_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestNativePackageConfigurationIsPreviewOnly(t *testing.T) {
	root := repoRoot(t)
	raw := readNativePackageFile(t, root, ".goreleaser.packages.yaml")
	var document any
	require.NoError(t, yaml.Unmarshal([]byte(raw), &document))

	for _, required := range []string{
		"version: 2",
		"CGO_ENABLED=0",
		"goos:",
		"- linux",
		"goarch:",
		"- amd64",
		"- deb",
		"- rpm",
		"bindir: /usr/bin",
		"main: ./cmd/wkcli",
		"binary: wkcli",
		"- wkcli-linux-amd64",
		"-X main.buildVersion={{ .Version }}",
		"-X main.buildCommit={{ .Commit }}",
		"-X main.buildSource=release",
		"disable: true",
	} {
		require.Contains(t, raw, required)
	}
	for _, forbidden := range []string{
		"arm64",
		"signs:",
		"publishers:",
		"uploads:",
		"packages.githubim.com",
	} {
		require.NotContains(t, raw, forbidden)
	}
	artifactValidation := readNativePackageFile(t, root, "scripts/validate-native-package.sh")
	require.Contains(t, artifactValidation, `if [[ "${WK_NATIVE_PACKAGE_SKIP_BUILD:-0}" != "1" ]]; then
  if ! command -v goreleaser`)
	require.Contains(t, artifactValidation, "goreleaser check --config .goreleaser.packages.yaml")
	require.Contains(t, artifactValidation, `deb_contents="$(dpkg-deb --contents`)
	require.Contains(t, artifactValidation, `rpm_contents="$(rpm -qpl`)
	require.NotContains(t, artifactValidation, `dpkg-deb --contents "${deb_packages[0]}" |`)
	require.NotContains(t, artifactValidation, `rpm -qpl "${rpm_packages[0]}" |`)
	containerValidation := readNativePackageFile(t, root, "scripts/validate-native-package-container.sh")
	for _, required := range []string{
		"--platform linux/amd64",
		"WK_NATIVE_PACKAGE_DIST_DIR",
		"NATIVE_PACKAGE_TEST_APT_MIRROR",
		"apt-get install --reinstall",
		"dnf reinstall",
		"wukongim-systemctl.calls",
		"/usr/bin/wukongim init --admin-password-stdin",
		"native package upgrade changed service activation state",
		"sha256sum --check /tmp/wukongim-package-state.sha256",
		"package-upgrade-sentinel",
	} {
		require.Contains(t, containerValidation, required)
	}
}

func TestNativePackageDoesNotActivateAnUnconfiguredService(t *testing.T) {
	root := repoRoot(t)
	postinstall := readNativePackageFile(t, root, "packaging/scripts/postinstall.sh")
	preremove := readNativePackageFile(t, root, "packaging/scripts/preremove.sh")
	postremove := readNativePackageFile(t, root, "packaging/scripts/postremove.sh")
	service := readNativePackageFile(t, root, "packaging/systemd/wukongim.service")

	for _, forbidden := range []string{
		"systemctl start",
		"systemctl restart",
		"systemctl enable",
		"enable --now",
		"wukongim.toml.example",
	} {
		require.NotContains(t, postinstall, forbidden)
	}
	require.Contains(t, postinstall, "systemd-sysusers")
	require.Contains(t, postinstall, "systemd-tmpfiles --create")
	require.Contains(t, postinstall, "systemctl daemon-reload")

	require.Contains(t, service, "ConditionPathExists=/etc/wukongim/wukongim.toml")
	require.Contains(t, service, "ExecStart=/usr/bin/wukongim -config")
	require.Contains(t, service, "RestartPreventExitStatus=78")
	require.NotContains(t, service, "ExecStartPre=")
	require.Contains(t, service, "ProtectSystem=strict")
	require.Contains(t, service, "ReadWritePaths=/var/lib/wukongim /var/log/wukongim /run/wukongim")

	require.Contains(t, preremove, "remove|purge|0)")
	require.NotContains(t, preremove, "systemctl restart")
	for _, script := range []string{postinstall, preremove, postremove} {
		for _, destructive := range []string{"rm -", "userdel", "groupdel", "/etc/wukongim/wukongim.toml"} {
			require.NotContains(t, script, destructive)
		}
	}
}

func TestNativePackageBootstrapProgressCheckpoints(t *testing.T) {
	validator := readNativePackageFile(t, repoRoot(t), "scripts/validate-native-package-lifecycle-container.sh")
	previous := -1
	for _, checkpoint := range []string{"bootstrap_stage package-update", "$bootstrap_update_command\n", "bootstrap_stage package-install", "$install_command\n", "bootstrap_stage systemd-exec", "then exec /lib/systemd/systemd"} {
		position := strings.Index(validator, checkpoint)
		require.Greater(t, position, previous, "missing or reordered bootstrap checkpoint: %s", checkpoint)
		previous = position
	}
}

func TestNativePackageLifecycleUsesRealSystemd(t *testing.T) {
	root := repoRoot(t)
	validator := readNativePackageFile(t, root, "scripts/validate-native-package-lifecycle-container.sh")
	for _, required := range []string{
		"--privileged",
		"--cgroupns private",
		"native package lifecycle validation exceeded its 900-second total deadline",
		`run_bounded 300 docker pull --platform linux/amd64 "$image"`,
		"run_bounded 30 docker rm --force --volumes",
		"package bootstrap and systemd did not reach running or degraded state within 300 seconds",
		"IFS= read -r -t 2 status",
		"probe_http /healthz",
		"probe_http /readyz",
		"/usr/bin/wukongim config init --config /etc/wukongim/wukongim.toml --admin-password-stdin",
		"systemctl enable --now wukongim.service",
		`run_shell_bounded 300 "$reinstall_command"`,
		"InvocationID",
		"require_service_identity",
		"systemctl restart wukongim.service",
		"ActiveState=$active_state SubState=$sub_state Result=$result MainPID=$main_pid",
		"readyz remained reachable after explicit stop",
		"sha256sum --check /tmp/wukongim-lifecycle-state.sha256",
		"wukongim-lifecycle-state.manifest",
		"getent passwd wukongim",
		"require_unit_removed \"$removed_pid\"",
		"test ! -e /usr/bin/wukongim",
		"test ! -e /usr/lib/systemd/system/wukongim.service",
		"journalctl --no-pager -u wukongim.service -n 300",
		"native package lifecycle validation passed",
	} {
		require.Contains(t, validator, required)
	}
	for _, forbidden := range []string{
		"/workspace",
		"docker.sock",
		"/sys/fs/cgroup",
		"--volume $repository_root",
	} {
		require.NotContains(t, validator, forbidden)
	}
	require.NotContains(t, validator, `run_shell "$reinstall_command"`)
	require.NotContains(t, validator, `run_shell "$remove_command"`)
	require.NotContains(t, validator, `run_shell "$install_command"`)

	info, err := os.Stat(filepath.Join(root, "scripts", "validate-native-package-lifecycle-container.sh"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o755), info.Mode().Perm())

	wrapper := readNativePackageFile(t, root, "scripts/native_package_lifecycle_integration_test.go")
	require.Contains(t, wrapper, "command.Process.Signal(syscall.SIGTERM)")
	require.Contains(t, wrapper, "command.WaitDelay = 3 * time.Minute")
}

func TestNativePackageFilesUseExpectedModes(t *testing.T) {
	root := repoRoot(t)
	for _, path := range []string{
		"packaging/scripts/postinstall.sh",
		"packaging/scripts/preremove.sh",
		"packaging/scripts/postremove.sh",
	} {
		info, err := os.Stat(filepath.Join(root, path))
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o755), info.Mode().Perm(), path)
	}
	for _, path := range []string{
		"packaging/systemd/wukongim.service",
		"packaging/systemd/wukongim.sysusers",
		"packaging/systemd/wukongim.tmpfiles",
	} {
		info, err := os.Stat(filepath.Join(root, path))
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o644), info.Mode().Perm(), path)
	}
}

func TestNativePackageRepositorySigningFailsClosed(t *testing.T) {
	root := repoRoot(t)
	generator := readNativePackageFile(t, root, "scripts/generate-native-package-test-keyring.sh")
	builder := readNativePackageFile(t, root, "scripts/build-native-package-repositories.sh")
	signer := readNativePackageFile(t, root, "scripts/sign-native-package-repositories.sh")
	verifier := readNativePackageFile(t, root, "scripts/verify-native-package-repositories.sh")
	metadataVerifier := readNativePackageFile(t, root, "scripts/verify-native-package-metadata.py")
	containerVerifier := readNativePackageFile(t, root, "scripts/validate-native-package-repositories-container.sh")
	for _, required := range []string{
		"--network none",
		"--read-only",
		"find /work -xdev -mindepth 1 -depth -delete",
		"trap - EXIT HUP INT TERM",
		"if ((status != 0)); then",
		"exit \"$cleanup_status\"",
	} {
		require.Contains(t, containerVerifier, required)
	}

	for _, required := range []string{
		"TEST ONLY",
		"must be generated outside the repository",
		"--export-secret-subkeys",
		"gpgconf --homedir \"$signing_home\" --kill all",
	} {
		require.Contains(t, generator, required)
	}
	for _, required := range []string{
		"dpkg-scanpackages --multiversion",
		"APT::FTPArchive::Release::Acquire-By-Hash=yes",
		">\"Release.tmp\"",
		"by-hash/SHA256",
		"find \"$stage\" -type d -exec chmod 0755",
	} {
		require.Contains(t, builder, required)
	}
	for _, required := range []string{
		"--test-only",
		"minimum-valid-days",
		"fingerprints must be complete 40-hex values",
		"must be owned by the signing user",
		"must not be accessible by group or other",
		"primary secret material must not be present",
		"primary certificate must match exactly, remain valid, and be certify-only",
		"exactly the requested sign-only subkey",
		"--test-only certificate UID must contain TEST ONLY",
		"TEST ONLY certificate is forbidden for production signing",
		"output repository must not be located inside the input repository",
		"input repository may contain only regular files and directories",
		"APT repository contains an unsupported Packages index",
		"--local-user \"$signing!\"",
		"--define \"__gpg $(command -v gpg)\"",
		"rpm --dbpath \"$rpm_verify_db\" --import",
		"rpmkeys --dbpath \"$rpm_verify_db\" --verbose --checksig",
		"issuer fpr v4 $rpm_signing",
		"TEST_ONLY=$test_only",
	} {
		require.Contains(t, signer, required)
	}
	rpmSigningIndex := strings.Index(signer, "--addsign")
	rpmMetadataResetIndex := strings.Index(signer, "rm -rf -- \"$rpm_repository_path/repodata\"")
	rpmMetadataSignatureIndex := strings.Index(signer, "repomd.xml.asc")
	require.NotEqual(t, -1, rpmSigningIndex)
	require.NotEqual(t, -1, rpmMetadataResetIndex)
	require.NotEqual(t, -1, rpmMetadataSignatureIndex)
	require.Less(t, rpmSigningIndex, rpmMetadataResetIndex)
	require.Less(t, rpmMetadataResetIndex, rpmMetadataSignatureIndex)
	aptIndexPolicyIndex := strings.Index(signer, "unsupported_apt_index=")
	require.NotEqual(t, -1, aptIndexPolicyIndex)
	require.Less(t, aptIndexPolicyIndex, rpmSigningIndex)
	for _, required := range []string{
		"--allow-test-only",
		"a zero-day validity floor requires --allow-test-only",
		"public key must contain exactly one primary certificate",
		"public key must contain exactly the requested valid sign-only subkey",
		"test-only certificate is forbidden without --allow-test-only",
		"gpgv --status-fd 1",
		"InRelease cleartext differs from Release",
		"APT Release must enable Acquire-By-Hash",
		"APT Release contains no SHA256 entries",
		"APT by-hash target differs",
		"rpmkeys --dbpath \"$rpm_db\" --checksig",
		"issuer fpr v4 $rpm_signing",
		"repomd.xml.asc",
		"python3 \"$metadata_verifier\"",
	} {
		require.Contains(t, verifier, required)
	}
	for _, required := range []string{
		"APT Packages indexes do not close over the exact pool payload set",
		"APT Release authenticates unsupported Packages indexes",
		"APT Release does not close over the exact Packages index set",
		"RPM repomd.xml does not close over the exact repodata file set",
		"RPM primary metadata does not close over the exact package set",
		"APT payload digest or size mismatch",
		"RPM compressed metadata digest or size mismatch",
		"open size exceeds the repository budget",
		"zstd failed for",
		"target.is_symlink() or not stat.S_ISREG(mode)",
	} {
		require.Contains(t, metadataVerifier, required)
	}
	for _, required := range []string{
		"--allow-test-only",
		"production verifier accepted test-only keys",
		"signed unsupported APT Packages.xz was accepted",
		"signer accepted an unsupported APT Packages.xz",
		"signer accepted an output repository inside its input",
		"missing deb was accepted",
		"tampered RPM metadata was accepted",
		"unindexed validly signed RPM was accepted",
		"expanded APT key bundle was accepted",
		"group-readable signing passphrase was accepted",
		"special repository input was accepted",
		"repo_gpgcheck=1",
		"gpgcheck=1",
	} {
		require.Contains(t, containerVerifier, required)
	}

	for _, path := range []string{
		"scripts/generate-native-package-test-keyring.sh",
		"scripts/build-native-package-repositories.sh",
		"scripts/sign-native-package-repositories.sh",
		"scripts/verify-native-package-repositories.sh",
		"scripts/verify-native-package-metadata.py",
		"scripts/validate-native-package-repositories-container.sh",
	} {
		info, err := os.Stat(filepath.Join(root, path))
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o755), info.Mode().Perm(), path)
	}
}

func readNativePackageFile(t *testing.T, root, relative string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
	require.NoError(t, err)
	return strings.ReplaceAll(string(body), "\r\n", "\n")
}
