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

const docsCDNInstalledCertificateWithProviderCNAMEOKSummary = `{"certificate_present":true,"fingerprint":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","domain_cname_status":"ok","days_remaining":45,"not_after":"2026-10-18T14:59:59Z","renewal_required":false,"seconds_remaining":3888000}`

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

func TestDocsCDNCertificateInspectSkipsGitHubPagesBeforeDNSCutover(t *testing.T) {
	fixture := newDocsCDNCertificateFixture(t)
	output, err := fixture.run(t, "inspect", docsCDNInstalledCertificateWithProviderCNAMEOKSummary, false)
	require.NoError(t, err, string(output))
	require.Contains(
		t,
		readFile(t, fixture.githubSummaryPath),
		"Public edge verification: `skipped-public-dns-not-on-alibaba-cdn`",
	)
	lookups := readFile(t, fixture.dnsLookupPath)
	for _, resolver := range []string{"223.5.5.5", "1.1.1.1", "8.8.8.8"} {
		require.Contains(t, lookups, "@"+resolver+" docs.githubim.com CNAME\n")
		require.Contains(
			t,
			readFile(t, fixture.githubSummaryPath),
			"Public CNAME via `"+resolver+"`: `wukongim.github.io`",
		)
	}
}

func TestDocsCDNCertificateInspectVerifiesAlibabaCDNFromPublicDNS(t *testing.T) {
	fixture := newDocsCDNCertificateFixture(t)
	fixture.publicRouteMode = "alibaba-cdn"
	fixture.aliDNSCNAME = "DOCS.GITHUBIM.COM.W.KUNLUNAQ.COM."
	fixture.cloudflareCNAME = "docs.githubim.com.w.kunlunaq.com."
	fixture.googleCNAME = "Docs.Githubim.Com.W.Kunlunaq.Com"
	fixture.publicFingerprint = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	output, err := fixture.run(t, "inspect", docsCDNInstalledCertificateSummary, false)
	require.NoError(t, err, string(output))
	summary := readFile(t, fixture.githubSummaryPath)
	require.Contains(t, summary, "Public edge verification: `passed`")
	for _, resolver := range []string{"223.5.5.5", "1.1.1.1", "8.8.8.8"} {
		require.Contains(
			t,
			summary,
			"Public CNAME via `"+resolver+"`: `docs.githubim.com.w.kunlunaq.com`",
		)
	}
}

func TestDocsCDNCertificateInspectRejectsAlibabaCDNCertificateMismatch(t *testing.T) {
	fixture := newDocsCDNCertificateFixture(t)
	fixture.publicRouteMode = "alibaba-cdn"
	fixture.aliDNSCNAME = "docs.githubim.com.w.kunlunaq.com."
	fixture.cloudflareCNAME = "docs.githubim.com.w.kunlunaq.com."
	fixture.googleCNAME = "docs.githubim.com.w.kunlunaq.com."

	output, err := fixture.run(t, "inspect", docsCDNInstalledCertificateSummary, false)
	require.Error(t, err, string(output))
	require.Contains(t, string(output), "the public CNAME route or trusted edge certificate does not match")
	require.Contains(t, readFile(t, fixture.githubSummaryPath), "Public edge verification: `failed`")
}

