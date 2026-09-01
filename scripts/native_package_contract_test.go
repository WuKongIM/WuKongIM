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
	containerValidation := readNativePackageFile(t, root, "scripts/validate-native-package-container.sh")
	for _, required := range []string{
		"--platform linux/amd64",
		"WK_NATIVE_PACKAGE_DIST_DIR",
		"NATIVE_PACKAGE_TEST_APT_MIRROR",
		"apt-get install --reinstall",
		"dnf reinstall",
		"wukongim-systemctl.calls",
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

func TestNativePackageWorkflowIsCredentialFreeAndBounded(t *testing.T) {
	root := repoRoot(t)
	raw := readNativePackageFile(t, root, ".github/workflows/native-package-preview.yml")
	var document any
	require.NoError(t, yaml.Unmarshal([]byte(raw), &document))

	for _, required := range []string{
		"permissions:\n  contents: read",
		"persist-credentials: false",
		"goreleaser/goreleaser-action@4c6ab561adb47e50c45ef534e2155934e91c40c1",
		"version: v2.18.0",
		"scripts/native_package_repository_integration_test.go",
		"scripts/validate-native-package-repositories-container.sh",
		"scripts/verify-native-package-metadata.py",
		"WK_NATIVE_PACKAGE_REPOSITORY_INTEGRATION: \"1\"",
		"go test -tags=integration ./scripts",
		"-run '^TestNativePackageSignedRepository$'",
		"ubuntu:24.04 deb",
		"debian:12 deb",
		"rockylinux:9 rpm",
		"almalinux:9 rpm",
		"retention-days: 14",
	} {
		require.Contains(t, raw, required)
	}
	for _, forbidden := range []string{
		"contents: write",
		"packages: write",
		"id-token: write",
		"secrets.",
		"packages.githubim.com",
		"release --clean",
	} {
		require.NotContains(t, raw, forbidden)
	}
}

func readNativePackageFile(t *testing.T, root, relative string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
	require.NoError(t, err)
	return strings.ReplaceAll(string(body), "\r\n", "\n")
}
