package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDocsCDNRefreshRunsOnlyAfterSuccessfulPagesDeployment(t *testing.T) {
	workflow := readFile(
		t,
		filepath.Join(repoRoot(t), ".github", "workflows", "docs-pages.yml"),
	)

	for _, want := range []string{
		"refresh_cdn:",
		"needs: deploy",
		"if: github.ref == 'refs/heads/main' && vars.DOCS_CDN_ENABLED == 'true'",
		"name: docs-cdn",
		"contents: read",
		"id-token: write",
		"persist-credentials: false",
		"scripts/docs-cdn/install-aliyun-cli.sh",
		"scripts/docs-cdn/refresh.sh",
		"DOCS_CDN_DOMAIN: ${{ vars.DOCS_CDN_DOMAIN }}",
		"DOCS_CDN_REFRESH_ROLE_ARN: ${{ vars.DOCS_CDN_REFRESH_ROLE_ARN }}",
		"DOCS_CDN_CERTIFICATE_ROLE_ARN: ${{ vars.DOCS_CDN_CERTIFICATE_ROLE_ARN }}",
		"DOCS_CDN_OIDC_PROVIDER_ARN: ${{ vars.DOCS_CDN_OIDC_PROVIDER_ARN }}",
		"DOCS_CDN_OIDC_AUDIENCE: ${{ vars.DOCS_CDN_OIDC_AUDIENCE }}",
		`[[ "$DOCS_CDN_DOMAIN" == docs.githubim.com ]]`,
		`[[ "$DOCS_CDN_REFRESH_ROLE_ARN" =~ ^acs:ram::([0-9]+):role/[A-Za-z0-9_-]+$ ]]`,
		`[[ "$DOCS_CDN_CERTIFICATE_ROLE_ARN" =~ ^acs:ram::([0-9]+):role/[A-Za-z0-9_-]+$ ]]`,
		`[[ "$DOCS_CDN_OIDC_PROVIDER_ARN" =~ ^acs:ram::([0-9]+):oidc-provider/[A-Za-z0-9._-]+$ ]]`,
		`[[ "$role_account" == "$certificate_role_account" && "$role_account" == "$provider_account" ]]`,
		`[[ "$DOCS_CDN_REFRESH_ROLE_ARN" != "$DOCS_CDN_CERTIFICATE_ROLE_ARN" ]]`,
		`[[ "$DOCS_CDN_OIDC_AUDIENCE" =~ ^[A-Za-z0-9._:-]{1,128}$ ]]`,
		"role-to-assume: ${{ vars.DOCS_CDN_REFRESH_ROLE_ARN }}",
		"oidc-provider-arn: ${{ vars.DOCS_CDN_OIDC_PROVIDER_ARN }}",
		"audience: ${{ vars.DOCS_CDN_OIDC_AUDIENCE }}",
		"role-session-expiration: 900",
		"aliyun/configure-aliyun-credentials-action@1e5248c8d5d93a8781ac344a68e19a43341e79e6",
	} {
		require.Contains(t, workflow, want)
	}
	require.NotContains(t, workflow, "DOCS_CDN_ACCESS_KEY")
	require.NotContains(t, workflow, "secrets.ALIBABA")

	deployStart := strings.Index(workflow, "\n  deploy:")
	refreshStart := strings.Index(workflow, "\n  refresh_cdn:")
	require.NotEqual(t, -1, deployStart)
	require.Greater(t, refreshStart, deployStart)
	require.NotContains(
		t,
		workflow[deployStart:refreshStart],
		"configure-aliyun-credentials-action",
		"the Pages deployment job must not receive Alibaba credentials",
	)

	preflightStart := strings.Index(workflow, "- name: Validate the exact CDN refresh binding without credentials")
	authStart := strings.Index(workflow, "- name: Exchange the exact CDN refresh OIDC identity")
	require.NotEqual(t, -1, preflightStart)
	require.Greater(t, authStart, preflightStart, "binding validation must happen before OIDC exchange")
}

func TestDocsCDNRefreshHelperHasAnExactBoundedMutationSurface(t *testing.T) {
	helper := readFile(
		t,
		filepath.Join(repoRoot(t), "scripts", "docs-cdn", "refresh.sh"),
	)

	for _, want := range []string{
		`readonly expected_domain="docs.githubim.com"`,
		`[[ "${DOCS_CDN_ENABLED:-}" == true ]]`,
		`[[ "${DOCS_CDN_DOMAIN:-}" == "$expected_domain" ]]`,
		`[[ -n "${ALIBABA_CLOUD_SECURITY_TOKEN:-}" ]]`,
		`export ALIBABA_CLOUD_IGNORE_PROFILE=TRUE`,
		`export ALIBABA_CLOUD_DISABLE_EXTERNAL_PROCESS=TRUE`,
		`aliyun --region cn-hangzhou cdn RefreshObjectCaches`,
		`--ObjectType File`,
		`https://${expected_domain}/`,
		`https://${expected_domain}/zh/`,
		`https://${expected_domain}/en/`,
		`https://${expected_domain}/api/search`,
		`.RefreshTaskId`,
		`.RequestId`,
		`test("^[A-Za-z0-9-]{1,128}$")`,
	} {
		require.Contains(t, helper, want)
	}
	require.NotContains(t, helper, "PushObjectCache")
	require.NotContains(t, helper, "--ObjectType Directory")
	require.NotContains(t, helper, "http://${expected_domain}")
}

