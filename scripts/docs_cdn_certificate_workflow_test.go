package scripts_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDocsCDNCertificateWorkflowIsDefaultDisabledAndNarrowlyAuthorized(t *testing.T) {
	workflow := readFile(
		t,
		filepath.Join(repoRoot(t), ".github", "workflows", "docs-cdn-certificate.yml"),
	)

	for _, want := range []string{
		"name: Safety Automation - Renew Documentation CDN Certificate",
		`cron: "17 0,12 * * *"`,
		"workflow_dispatch:",
		"force_renew:",
		"if: github.ref == 'refs/heads/main' && vars.DOCS_CDN_ENABLED == 'true'",
		"name: docs-cdn-certificate",
		"DOCS_CDN_DOMAIN: ${{ vars.DOCS_CDN_DOMAIN }}",
		"DOCS_CDN_CERTIFICATE_ROLE_ARN: ${{ vars.DOCS_CDN_CERTIFICATE_ROLE_ARN }}",
		"DOCS_CDN_REFRESH_ROLE_ARN: ${{ vars.DOCS_CDN_REFRESH_ROLE_ARN }}",
		"DOCS_CDN_OIDC_PROVIDER_ARN: ${{ vars.DOCS_CDN_OIDC_PROVIDER_ARN }}",
		"DOCS_CDN_OIDC_AUDIENCE: ${{ vars.DOCS_CDN_OIDC_AUDIENCE }}",
		"DOCS_ACME_EMAIL: ${{ vars.DOCS_ACME_EMAIL }}",
		`[[ "$DOCS_CDN_CERTIFICATE_ROLE_ARN" =~ ^acs:ram::([0-9]+):role/[A-Za-z0-9_-]+$ ]]`,
		`[[ "$DOCS_CDN_REFRESH_ROLE_ARN" =~ ^acs:ram::([0-9]+):role/[A-Za-z0-9_-]+$ ]]`,
		`[[ "$DOCS_CDN_OIDC_PROVIDER_ARN" =~ ^acs:ram::([0-9]+):oidc-provider/[A-Za-z0-9._-]+$ ]]`,
		`[[ "$role_account" == "$refresh_role_account" && "$role_account" == "$provider_account" ]]`,
		`[[ "$DOCS_CDN_CERTIFICATE_ROLE_ARN" != "$DOCS_CDN_REFRESH_ROLE_ARN" ]]`,
		"aliyun/configure-aliyun-credentials-action@1e5248c8d5d93a8781ac344a68e19a43341e79e6",
		"role-session-name: wukongim-docs-certificate-${{ github.run_id }}",
		"role-session-expiration: 3600",
		"./scripts/docs-cdn/install-aliyun-cli.sh",
		"./scripts/docs-cdn/install-lego.sh",
		"./scripts/docs-cdn/certificate.sh inspect",
		"./scripts/docs-cdn/certificate.sh rotate",
		"fingerprint: ${{ steps.current.outputs.fingerprint }}",
		"domain_cname_status: ${{ steps.current.outputs.domain_cname_status }}",
	} {
		require.Contains(t, workflow, want)
	}
	require.Equal(t, 1, strings.Count(workflow, "id-token: write"))
	require.Equal(t, 2, strings.Count(workflow, "issues: write"))
	require.Equal(t, 1, strings.Count(workflow, "secrets.DOCS_ACME_ACCOUNT_BUNDLE_B64"))
	for _, forbidden := range []string{
		"secrets.ALIBABA",
		"DOCS_CDN_ACCESS_KEY",
		"ALIBABA_CLOUD_ACCESS_KEY_ID: ${{ secrets.",
		"ALIBABA_CLOUD_ACCESS_KEY_SECRET: ${{ secrets.",
	} {
		require.NotContains(t, workflow, forbidden)
	}
}

