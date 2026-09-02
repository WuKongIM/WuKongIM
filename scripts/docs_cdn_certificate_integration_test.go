//go:build integration

package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const docsCDNInstalledCertificateSummary = `{"certificate_present":true,"fingerprint":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","domain_cname_status":"cname_error","days_remaining":45,"not_after":"2026-10-18T14:59:59Z","renewal_required":false,"seconds_remaining":3888000}`

const docsCDNMissingCertificateSummary = `{"certificate_present":false,"fingerprint":"","domain_cname_status":"","days_remaining":0,"not_after":"missing","renewal_required":true,"seconds_remaining":0}`

func TestDocsCDNCertificateInspectAcceptsFalseRenewalDecision(t *testing.T) {
	fixture := newDocsCDNCertificateFixture(t)
	output, err := fixture.run(t, "inspect", docsCDNInstalledCertificateSummary, false)
	require.NoError(t, err, string(output))
	require.Contains(t, string(output), `"renewal_required":false`)

	githubOutput := readFile(t, fixture.githubOutputPath)
	for _, want := range []string{
		"certificate_present=true\n",
		"renewal_required=false\n",
		"domain_cname_status=cname_error\n",
	} {
		require.Contains(t, githubOutput, want)
	}
}

func TestDocsCDNCertificateInspectAllowsForcedMissingCertificate(t *testing.T) {
	fixture := newDocsCDNCertificateFixture(t)
	output, err := fixture.run(t, "inspect", docsCDNMissingCertificateSummary, true)
	require.NoError(t, err, string(output))
	require.Contains(t, string(output), `"certificate_present":false`)

	githubOutput := readFile(t, fixture.githubOutputPath)
	require.Contains(t, githubOutput, "certificate_present=false\n")
	require.Contains(t, githubOutput, "renewal_required=true\n")
	require.Contains(t, readFile(t, fixture.githubSummaryPath), "Public edge verification: `skipped-no-certificate-installed`")
}

func TestDocsCDNCertificateInspectRejectsMissingOrNonBooleanFields(t *testing.T) {
	testCases := []struct {
		name        string
		summary     string
		wantFailure string
	}{
		{
			name:        "missing certificate presence",
			summary:     `{"fingerprint":"","domain_cname_status":"","days_remaining":0,"not_after":"missing","renewal_required":true,"seconds_remaining":0}`,
			wantFailure: "invalid certificate presence",
		},
		{
			name:        "string certificate presence",
			summary:     `{"certificate_present":"false","fingerprint":"","domain_cname_status":"","days_remaining":0,"not_after":"missing","renewal_required":true,"seconds_remaining":0}`,
			wantFailure: "invalid certificate presence",
		},
		{
			name:        "missing renewal decision",
			summary:     `{"certificate_present":true,"fingerprint":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","domain_cname_status":"cname_error","days_remaining":45,"not_after":"2026-10-18T14:59:59Z","seconds_remaining":3888000}`,
			wantFailure: "invalid renewal decision",
		},
		{
			name:        "string renewal decision",
			summary:     `{"certificate_present":true,"fingerprint":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","domain_cname_status":"cname_error","days_remaining":45,"not_after":"2026-10-18T14:59:59Z","renewal_required":"false","seconds_remaining":3888000}`,
			wantFailure: "invalid renewal decision",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newDocsCDNCertificateFixture(t)
			output, err := fixture.run(t, "inspect", testCase.summary, true)
			require.Error(t, err, string(output))
			require.Contains(t, string(output), testCase.wantFailure)
		})
	}
}

func TestDocsCDNCertificateRotateAcceptsFalseBooleansBeforeApplyingPolicy(t *testing.T) {
	t.Run("renewal not required", func(t *testing.T) {
		fixture := newDocsCDNCertificateFixture(t)
		output, err := fixture.run(t, "rotate", docsCDNInstalledCertificateSummary, false)
		require.Error(t, err, string(output))
		require.Contains(t, string(output), "refusing renewal outside the fixed 30-day window")
		require.NotContains(t, string(output), "invalid renewal decision")
	})

	t.Run("forced missing certificate", func(t *testing.T) {
		fixture := newDocsCDNCertificateFixture(t)
		output, err := fixture.run(t, "rotate", docsCDNMissingCertificateSummary, true)
		require.Error(t, err, string(output))
		require.Contains(t, string(output), "the fixed ACME challenge CNAME delegation is missing")
		require.NotContains(t, string(output), "invalid certificate presence")
	})
}

