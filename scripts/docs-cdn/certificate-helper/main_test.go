package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestVerifyDelegationRequiresExactValidationTarget(t *testing.T) {
	lookup := func(_ context.Context, name string) (string, error) {
		if name != expectedChallengeCNAME {
			t.Fatalf("lookup name = %q", name)
		}
		return strings.ToUpper(expectedChallengeTarget), nil
	}
	if err := verifyDelegation(t.Context(), lookup); err != nil {
		t.Fatalf("verifyDelegation() error = %v", err)
	}
	wrong := func(context.Context, string) (string, error) { return "_acme-challenge.githubim.com.", nil }
	if err := verifyDelegation(t.Context(), wrong); err == nil {
		t.Fatal("verifyDelegation accepted the parent production zone")
	}
	failed := func(context.Context, string) (string, error) { return "", errors.New("dns unavailable") }
	if err := verifyDelegation(t.Context(), failed); err == nil {
		t.Fatal("verifyDelegation accepted a DNS lookup failure")
	}
}

func TestValidateEmailAllowsOrdinaryAddressesContainingRNT(t *testing.T) {
	for _, email := range []string{
		"tangtaoit@githubim.com",
		"reader@example.com",
		"notice@example.com",
		"team@example.com",
	} {
		t.Run(email, func(t *testing.T) {
			if err := validateEmail(email); err != nil {
				t.Fatalf("validateEmail(%q) error = %v", email, err)
			}
		})
	}
}

func TestValidateEmailRejectsUnsafeStorageValues(t *testing.T) {
	for _, email := range []string{
		"bad/name@example.com",
		"bad\\name@example.com",
		"bad\rname@example.com",
		"bad\nname@example.com",
		"bad\tname@example.com",
		"bad..name@example.com",
		"bad name@example.com",
		"<bad@example.com>",
	} {
		t.Run(strings.ReplaceAll(email, "\n", "newline"), func(t *testing.T) {
			if err := validateEmail(email); err == nil {
				t.Fatalf("validateEmail(%q) accepted an unsafe value", email)
			}
		})
	}
}

func TestRegisterAccountRequiresReviewedTermsBeforeNetworkOrMutation(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	requests := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests++
		return nil, errors.New("unexpected network request")
	})}
	err := registerAccount(t.Context(), "docs-ops@example.com", stateDir,
		"https://letsencrypt.org/documents/unreviewed.pdf", httpClient)
	if err == nil || !strings.Contains(err.Error(), "reviewed URL") {
		t.Fatalf("registerAccount() error = %v", err)
	}
	if requests != 0 {
		t.Fatalf("unreviewed terms caused %d network requests", requests)
	}
	if _, statErr := os.Stat(stateDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("rejected registration mutated state: %v", statErr)
	}
}

func TestRegisterAccountRejectsNonEmptyStateBeforeNetwork(t *testing.T) {
	stateDir := t.TempDir()
	if err := os.Chmod(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "existing"), []byte("do not overwrite"), 0o600); err != nil {
		t.Fatal(err)
	}
	requests := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests++
		return nil, errors.New("unexpected network request")
	})}
	err := registerAccount(t.Context(), "docs-ops@example.com", stateDir, expectedACMETerms, httpClient)
	if err == nil || !strings.Contains(err.Error(), "must be empty") {
		t.Fatalf("registerAccount() error = %v", err)
	}
	if requests != 0 {
		t.Fatalf("non-empty state caused %d network requests", requests)
	}
}

func TestRegisterAccountDoesNotFollowDirectoryRedirects(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	requests := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusFound,
			Status:     "302 Found",
			Header:     http.Header{"Location": []string{"https://attacker.example/directory"}},
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    request,
		}, nil
	})}
	err := registerAccount(t.Context(), "docs-ops@example.com", stateDir, expectedACMETerms, httpClient)
	if err == nil {
		t.Fatal("registerAccount followed or accepted a directory redirect")
	}
	if requests != 1 {
		t.Fatalf("directory redirect caused %d requests, want 1", requests)
	}
	if _, statErr := os.Stat(stateDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("directory redirect mutated state: %v", statErr)
	}
}