func TestDocsCDNCertificateWorkflowInstallsACMEClientOnlyForRenewalBeforeSecretUse(t *testing.T) {
	workflow := readFile(
		t,
		filepath.Join(repoRoot(t), ".github", "workflows", "docs-cdn-certificate.yml"),
	)

	inspect := strings.Index(workflow, "- name: Inspect the active CDN certificate")
	install := strings.Index(workflow, "- name: Install the pinned integrity-checked ACME client")
	issue := strings.Index(workflow, "- name: Issue with Let's Encrypt DNS-01")
	secret := strings.Index(workflow, "DOCS_ACME_ACCOUNT_BUNDLE_B64: ${{ secrets.DOCS_ACME_ACCOUNT_BUNDLE_B64 }}")
	require.NotEqual(t, -1, inspect)
	require.Greater(t, install, inspect)
	require.Greater(t, issue, install)
	require.Greater(t, secret, install)

	installStep := workflow[install:issue]
	for _, want := range []string{
		"!cancelled() && steps.current.outputs.certificate_present != ''",
		"steps.current.outputs.renewal_required == 'true'",
		"github.event_name == 'workflow_dispatch' && inputs.force_renew",
		"unset \\\n",
		"ALIBABA_CLOUD_ACCESS_KEY_ID ALIBABA_CLOUD_ACCESS_KEY_SECRET ALIBABA_CLOUD_SECURITY_TOKEN",
		"ALICLOUD_ACCESS_KEY ALICLOUD_SECRET_KEY ALICLOUD_SECURITY_TOKEN",
		"ALIBABACLOUD_ACCESS_KEY_ID ALIBABACLOUD_ACCESS_KEY_SECRET ALIBABACLOUD_SECURITY_TOKEN",
		"./scripts/docs-cdn/install-lego.sh",
	} {
		require.Contains(t, installStep, want)
	}
	require.NotContains(t, installStep, "DOCS_ACME_ACCOUNT_BUNDLE_B64")
	require.NotContains(t, workflow[:install], "install-lego.sh")
	require.Contains(t, workflow[issue:], "!cancelled() && steps.lego.outcome == 'success'")
}

func TestDocsCDNCertificateWorkflowRequiresHealthyInspectionOrSuccessfulRepair(t *testing.T) {
	workflow := readFile(
		t,
		filepath.Join(repoRoot(t), ".github", "workflows", "docs-cdn-certificate.yml"),
	)

	inspect := strings.Index(workflow, "- name: Inspect the active CDN certificate")
	install := strings.Index(workflow, "- name: Install the pinned integrity-checked ACME client")
	rotate := strings.Index(workflow, "- name: Issue with Let's Encrypt DNS-01")
	gate := strings.Index(workflow, "- name: Require a healthy inspection or successful repair")
	report := strings.Index(workflow, "\n  report_failure:")
	require.NotEqual(t, -1, inspect)
	require.Greater(t, install, inspect)
	require.Greater(t, rotate, install)
	require.Greater(t, gate, rotate)
	require.Greater(t, report, gate)

	inspectStep := workflow[inspect:install]
	require.Contains(t, inspectStep, "continue-on-error: true")
	rotationStep := workflow[rotate:gate]
	require.Contains(t, rotationStep, "id: rotation")
	require.NotContains(t, rotationStep, "continue-on-error:")

	gateStep := workflow[gate:report]
	for _, want := range []string{
		"if: always()",
		"INSPECTION_OUTCOME: ${{ steps.current.outcome }}",
		"ROTATION_OUTCOME: ${{ steps.rotation.outcome }}",
		`if [[ "$INSPECTION_OUTCOME" == success ]]; then`,
		`if [[ "$INSPECTION_OUTCOME" == failure && "$ROTATION_OUTCOME" == success ]]; then`,
		`exit 1`,
	} {
		require.Contains(t, gateStep, want)
	}
}

func TestDocsCDNCertificateWorkflowDeduplicatesAndResolvesExpiryAlerts(t *testing.T) {
	workflow := readFile(
		t,
		filepath.Join(repoRoot(t), ".github", "workflows", "docs-cdn-certificate.yml"),
	)

	for _, want := range []string{
		`readonly title="[docs-cdn] certificate rotation failed"`,
		`severity="no-current-certificate"`,
		`severity="expiry-14d"`,
		`severity="expiry-7d"`,
		`severity="expiry-3d"`,
		`severity="expired"`,
		"gh issue create",
		"gh issue edit",
		"gh issue comment",
		"gh issue close",
	} {
		require.Contains(t, workflow, want)
	}
}

