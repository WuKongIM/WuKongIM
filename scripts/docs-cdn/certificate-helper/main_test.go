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
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