func TestDocsCDNRefreshHelperSubmitsOnlyTheFourExactFileURLs(t *testing.T) {
	root := repoRoot(t)
	fakeBin := t.TempDir()
	argumentsPath := filepath.Join(t.TempDir(), "aliyun-arguments")
	fakeAliyun := filepath.Join(fakeBin, "aliyun")
	require.NoError(t, os.WriteFile(fakeAliyun, []byte(`#!/usr/bin/env bash
set -euo pipefail
printf '%s\0' "$@" >"$DOCS_CDN_TEST_ARGUMENTS"
printf '%s\n' '{"RefreshTaskId":"704222901","RequestId":"D61E4801-EAFF-4A63-AAE1-FBF6CE1CFD1C"}'
`), 0o700))

	command := exec.Command("bash", filepath.Join(root, "scripts", "docs-cdn", "refresh.sh"))
	command.Env = []string{
		"PATH=" + fakeBin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"DOCS_CDN_ENABLED=true",
		"DOCS_CDN_DOMAIN=docs.githubim.com",
		"ALIBABA_CLOUD_ACCESS_KEY_ID=temporary-id",
		"ALIBABA_CLOUD_ACCESS_KEY_SECRET=temporary-secret",
		"ALIBABA_CLOUD_SECURITY_TOKEN=temporary-token",
		"DOCS_CDN_TEST_ARGUMENTS=" + argumentsPath,
	}
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	require.Contains(t, string(output), "urls=4")

	arguments, err := os.ReadFile(argumentsPath)
	require.NoError(t, err)
	require.Equal(t, []string{
		"--region",
		"cn-hangzhou",
		"cdn",
		"RefreshObjectCaches",
		"--ObjectPath",
		"https://docs.githubim.com/\n" +
			"https://docs.githubim.com/zh/\n" +
			"https://docs.githubim.com/en/\n" +
			"https://docs.githubim.com/api/search",
		"--ObjectType",
		"File",
	}, strings.Split(strings.TrimSuffix(string(arguments), "\x00"), "\x00"))
}

func TestDocsCDNRefreshHelperRejectsDisabledRunsBeforeCallingAlibaba(t *testing.T) {
	root := repoRoot(t)
	fakeBin := t.TempDir()
	calledPath := filepath.Join(t.TempDir(), "aliyun-called")
	fakeAliyun := filepath.Join(fakeBin, "aliyun")
	require.NoError(t, os.WriteFile(fakeAliyun, []byte(`#!/usr/bin/env bash
set -euo pipefail
: >"$DOCS_CDN_TEST_CALLED"
`), 0o700))

	command := exec.Command("bash", filepath.Join(root, "scripts", "docs-cdn", "refresh.sh"))
	command.Env = []string{
		"PATH=" + fakeBin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"DOCS_CDN_ENABLED=false",
		"DOCS_CDN_DOMAIN=docs.githubim.com",
		"ALIBABA_CLOUD_ACCESS_KEY_ID=temporary-id",
		"ALIBABA_CLOUD_ACCESS_KEY_SECRET=temporary-secret",
		"ALIBABA_CLOUD_SECURITY_TOKEN=temporary-token",
		"DOCS_CDN_TEST_CALLED=" + calledPath,
	}
	output, err := command.CombinedOutput()
	require.Error(t, err)
	require.Contains(t, string(output), "DOCS_CDN_ENABLED must be exactly true")
	_, statErr := os.Stat(calledPath)
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestDocsCDNAliyunCLIInstallerIsPinnedAndLocal(t *testing.T) {
	installer := readFile(
		t,
		filepath.Join(repoRoot(t), "scripts", "docs-cdn", "install-aliyun-cli.sh"),
	)

	for _, want := range []string{
		`readonly cli_version="3.4.11"`,
		`aliyun-cli-linux-${cli_version}-amd64.tgz`,
		`a7e3df497db14c10d4d7587795e9fa7849b0c51dfce02908b9de5a41fe717d5c`,
		`https://github.com/aliyun/aliyun-cli/releases/download/v${cli_version}/${archive_name}`,
		`--proto '=https'`,
		`--connect-timeout 15`,
		`--max-time 180`,
		`sha256sum --check --strict`,
		`[[ ${#archive_entries[@]} -eq 1 && "${archive_entries[0]}" == aliyun ]]`,
		`install -m 0755 "$extract_directory/aliyun" "$install_directory/aliyun"`,
	} {
		require.Contains(t, installer, want)
	}
	require.NotContains(t, installer, "latest")
	require.NotContains(t, installer, "sudo")
	require.NotContains(t, installer, "/usr/local")
}