func TestAccountBundleRoundTripContainsOnlyValidatedLegoIdentity(t *testing.T) {
	email := "docs-ops@example.com"
	source := t.TempDir()
	accountDir := filepath.Join(source, "accounts", expectedACMEHost, email)
	if err := os.MkdirAll(filepath.Join(accountDir, "keys"), 0o700); err != nil {
		t.Fatal(err)
	}
	accountJSON := validAccountJSON(t, email)
	if err := os.WriteFile(filepath.Join(accountDir, "account.json"), accountJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	keyPEM := accountKeyPEM(t)
	if err := os.WriteFile(filepath.Join(accountDir, "keys", email+".key"), keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	// A certificate artifact in the source state must not enter the account bundle.
	if err := os.MkdirAll(filepath.Join(source, "certificates"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "certificates", "must-not-persist.key"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	bundlePath := filepath.Join(t.TempDir(), "account.bundle.b64")
	if err := packAccount(source, email, bundlePath); err != nil {
		t.Fatalf("packAccount() error = %v", err)
	}
	bundle, err := readBundle(bundlePath, email)
	if err != nil {
		t.Fatalf("readBundle() error = %v", err)
	}
	if strings.Contains(string(bundle.Account), "must-not-persist") || strings.Contains(bundle.AccountKeyPEM, "must-not-persist") {
		t.Fatal("bundle retained certificate state")
	}

	destination := filepath.Join(t.TempDir(), "lego-state")
	if err := restoreAccount(bundle, destination); err != nil {
		t.Fatalf("restoreAccount() error = %v", err)
	}
	gotAccount, err := os.ReadFile(filepath.Join(destination, "accounts", expectedACMEHost, email, "account.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(gotAccount)) != strings.TrimSpace(string(accountJSON)) {
		t.Fatalf("restored account = %s", gotAccount)
	}
	if _, err := os.Stat(filepath.Join(destination, "certificates")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("restore created certificate state: %v", err)
	}
	if err := restoreAccount(bundle, destination); err == nil {
		t.Fatal("restoreAccount overwrote an existing state directory")
	}
}

func TestReadBundleRejectsWrongServerContactAndWeakKey(t *testing.T) {
	email := "docs-ops@example.com"
	base := accountBundle{
		Schema: accountBundleSchema, Server: expectedACMEServer, Email: email,
		Account: validAccountJSON(t, email), AccountKeyPEM: string(accountKeyPEM(t)),
	}
	tests := []struct {
		name   string
		mutate func(*accountBundle)
	}{
		{name: "server", mutate: func(bundle *accountBundle) { bundle.Server = "https://acme-staging-v02.api.letsencrypt.org/directory" }},
		{name: "contact", mutate: func(bundle *accountBundle) { bundle.Account = validAccountJSON(t, "other@example.com") }},
		{name: "weak key", mutate: func(bundle *accountBundle) {
			key, err := rsa.GenerateKey(rand.Reader, 1024)
			if err != nil {
				t.Fatal(err)
			}
			bundle.AccountKeyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bundle := base
			test.mutate(&bundle)
			path := writeBundle(t, bundle)
			if _, err := readBundle(path, email); err == nil {
				t.Fatal("readBundle accepted an invalid identity")
			}
		})
	}
}

func TestValidateLegoAccountAllowsMissingContactOnly(t *testing.T) {
	email := "docs-ops@example.com"
	missingContact := map[string]any{
		"email": email,
		"registration": map[string]any{
			"body": map[string]any{"status": "valid"},
			"uri":  "https://" + expectedACMEHost + "/acme/acct/123456",
		},
	}
	data, err := json.Marshal(missingContact)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateLegoAccount(data, email); err != nil {
		t.Fatalf("validateLegoAccount() missing contact error = %v", err)
	}

	registration := missingContact["registration"].(map[string]any)
	body := registration["body"].(map[string]any)
	for _, contacts := range [][]string{
		{"mailto:other@example.com"},
		{"mailto:" + email, "mailto:other@example.com"},
	} {
		body["contact"] = contacts
		data, err = json.Marshal(missingContact)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateLegoAccount(data, email); err == nil {
			t.Fatalf("validateLegoAccount accepted contacts %v", contacts)
		}
	}
}

func TestValidateLegoAccountRequiresStrictValidAccountURI(t *testing.T) {
	email := "docs-ops@example.com"
	for _, test := range []struct {
		name   string
		status string
		uri    string
	}{
		{name: "pending status", status: "pending", uri: "https://" + expectedACMEHost + "/acme/acct/123456"},
		{name: "HTTP", status: "valid", uri: "http://" + expectedACMEHost + "/acme/acct/123456"},
		{name: "userinfo", status: "valid", uri: "https://operator@" + expectedACMEHost + "/acme/acct/123456"},
		{name: "wrong host", status: "valid", uri: "https://acme-staging-v02.api.letsencrypt.org/acme/acct/123456"},
		{name: "explicit port", status: "valid", uri: "https://" + expectedACMEHost + ":443/acme/acct/123456"},
		{name: "non-numeric ID", status: "valid", uri: "https://" + expectedACMEHost + "/acme/acct/account"},
		{name: "encoded ID", status: "valid", uri: "https://" + expectedACMEHost + "/acme/acct/%31%32%33%34%35%36"},
		{name: "query", status: "valid", uri: "https://" + expectedACMEHost + "/acme/acct/123456?source=test"},
		{name: "empty query", status: "valid", uri: "https://" + expectedACMEHost + "/acme/acct/123456?"},
		{name: "fragment", status: "valid", uri: "https://" + expectedACMEHost + "/acme/acct/123456#fragment"},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := map[string]any{
				"email": email,
				"registration": map[string]any{
					"body": map[string]any{"status": test.status},
					"uri":  test.uri,
				},
			}
			data, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if err := validateLegoAccount(data, email); err == nil {
				t.Fatalf("validateLegoAccount accepted status=%q URI=%q", test.status, test.uri)
			}
		})
	}
}

func TestValidateLegoAccountRequiresOrdersURIForSameAccount(t *testing.T) {
	email := "docs-ops@example.com"
	value := map[string]any{
		"email": email,
		"registration": map[string]any{
			"body": map[string]any{
				"status": "valid",
				"orders": "https://" + expectedACMEHost + "/acme/acct/654321/orders",
			},
			"uri": "https://" + expectedACMEHost + "/acme/acct/123456",
		},
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateLegoAccount(data, email); err == nil {
		t.Fatal("validateLegoAccount accepted orders URI for another account")
	}
}

func TestInspectCDNCertificateUsesThirtyDayThreshold(t *testing.T) {
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	certificatePEM, _ := issueTestCertificate(t, now, []string{expectedDomain}, 31*24*time.Hour)
	response := responseForCertificate(t, certificatePEM, "existing", "upload")
	summary, err := inspectCDNCertificate(response, now, false)
	if err != nil {
		t.Fatalf("inspectCDNCertificate() error = %v", err)
	}
	if summary.RenewalRequired || summary.DaysRemaining != 31 {
		t.Fatalf("31-day summary = %+v", summary)
	}
	if summary.DomainCNAMEStatus != "ok" || len(summary.Fingerprint) != 64 {
		t.Fatalf("31-day inspection identity = %+v", summary)
	}
	certificatePEM, _ = issueTestCertificate(t, now, []string{expectedDomain}, renewalWindow)
	response = responseForCertificate(t, certificatePEM, "existing", "upload")
	summary, err = inspectCDNCertificate(response, now, false)
	if err != nil {
		t.Fatalf("inspectCDNCertificate() error = %v", err)
	}
	if !summary.RenewalRequired || summary.DaysRemaining != 30 {
		t.Fatalf("30-day summary = %+v", summary)
	}
}

func TestInspectCDNCertificateAllowsOnlyExplicitMissingBootstrap(t *testing.T) {
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	var response cdnResponse
	if _, err := inspectCDNCertificate(response, now, false); err == nil {
		t.Fatal("inspectCDNCertificate accepted a missing certificate without bootstrap authorization")
	}
	summary, err := inspectCDNCertificate(response, now, true)
	if err != nil {
		t.Fatalf("inspectCDNCertificate() bootstrap error = %v", err)
	}
	if summary.CertificatePresent || !summary.RenewalRequired || summary.NotAfter != "missing" || summary.Fingerprint != "" || summary.DomainCNAMEStatus != "" {
		t.Fatalf("missing certificate summary = %+v", summary)
	}

	response.CertInfos.CertInfo = []cdnCertificateInfo{{
		DomainName: expectedDomain, ServerCertificateStatus: "off",
	}}
	if _, err := inspectCDNCertificate(response, now, true); err != nil {
		t.Fatalf("inspectCDNCertificate() disabled bootstrap error = %v", err)
	}
	response.CertInfos.CertInfo[0].DomainName = "other.example.com"
	if _, err := inspectCDNCertificate(response, now, true); err == nil {
		t.Fatal("inspectCDNCertificate accepted a missing certificate for another domain")
	}
}

func TestReadCDNResponseRequiresAnAuthenticEnvelope(t *testing.T) {
	writeResponse := func(value string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "response.json")
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	for _, value := range []string{
		`{}`,
		`{"RequestId":"request-1"}`,
		`{"RequestId":"request-1","CertInfos":{"CertInfo":null}}`,
		`{"RequestId":"bad request","CertInfos":{"CertInfo":[]}}`,
	} {
		if _, err := readCDNResponse(writeResponse(value)); err == nil {
			t.Fatalf("readCDNResponse accepted %s", value)
		}
	}
	response, err := readCDNResponse(writeResponse(
		`{"RequestId":"request-1","CertInfos":{"CertInfo":[]}}`,
	))
	if err != nil {
		t.Fatalf("readCDNResponse() error = %v", err)
	}
	if response.RequestID != "request-1" || len(response.CertInfos.CertInfo) != 0 {
		t.Fatalf("readCDNResponse() = %+v", response)
	}
}

func TestInspectCDNCertificateAllowsPreCutoverState(t *testing.T) {
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	certificatePEM, _ := issueTestCertificate(t, now, []string{expectedDomain}, 31*24*time.Hour)
	response := responseForCertificate(t, certificatePEM, "existing", "upload")
	response.CertInfos.CertInfo[0].Status = "cname_error"
	response.CertInfos.CertInfo[0].DomainCnameStatus = "cname_error"
	summary, err := inspectCDNCertificate(response, now, false)
	if err != nil {
		t.Fatalf("inspectCDNCertificate() pre-cutover error = %v", err)
	}
	if summary.DomainCNAMEStatus != "cname_error" || len(summary.Fingerprint) != 64 {
		t.Fatalf("pre-cutover inspection identity = %+v", summary)
	}
}

func TestValidCDNStateMatrix(t *testing.T) {
	tests := []struct {
		name              string
		certificateType   string
		certificateStatus string
		cnameStatus       string
		want              bool
	}{
		{name: "uploaded certificate omits status", certificateType: "upload", certificateStatus: "", cnameStatus: "ok", want: true},
		{name: "uploaded certificate is successful", certificateType: "upload", certificateStatus: "success", cnameStatus: "ok", want: true},
		{name: "free certificate is successful", certificateType: "free", certificateStatus: "success", cnameStatus: "ok", want: true},
		{name: "pre-cutover CNAME error", certificateType: "upload", certificateStatus: "cname_error", cnameStatus: "cname_error", want: true},
		{name: "pre-cutover top-domain CNAME error", certificateType: "upload", certificateStatus: "top_domain_cname_error", cnameStatus: "top_domain_cname_error", want: true},
		{name: "free certificate omits status", certificateType: "free", certificateStatus: "", cnameStatus: "ok", want: false},
		{name: "unknown certificate omits status", certificateType: "unknown", certificateStatus: "", cnameStatus: "ok", want: false},
		{name: "certificate type omitted with status", certificateType: "", certificateStatus: "", cnameStatus: "ok", want: false},
		{name: "certificate checking", certificateType: "upload", certificateStatus: "checking", cnameStatus: "ok", want: false},
		{name: "certificate applying", certificateType: "upload", certificateStatus: "applying", cnameStatus: "ok", want: false},
		{name: "certificate failed", certificateType: "upload", certificateStatus: "failed", cnameStatus: "ok", want: false},
		{name: "certificate does not support wildcard", certificateType: "upload", certificateStatus: "unsupport_wildcard", cnameStatus: "ok", want: false},
		{name: "CNAME status omitted", certificateType: "upload", certificateStatus: "", cnameStatus: "", want: false},
		{name: "CNAME checking", certificateType: "upload", certificateStatus: "success", cnameStatus: "checking", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			info := cdnCertificateInfo{
				CertType:          test.certificateType,
				Status:            test.certificateStatus,
				DomainCnameStatus: test.cnameStatus,
			}
			if got := validCDNState(info); got != test.want {
				t.Fatalf("validCDNState(%+v) = %t, want %t", info, got, test.want)
			}
		})
	}
}

func TestInspectCDNCertificateStatusMatrix(t *testing.T) {
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	certificatePEM, _ := issueTestCertificate(t, now, []string{expectedDomain}, 31*24*time.Hour)
	tests := []struct {
		name              string
		certificateType   string
		certificateStatus string
		cnameStatus       string
		wantOK            bool
	}{
		{name: "uploaded certificate omits status", certificateType: "upload", certificateStatus: "", cnameStatus: "ok", wantOK: true},
		{name: "free certificate is successful", certificateType: "free", certificateStatus: "success", cnameStatus: "ok", wantOK: true},
		{name: "free certificate omits status", certificateType: "free", certificateStatus: "", cnameStatus: "ok"},
		{name: "unknown certificate omits status", certificateType: "unknown", certificateStatus: "", cnameStatus: "ok"},
		{name: "uploaded certificate is checking", certificateType: "upload", certificateStatus: "checking", cnameStatus: "ok"},
		{name: "uploaded certificate is applying", certificateType: "upload", certificateStatus: "applying", cnameStatus: "ok"},
		{name: "uploaded certificate has failed", certificateType: "upload", certificateStatus: "failed", cnameStatus: "ok"},
		{name: "uploaded certificate does not support wildcard", certificateType: "upload", certificateStatus: "unsupport_wildcard", cnameStatus: "ok"},
		{name: "uploaded certificate has unknown CNAME state", certificateType: "upload", certificateStatus: "", cnameStatus: "checking"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := responseForCertificate(t, certificatePEM, "existing", test.certificateType)
			info := &response.CertInfos.CertInfo[0]
			info.Status = test.certificateStatus
			info.DomainCnameStatus = test.cnameStatus
			summary, err := inspectCDNCertificate(response, now, false)
			if test.wantOK {
				if err != nil {
					t.Fatalf("inspectCDNCertificate() error = %v", err)
				}
				if !summary.CertificatePresent || summary.DomainCNAMEStatus != test.cnameStatus {
					t.Fatalf("inspectCDNCertificate() summary = %+v", summary)
				}
				return
			}
			if err == nil {
				t.Fatal("inspectCDNCertificate accepted an invalid Alibaba CDN state")
			}
			assertSanitizedCDNStateError(t, err, *info)
		})
	}
}

func TestCDNStateDiagnosticIncludesOnlyApprovedFields(t *testing.T) {
	info := cdnCertificateInfo{
		CertDomainName:          "secret-cert-domain",
		CertExpireTime:          "secret-expiry",
		CertName:                "secret-name",
		CertType:                "upload",
		DomainCnameStatus:       "ok",
		DomainName:              expectedDomain,
		ServerCertificate:       "secret-certificate-pem",
		ServerCertificateStatus: "on",
		Status:                  "checking",
	}
	want := `domain="docs.githubim.com" https="on" cert_type="upload" status="checking" cname_status="ok"`
	if got := cdnStateDiagnostic(info); got != want {
		t.Fatalf("cdnStateDiagnostic() = %q, want %q", got, want)
	}
}

func TestValidateIssuedCertificateRequiresExactSANAndMatchingKey(t *testing.T) {
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	certificatePEM, keyPEM := issueTestCertificate(t, now, []string{expectedDomain}, 89*24*time.Hour)
	summary, err := validateIssuedCertificate(certificatePEM, keyPEM, now)
	if err != nil {
		t.Fatalf("validateIssuedCertificate() error = %v", err)
	}
	if !regexpCertificateName(summary.CertificateName) || summary.RenewalRequired {
		t.Fatalf("issued summary = %+v", summary)
	}

	extraSANPEM, extraSANKey := issueTestCertificate(t, now, []string{expectedDomain, "origin-docs.githubim.com"}, 89*24*time.Hour)
	if _, err := validateIssuedCertificate(extraSANPEM, extraSANKey, now); err == nil {
		t.Fatal("validateIssuedCertificate accepted an extra SAN")
	}
	_, wrongKey := issueTestCertificate(t, now, []string{expectedDomain}, 89*24*time.Hour)
	if _, err := validateIssuedCertificate(certificatePEM, wrongKey, now); err == nil {
		t.Fatal("validateIssuedCertificate accepted a mismatched private key")
	}
	shortPEM, shortKey := issueTestCertificate(t, now, []string{expectedDomain}, 29*24*time.Hour)
	if _, err := validateIssuedCertificate(shortPEM, shortKey, now); err == nil {
		t.Fatal("validateIssuedCertificate accepted a short-lived certificate")
	}
}

func TestVerifyCDNDeploymentRequiresExactUploadedCertificate(t *testing.T) {
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	certificatePEM, keyPEM := issueTestCertificate(t, now, []string{expectedDomain}, 89*24*time.Hour)
	summary, err := validateIssuedCertificate(certificatePEM, keyPEM, now)
	if err != nil {
		t.Fatal(err)
	}
	response := responseForCertificate(t, certificatePEM, summary.CertificateName, "upload")
	if err := verifyCDNDeployment(response, certificatePEM, summary.CertificateName); err != nil {
		t.Fatalf("verifyCDNDeployment() error = %v", err)
	}
	response.CertInfos.CertInfo[0].Status = "cname_error"
	response.CertInfos.CertInfo[0].DomainCnameStatus = "cname_error"
	if err := verifyCDNDeployment(response, certificatePEM, summary.CertificateName); err != nil {
		t.Fatalf("verifyCDNDeployment() pre-cutover error = %v", err)
	}
	response.CertInfos.CertInfo[0].CertExpireTime = now.Add(88 * 24 * time.Hour).Format(time.RFC3339)
	if err := verifyCDNDeployment(response, certificatePEM, summary.CertificateName); err == nil {
		t.Fatal("verifyCDNDeployment accepted a mismatched reported expiry")
	}
	response = responseForCertificate(t, certificatePEM, summary.CertificateName, "upload")
	response.CertInfos.CertInfo[0].CertType = "cas"
	if err := verifyCDNDeployment(response, certificatePEM, summary.CertificateName); err == nil {
		t.Fatal("verifyCDNDeployment accepted the wrong certificate type")
	}
}

func TestVerifyCDNDeploymentStatusMatrix(t *testing.T) {
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	certificatePEM, keyPEM := issueTestCertificate(t, now, []string{expectedDomain}, 89*24*time.Hour)
	summary, err := validateIssuedCertificate(certificatePEM, keyPEM, now)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name              string
		certificateType   string
		certificateStatus string
		cnameStatus       string
		wantOK            bool
	}{
		{name: "uploaded certificate omits status", certificateType: "upload", certificateStatus: "", cnameStatus: "ok", wantOK: true},
		{name: "uploaded certificate is successful", certificateType: "upload", certificateStatus: "success", cnameStatus: "ok", wantOK: true},
		{name: "pre-cutover CNAME error", certificateType: "upload", certificateStatus: "cname_error", cnameStatus: "cname_error", wantOK: true},
		{name: "pre-cutover top-domain CNAME error", certificateType: "upload", certificateStatus: "top_domain_cname_error", cnameStatus: "top_domain_cname_error", wantOK: true},
		{name: "free certificate omits status", certificateType: "free", certificateStatus: "", cnameStatus: "ok"},
		{name: "unknown certificate omits status", certificateType: "unknown", certificateStatus: "", cnameStatus: "ok"},
		{name: "uploaded certificate is checking", certificateType: "upload", certificateStatus: "checking", cnameStatus: "ok"},
		{name: "uploaded certificate is applying", certificateType: "upload", certificateStatus: "applying", cnameStatus: "ok"},
		{name: "uploaded certificate has failed", certificateType: "upload", certificateStatus: "failed", cnameStatus: "ok"},
		{name: "uploaded certificate does not support wildcard", certificateType: "upload", certificateStatus: "unsupport_wildcard", cnameStatus: "ok"},
		{name: "uploaded certificate has unknown CNAME state", certificateType: "upload", certificateStatus: "", cnameStatus: "checking"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := responseForCertificate(t, certificatePEM, summary.CertificateName, test.certificateType)
			info := &response.CertInfos.CertInfo[0]
			info.Status = test.certificateStatus
			info.DomainCnameStatus = test.cnameStatus
			err := verifyCDNDeployment(response, certificatePEM, summary.CertificateName)
			if test.wantOK {
				if err != nil {
					t.Fatalf("verifyCDNDeployment() error = %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("verifyCDNDeployment accepted an invalid Alibaba CDN state")
			}
			assertSanitizedCDNStateError(t, err, *info)
		})
	}
}

func assertSanitizedCDNStateError(t *testing.T, err error, info cdnCertificateInfo) {
	t.Helper()
	if !strings.Contains(err.Error(), cdnStateDiagnostic(info)) {
		t.Fatalf("error %q does not contain sanitized CDN state %q", err, cdnStateDiagnostic(info))
	}
	leakedCertificateName := info.CertName != "" && strings.Contains(err.Error(), info.CertName)
	if strings.Contains(err.Error(), "BEGIN CERTIFICATE") || leakedCertificateName {
		t.Fatalf("error leaked certificate material or certificate name: %q", err)
	}
}

func validAccountJSON(t *testing.T, email string) []byte {
	t.Helper()
	value := map[string]any{
		"email": email,
		"registration": map[string]any{
			"body": map[string]any{"status": "valid", "contact": []string{"mailto:" + email}},
			"uri":  "https://" + expectedACMEHost + "/acme/acct/123456",
		},
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func accountKeyPEM(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
}

func writeBundle(t *testing.T, bundle accountBundle) string {
	t.Helper()
	data, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "bundle.b64")
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(data)), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func issueTestCertificate(t *testing.T, now time.Time, dnsNames []string, validity time.Duration) ([]byte, []byte) {
	t.Helper()
	rootKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	rootTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test Root"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(365 * 24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, &rootKey.PublicKey, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	root, err := x509.ParseCertificate(rootDER)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: expectedDomain}, DNSNames: dnsNames,
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(validity),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, root, &leafKey.PublicKey, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	certificatePEM := append(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER})...,
	)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(leafKey)})
	return certificatePEM, keyPEM
}

func responseForCertificate(t *testing.T, certificatePEM []byte, name, certificateType string) cdnResponse {
	t.Helper()
	leaf, _, err := parseCertificateChain(certificatePEM)
	if err != nil {
		t.Fatal(err)
	}
	var response cdnResponse
	response.RequestID = "test-request-id"
	response.CertInfos.CertInfo = []cdnCertificateInfo{{
		CertDomainName: expectedDomain, CertExpireTime: leaf.NotAfter.UTC().Format(time.RFC3339),
		CertName: name, CertType: certificateType, DomainCnameStatus: "ok",
		DomainName: expectedDomain, ServerCertificate: string(certificatePEM),
		ServerCertificateStatus: "on", Status: "success",
	}}
	return response
}

func regexpCertificateName(value string) bool {
	parts := strings.Split(value, "-")
	return len(parts) == 5 && parts[0] == "wukongim" && parts[1] == "docs" && parts[2] == "le" && len(parts[3]) == 8 && len(parts[4]) == 12
}