func TestDocsCDNCertificateHelperUsesPinnedUpstreamACMEAndExactCDNMutation(t *testing.T) {
	rotation := readFile(t, filepath.Join(repoRoot(t), "scripts", "docs-cdn", "certificate.sh"))

	for _, want := range []string{
		`readonly expected_domain="docs.githubim.com"`,
		`readonly expected_acme_server="https://acme-v02.api.letsencrypt.org/directory"`,
		`readonly renewal_window_seconds="$((30 * 24 * 60 * 60))"`,
		`helper_arguments+=(--allow-missing)`,
		`certificate_present`,
		`verify-delegation`,
		`--dns alidns`,
		`--key-type rsa2048`,
		`SetCdnDomainSSLCertificate`,
		`--DomainName "$expected_domain"`,
		`--CertType upload`,
		`--SSLProtocol on`,
		`.RequestId | strings | select(test("^[A-Za-z0-9-]{1,128}$"))`,
		`verify-cdn`,
		`DomainCnameStatus`,
		`verify_public_edge()`,
		`assess_public_edge()`,
		`assess_public_edge "$inspection_fingerprint" "$inspection_cname_status" 3 10`,
		`assess_public_edge "$expected_fingerprint" "$cdn_cname_status" 40 15`,
		`-verify_hostname "$expected_domain"`,
		`-verify_return_error`,
		`-CApath /etc/ssl/certs`,
		`skipped-public-dns-not-on-alibaba-cdn`,
	} {
		require.Contains(t, rotation, want)
	}
	require.Equal(t, 1, strings.Count(rotation, "openssl s_client"), "inspect and rotation must share one edge verifier")
	require.Equal(t, 5, strings.Count(rotation, "| booleans | tostring"), "every required boolean must accept false without weakening type checks")
	require.NotContains(t, rotation, "| booleans'", "raw boolean outputs make jq -e reject false")
	inspectStart := strings.Index(rotation, `if [[ "$operation" == inspect ]]`)
	rotateStart := strings.Index(rotation, "command -v lego")
	require.NotEqual(t, -1, inspectStart)
	require.Greater(t, rotateStart, inspectStart)
	inspectBlock := rotation[inspectStart:rotateStart]
	writeOutputs := strings.Index(inspectBlock, `write_inspection_outputs "$inspection_summary"`)
	assessEdge := strings.Index(inspectBlock, `assess_public_edge`)
	require.NotEqual(t, -1, writeOutputs)
	require.NotEqual(t, -1, assessEdge)
	require.Less(t, writeOutputs, assessEdge, "expiry outputs must survive a later public edge failure")
}

func TestDocsCDNLegoInstallerPinsSourceAndRequiresExactVersionOutput(t *testing.T) {
	installer := readFile(t, filepath.Join(repoRoot(t), "scripts", "docs-cdn", "install-lego.sh"))

	for _, want := range []string{
		`readonly lego_version="v4.35.2"`,
		`readonly go_toolchain="go1.25.11"`,
		`readonly lego_sum="h1:uVQg+KC/yj9R2g7Q9W5wDqhvQvxV5SMu5eqFVoN5xZU="`,
		`readonly lego_go_mod_sum="h1:pX2jN5n8OphMGY1IaMjYm5DAEzguBaKRt8AvJAgJXpc="`,
		`go mod download -json "${lego_module}@${lego_version}"`,
		`go install "${lego_module}/cmd/lego@${lego_version}"`,
		`readonly target_goos="$(go env GOOS)"`,
		`readonly target_goarch="$(go env GOARCH)"`,
		`readonly expected_version_output="lego version ${lego_version}+dev-release ${target_goos}/${target_goarch}"`,
		`[[ "$version_output" == "$expected_version_output" ]]`,
		`export GOWORK="off"`,
	} {
		require.Contains(t, installer, want)
	}
	require.NotContains(t, installer, `== *"lego version`)
	require.NotContains(t, installer, "latest")
	require.NotContains(t, installer, "sudo")
}

func TestDocsCDNAcmeAccountBootstrapUsesCertificateHelper(t *testing.T) {
	runbook := readFile(
		t,
		filepath.Join(repoRoot(t), "docs", "superpowers", "runbooks", "docs-alibaba-cdn.md"),
	)
	bootstrapStart := strings.Index(runbook, "### Initialize the encrypted ACME account identity")
	bootstrapEnd := strings.Index(runbook, "## External target configuration")
	require.NotEqual(t, -1, bootstrapStart)
	require.Greater(t, bootstrapEnd, bootstrapStart)
	bootstrap := runbook[bootstrapStart:bootstrapEnd]

	for _, want := range []string{
		`"$bootstrap_dir/certificate-helper" register-account \`,
		`--email "$DOCS_ACME_EMAIL" \`,
		`--state "$bootstrap_dir/state" \`,
		`--accept-terms-of-service \`,
		`"https://letsencrypt.org/documents/LE-SA-v1.8-July-06-2026.pdf"`,
		`"$bootstrap_dir/certificate-helper" pack-account \`,
	} {
		require.Contains(t, bootstrap, want)
	}

	build := strings.Index(bootstrap, `go build -trimpath`)
	register := strings.Index(bootstrap, `"$bootstrap_dir/certificate-helper" register-account`)
	pack := strings.Index(bootstrap, `"$bootstrap_dir/certificate-helper" pack-account`)
	require.NotEqual(t, -1, build)
	require.NotEqual(t, -1, register)
	require.Greater(t, register, build, "the helper must be built before account registration")
	require.Greater(t, pack, register, "the validated registered account must be packed only after registration")
	require.NotRegexp(t, `(?m)^[ \t]*("[^"\n]*/lego"|[^ \t\n]*/lego|lego)([ \t]|$)`, bootstrap)
	require.NotContains(t, bootstrap, `--accept-tos register`)
}