func TestDocsCDNCertificateInspectRejectsAmbiguousOrUnexpectedPublicDNS(t *testing.T) {
	testCases := []struct {
		name              string
		publicRouteMode   string
		aliDNSCNAME       string
		cloudflareCNAME   string
		googleCNAME       string
		wantConfiguration bool
	}{
		{
			name:            "missing answers",
			publicRouteMode: "github-pages-precutover",
		},
		{
			name:            "unknown answer",
			publicRouteMode: "github-pages-precutover",
			aliDNSCNAME:     "unexpected.example.",
			cloudflareCNAME: "unexpected.example.",
			googleCNAME:     "unexpected.example.",
		},
		{
			name:            "multiple answers",
			publicRouteMode: "github-pages-precutover",
			aliDNSCNAME:     "wukongim.github.io.\nsecondary.example.",
			cloudflareCNAME: "wukongim.github.io.",
			googleCNAME:     "wukongim.github.io.",
		},
		{
			name:            "mixed resolver answers",
			publicRouteMode: "github-pages-precutover",
			aliDNSCNAME:     "wukongim.github.io.",
			cloudflareCNAME: "docs.githubim.com.w.kunlunaq.com.",
			googleCNAME:     "wukongim.github.io.",
		},
		{
			name:            "pages mode after CDN cutover",
			publicRouteMode: "github-pages-precutover",
			aliDNSCNAME:     "docs.githubim.com.w.kunlunaq.com.",
			cloudflareCNAME: "docs.githubim.com.w.kunlunaq.com.",
			googleCNAME:     "docs.githubim.com.w.kunlunaq.com.",
		},
		{
			name:            "CDN mode before cutover",
			publicRouteMode: "alibaba-cdn",
			aliDNSCNAME:     "wukongim.github.io.",
			cloudflareCNAME: "wukongim.github.io.",
			googleCNAME:     "wukongim.github.io.",
		},
		{
			name:              "missing route mode",
			wantConfiguration: true,
		},
		{
			name:              "unknown route mode",
			publicRouteMode:   "automatic",
			wantConfiguration: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newDocsCDNCertificateFixture(t)
			fixture.publicRouteMode = testCase.publicRouteMode
			fixture.aliDNSCNAME = testCase.aliDNSCNAME
			fixture.cloudflareCNAME = testCase.cloudflareCNAME
			fixture.googleCNAME = testCase.googleCNAME

			output, err := fixture.run(t, "inspect", docsCDNInstalledCertificateSummary, false)
			require.Error(t, err, string(output))
			if testCase.wantConfiguration {
				require.Contains(t, string(output), "DOCS_CDN_PUBLIC_ROUTE_MODE must be")
				return
			}
			require.Contains(t, string(output), "the public CNAME route or trusted edge certificate does not match")
			summary := readFile(t, fixture.githubSummaryPath)
			require.Contains(t, summary, "Public edge verification: `failed`")
			if testCase.name == "mixed resolver answers" {
				require.Contains(
					t,
					summary,
					"Public CNAME via `1.1.1.1`: `docs.githubim.com.w.kunlunaq.com`",
				)
			}
		})
	}
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
	dnsLookupPath     string
	publicRouteMode   string
	aliDNSCNAME       string
	cloudflareCNAME   string
	googleCNAME       string
	publicFingerprint string
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
	writeDocsCDNExecutable(t, filepath.Join(binPath, "sleep"), `#!/bin/sh
exit 0
`)
	writeDocsCDNExecutable(t, filepath.Join(binPath, "dig"), `#!/bin/sh
resolver=''
for argument in "$@"; do
  case "$argument" in
    @*) resolver="${argument#@}" ;;
  esac
done
printf '@%s docs.githubim.com CNAME\n' "$resolver" >>"$DOCS_TEST_DNS_LOOKUP_PATH"
case "$resolver" in
  223.5.5.5) answer="$DOCS_TEST_ALIDNS_CNAME" ;;
  1.1.1.1) answer="$DOCS_TEST_CLOUDFLARE_CNAME" ;;
  8.8.8.8) answer="$DOCS_TEST_GOOGLE_CNAME" ;;
  *) exit 96 ;;
esac
if [ -n "$answer" ]; then
  printf '%s\n' "$answer"
fi
`)
	writeDocsCDNExecutable(t, filepath.Join(binPath, "openssl"), `#!/bin/sh
if [ "${1:-}" = "s_client" ]; then
  printf '%s\n' '-----BEGIN CERTIFICATE-----' 'fixture' '-----END CERTIFICATE-----'
  exit 0
fi
if [ "${1:-}" = "x509" ]; then
  for argument in "$@"; do
    if [ "$argument" = "-checkhost" ]; then
      exit 0
    fi
    if [ "$argument" = "-fingerprint" ]; then
      printf 'sha256 Fingerprint=%s\n' "$DOCS_TEST_PUBLIC_FINGERPRINT"
      exit 0
    fi
  done
fi
exit 98
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
		dnsLookupPath:     filepath.Join(temporaryDirectory, "dns-lookups"),
		publicRouteMode:   "github-pages-precutover",
		aliDNSCNAME:       "wukongim.github.io.",
		cloudflareCNAME:   "wukongim.github.io.",
		googleCNAME:       "wukongim.github.io.",
		publicFingerprint: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
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
		"DOCS_CDN_CNAME=docs.githubim.com.w.kunlunaq.com",
		"DOCS_CDN_DOMAIN=docs.githubim.com",
		"DOCS_CDN_ENABLED=true",
		"DOCS_CDN_PUBLIC_ROUTE_MODE=" + fixture.publicRouteMode,
		"DOCS_CERTIFICATE_HELPER=" + fixture.helperPath,
		"DOCS_CERT_FORCE_RENEW=" + forceRenewValue,
		"DOCS_TEST_INSPECTION_SUMMARY=" + summary,
		"DOCS_TEST_ALIDNS_CNAME=" + fixture.aliDNSCNAME,
		"DOCS_TEST_CLOUDFLARE_CNAME=" + fixture.cloudflareCNAME,
		"DOCS_TEST_DNS_LOOKUP_PATH=" + fixture.dnsLookupPath,
		"DOCS_TEST_GOOGLE_CNAME=" + fixture.googleCNAME,
		"DOCS_TEST_PUBLIC_FINGERPRINT=" + fixture.publicFingerprint,
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