type docsCDNCertificateFixture struct {
	scriptPath        string
	binPath           string
	runnerTempPath    string
	githubOutputPath  string
	githubSummaryPath string
	helperPath        string
}

func newDocsCDNCertificateFixture(t *testing.T) docsCDNCertificateFixture {
	t.Helper()
	root := repoRoot(t)
	temporaryDirectory := t.TempDir()
	binPath := filepath.Join(temporaryDirectory, "bin")
	require.NoError(t, os.Mkdir(binPath, 0o755))

	writeDocsCDNExecutable(t, filepath.Join(binPath, "aliyun"), `#!/bin/sh
printf '%s\n' '{"CertInfos":{"CertInfo":[]}}'
`)
	writeDocsCDNExecutable(t, filepath.Join(binPath, "timeout"), `#!/bin/sh
shift
exec "$@"
`)
	writeDocsCDNExecutable(t, filepath.Join(binPath, "lego"), `#!/bin/sh
exit 97
`)
	helperPath := filepath.Join(binPath, "certificate-helper")
	writeDocsCDNExecutable(t, helperPath, `#!/bin/sh
case "${1:-}" in
  inspect-cdn)
    printf '%s\n' "$DOCS_TEST_INSPECTION_SUMMARY"
    ;;
  verify-delegation)
    exit 42
    ;;
  *)
    exit 43
    ;;
esac
`)

	return docsCDNCertificateFixture{
		scriptPath:        filepath.Join(root, "scripts", "docs-cdn", "certificate.sh"),
		binPath:           binPath,
		runnerTempPath:    filepath.Join(temporaryDirectory, "runner"),
		githubOutputPath:  filepath.Join(temporaryDirectory, "github-output"),
		githubSummaryPath: filepath.Join(temporaryDirectory, "github-summary"),
		helperPath:        helperPath,
	}
}

func (fixture docsCDNCertificateFixture) run(
	t *testing.T,
	operation string,
	summary string,
	forceRenew bool,
) ([]byte, error) {
	t.Helper()
	require.NoError(t, os.MkdirAll(fixture.runnerTempPath, 0o755))
	forceRenewValue := "false"
	if forceRenew {
		forceRenewValue = "true"
	}
	command := exec.CommandContext(t.Context(), "bash", fixture.scriptPath, operation)
	command.Env = []string{
		"ALIBABA_CLOUD_ACCESS_KEY_ID=test-access-key-id",
		"ALIBABA_CLOUD_ACCESS_KEY_SECRET=test-access-key-secret",
		"ALIBABA_CLOUD_SECURITY_TOKEN=test-security-token",
		"DOCS_ACME_ACCOUNT_BUNDLE_B64=test-account-bundle",
		"DOCS_ACME_EMAIL=tangtaoit@githubim.com",
		"DOCS_CDN_DOMAIN=docs.githubim.com",
		"DOCS_CDN_ENABLED=true",
		"DOCS_CERTIFICATE_HELPER=" + fixture.helperPath,
		"DOCS_CERT_FORCE_RENEW=" + forceRenewValue,
		"DOCS_TEST_INSPECTION_SUMMARY=" + summary,
		"GITHUB_EVENT_NAME=workflow_dispatch",
		"GITHUB_OUTPUT=" + fixture.githubOutputPath,
		"GITHUB_STEP_SUMMARY=" + fixture.githubSummaryPath,
		"PATH=" + fixture.binPath + string(os.PathListSeparator) + os.Getenv("PATH"),
		"RUNNER_TEMP=" + fixture.runnerTempPath,
	}
	return command.CombinedOutput()
}

func writeDocsCDNExecutable(t *testing.T, path string, contents string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o755))
}
