// Command certificate-helper enforces the fixed documentation certificate
// boundary around ACME account state, the lego client, and Alibaba CDN responses.
package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/acme"
)

const (
	expectedDomain          = "docs.githubim.com"
	expectedChallengeCNAME  = "_acme-challenge.docs.githubim.com."
	expectedChallengeTarget = "_acme-challenge.acme.docs.githubim.com."
	expectedACMEServer      = "https://acme-v02.api.letsencrypt.org/directory"
	expectedACMEHost        = "acme-v02.api.letsencrypt.org"
	expectedACMENewAccount  = "https://acme-v02.api.letsencrypt.org/acme/new-acct"
	expectedACMENewNonce    = "https://acme-v02.api.letsencrypt.org/acme/new-nonce"
	expectedACMENewOrder    = "https://acme-v02.api.letsencrypt.org/acme/new-order"
	expectedACMETerms       = "https://letsencrypt.org/documents/LE-SA-v1.8-July-06-2026.pdf"
	accountBundleSchema     = "wukongim/docs-acme-account-bundle/v1"
	maxBundleEncodedBytes   = 256 << 10
	maxBundleDecodedBytes   = 128 << 10
	renewalWindow           = 30 * 24 * time.Hour
	minimumIssuedValidity   = 30 * 24 * time.Hour
)

var accountURIPathPattern = regexp.MustCompile(`^/acme/acct/[0-9]+$`)
var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,128}$`)

type accountBundle struct {
	Schema        string          `json:"schema"`
	Server        string          `json:"server"`
	Email         string          `json:"email"`
	Account       json.RawMessage `json:"account"`
	AccountKeyPEM string          `json:"account_key_pem"`
}

type legoAccount struct {
	Email        string            `json:"email"`
	Registration *legoRegistration `json:"registration"`
}

type legoRegistration struct {
	Body legoAccountBody `json:"body"`
	URI  string          `json:"uri"`
}

type legoAccountBody struct {
	Status  string   `json:"status,omitempty"`
	Contact []string `json:"contact,omitempty"`
	Orders  string   `json:"orders,omitempty"`
}

type cdnResponse struct {
	RequestID string `json:"RequestId"`
	CertInfos struct {
		CertInfo []cdnCertificateInfo `json:"CertInfo"`
	} `json:"CertInfos"`
}

type cdnCertificateInfo struct {
	CertDomainName          string `json:"CertDomainName"`
	CertExpireTime          string `json:"CertExpireTime"`
	CertName                string `json:"CertName"`
	CertType                string `json:"CertType"`
	DomainCnameStatus       string `json:"DomainCnameStatus"`
	DomainName              string `json:"DomainName"`
	ServerCertificate       string `json:"ServerCertificate"`
	ServerCertificateStatus string `json:"ServerCertificateStatus"`
	Status                  string `json:"Status"`
}

type certificateSummary struct {
	CertificatePresent bool   `json:"certificate_present"`
	Fingerprint        string `json:"fingerprint"`
	DomainCNAMEStatus  string `json:"domain_cname_status"`
	NotAfter           string `json:"not_after"`
	SecondsRemaining   int64  `json:"seconds_remaining"`
	DaysRemaining      int64  `json:"days_remaining"`
	RenewalRequired    bool   `json:"renewal_required"`
	CertificateName    string `json:"certificate_name,omitempty"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "certificate helper: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("a command is required")
	}

	switch args[0] {
	case "verify-delegation":
		return runVerifyDelegation(args[1:], stdout)
	case "register-account":
		return runRegisterAccount(args[1:], stdout)
	case "restore-account":
		return runRestoreAccount(args[1:])
	case "pack-account":
		return runPackAccount(args[1:])
	case "inspect-cdn":
		return runInspectCDN(args[1:], stdout)
	case "validate-issued":
		return runValidateIssued(args[1:], stdout)
	case "verify-cdn":
		return runVerifyCDN(args[1:])
	default:
		return fmt.Errorf("unsupported command %q", args[0])
	}
}

func runRegisterAccount(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("register-account", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	email := flags.String("email", "", "ACME account email")
	stateDir := flags.String("state", "", "new lego state directory")
	acceptedTerms := flags.String("accept-terms-of-service", "", "reviewed Let's Encrypt terms URL")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *email == "" || *stateDir == "" || *acceptedTerms == "" {
		return errors.New("usage: register-account --email EMAIL --state ABSOLUTE_DIRECTORY --accept-terms-of-service EXACT_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := registerAccount(ctx, *email, *stateDir, *acceptedTerms, nil); err != nil {
		return err
	}
	_, err := fmt.Fprintln(stdout, "account_registered")
	return err
}

func runVerifyDelegation(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("verify-delegation", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return errors.New("usage: verify-delegation")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := verifyDelegation(ctx, net.DefaultResolver.LookupCNAME); err != nil {
		return err
	}
	_, err := fmt.Fprintln(stdout, "delegation_ok")
	return err
}

func runRestoreAccount(args []string) error {
	flags := flag.NewFlagSet("restore-account", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	bundlePath := flags.String("bundle", "", "base64-encoded account bundle")
	email := flags.String("email", "", "ACME account email")
	stateDir := flags.String("state", "", "new lego state directory")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *bundlePath == "" || *email == "" || *stateDir == "" {
		return errors.New("usage: restore-account --bundle FILE --email EMAIL --state ABSOLUTE_DIRECTORY")
	}
	bundle, err := readBundle(*bundlePath, *email)
	if err != nil {
		return err
	}
	return restoreAccount(bundle, *stateDir)
}

func runPackAccount(args []string) error {
	flags := flag.NewFlagSet("pack-account", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	email := flags.String("email", "", "ACME account email")
	stateDir := flags.String("state", "", "existing lego state directory")
	outputPath := flags.String("output", "", "new secret bundle file")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *email == "" || *stateDir == "" || *outputPath == "" {
		return errors.New("usage: pack-account --email EMAIL --state DIRECTORY --output NEW_FILE")
	}
	return packAccount(*stateDir, *email, *outputPath)
}

func runInspectCDN(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("inspect-cdn", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	responsePath := flags.String("response", "", "DescribeDomainCertificateInfo JSON")
	allowMissing := flags.Bool("allow-missing", false, "allow no current certificate for explicit bootstrap")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *responsePath == "" {
		return errors.New("usage: inspect-cdn --response FILE")
	}
	response, err := readCDNResponse(*responsePath)
	if err != nil {
		return err
	}
	summary, err := inspectCDNCertificate(response, time.Now(), *allowMissing)
	if err != nil {
		return err
	}
	return writeJSON(stdout, summary)
}

func runValidateIssued(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("validate-issued", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	certificatePath := flags.String("certificate", "", "issued full-chain PEM")
	keyPath := flags.String("key", "", "issued private key PEM")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *certificatePath == "" || *keyPath == "" {
		return errors.New("usage: validate-issued --certificate FILE --key FILE")
	}
	certificatePEM, err := readBoundedFile(*certificatePath, 256<<10)
	if err != nil {
		return fmt.Errorf("read issued certificate: %w", err)
	}
	keyPEM, err := readBoundedFile(*keyPath, 64<<10)
	if err != nil {
		return fmt.Errorf("read issued private key: %w", err)
	}
	summary, err := validateIssuedCertificate(certificatePEM, keyPEM, time.Now())
	if err != nil {
		return err
	}
	return writeJSON(stdout, summary)
}

func runVerifyCDN(args []string) error {
	flags := flag.NewFlagSet("verify-cdn", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	responsePath := flags.String("response", "", "DescribeDomainCertificateInfo JSON")
	certificatePath := flags.String("certificate", "", "expected full-chain PEM")
	certificateName := flags.String("certificate-name", "", "expected controlled certificate name")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *responsePath == "" || *certificatePath == "" || *certificateName == "" {
		return errors.New("usage: verify-cdn --response FILE --certificate FILE --certificate-name NAME")
	}
	response, err := readCDNResponse(*responsePath)
	if err != nil {
		return err
	}
	certificatePEM, err := readBoundedFile(*certificatePath, 256<<10)
	if err != nil {
		return fmt.Errorf("read expected certificate: %w", err)
	}
	return verifyCDNDeployment(response, certificatePEM, *certificateName)
}

func registerAccount(ctx context.Context, email, stateDir, acceptedTerms string, httpClient *http.Client) error {
	if acceptedTerms != expectedACMETerms {
		return fmt.Errorf("accepted ACME terms must exactly match the reviewed URL %q", expectedACMETerms)
	}
	if err := validateEmail(email); err != nil {
		return err
	}
	if err := validateEmptyStateDirectory(stateDir); err != nil {
		return err
	}

	accountKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate ACME account key: %w", err)
	}
	client := &acme.Client{
		Key:          accountKey,
		HTTPClient:   noRedirectHTTPClient(httpClient),
		DirectoryURL: expectedACMEServer,
		UserAgent:    "wukongim-docs-certificate-helper",
	}
	directory, err := client.Discover(ctx)
	if err != nil {
		return fmt.Errorf("discover fixed Let's Encrypt directory: %w", err)
	}
	if err := validateACMEDirectory(directory); err != nil {
		return err
	}

	registered, err := client.Register(ctx, &acme.Account{
		Contact: []string{"mailto:" + email},
	}, func(terms string) bool {
		return terms == expectedACMETerms
	})
	if err != nil {
		return fmt.Errorf("register Let's Encrypt account: %w", err)
	}
	if _, err := marshalLegoAccount(email, registered); err != nil {
		return fmt.Errorf("validate registered Let's Encrypt account: %w", err)
	}

	queried, err := client.GetReg(ctx, registered.URI)
	if err != nil {
		return fmt.Errorf("query registered Let's Encrypt account: %w", err)
	}
	if queried.URI != registered.URI {
		return errors.New("queried Let's Encrypt account URI does not match the registered identity")
	}
	accountJSON, err := marshalLegoAccount(email, queried)
	if err != nil {
		return fmt.Errorf("validate queried Let's Encrypt account: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(accountKey)
	if err != nil {
		return fmt.Errorf("encode ACME account key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := writeLegoAccountState(stateDir, email, accountJSON, keyPEM); err != nil {
		return err
	}
	return nil
}

func noRedirectHTTPClient(base *http.Client) *http.Client {
	if base == nil {
		base = http.DefaultClient
	}
	client := *base
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("ACME HTTP redirects are not allowed")
	}
	return &client
}

func validateACMEDirectory(directory acme.Directory) error {
	if directory.Terms != expectedACMETerms {
		return fmt.Errorf("Let's Encrypt directory terms are %q, want reviewed URL %q", directory.Terms, expectedACMETerms)
	}
	if directory.RegURL != expectedACMENewAccount || directory.NonceURL != expectedACMENewNonce || directory.OrderURL != expectedACMENewOrder {
		return errors.New("Let's Encrypt directory endpoints do not match the fixed production boundary")
	}
	if directory.ExternalAccountRequired {
		return errors.New("Let's Encrypt directory unexpectedly requires external account binding")
	}
	return nil
}

func marshalLegoAccount(email string, account *acme.Account) ([]byte, error) {
	if account == nil {
		return nil, errors.New("ACME account response is missing")
	}
	value := legoAccount{
		Email: email,
		Registration: &legoRegistration{
			Body: legoAccountBody{
				Status:  account.Status,
				Contact: append([]string(nil), account.Contact...),
				Orders:  account.OrdersURL,
			},
			URI: account.URI,
		},
	}
	data, err := json.MarshalIndent(value, "", "\t")
	if err != nil {
		return nil, fmt.Errorf("encode lego account JSON: %w", err)
	}
	if err := validateLegoAccount(data, email); err != nil {
		return nil, err
	}
	return data, nil
}

func writeLegoAccountState(stateDir, email string, accountJSON, keyPEM []byte) error {
	if err := validateEmptyStateDirectory(stateDir); err != nil {
		return err
	}
	accountDir := filepath.Join(stateDir, "accounts", expectedACMEHost, email)
	keysDir := filepath.Join(accountDir, "keys")
	if err := os.MkdirAll(keysDir, 0o700); err != nil {
		return fmt.Errorf("create lego account directory: %w", err)
	}
	if err := writeExclusive(filepath.Join(keysDir, email+".key"), keyPEM); err != nil {
		return fmt.Errorf("write lego account key: %w", err)
	}
	if err := writeExclusive(filepath.Join(accountDir, "account.json"), append(bytes.TrimSpace(accountJSON), '\n')); err != nil {
		return fmt.Errorf("write lego account JSON: %w", err)
	}
	return nil
}

func verifyDelegation(ctx context.Context, lookup func(context.Context, string) (string, error)) error {
	canonical, err := lookup(ctx, expectedChallengeCNAME)
	if err != nil {
		return fmt.Errorf("resolve fixed ACME challenge CNAME: %w", err)
	}
	if normalizeDNSName(canonical) != normalizeDNSName(expectedChallengeTarget) {
		return fmt.Errorf("ACME challenge CNAME resolves to %q, want %q", canonical, expectedChallengeTarget)
	}
	return nil
}

func normalizeDNSName(value string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
}

func readBundle(path, expectedEmail string) (accountBundle, error) {
	if err := validateEmail(expectedEmail); err != nil {
		return accountBundle{}, err
	}
	encoded, err := readBoundedFile(path, maxBundleEncodedBytes)
	if err != nil {
		return accountBundle{}, fmt.Errorf("read ACME account bundle: %w", err)
	}
	encoded = bytes.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\r', '\n':
			return -1
		default:
			return r
		}
	}, encoded)
	decoded := make([]byte, base64.StdEncoding.DecodedLen(len(encoded)))
	n, err := base64.StdEncoding.Strict().Decode(decoded, encoded)
	if err != nil {
		return accountBundle{}, errors.New("ACME account bundle is not strict base64")
	}
	decoded = decoded[:n]
	if len(decoded) == 0 || len(decoded) > maxBundleDecodedBytes {
		return accountBundle{}, errors.New("ACME account bundle has an invalid decoded size")
	}

	var bundle accountBundle
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bundle); err != nil {
		return accountBundle{}, fmt.Errorf("decode ACME account bundle: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return accountBundle{}, err
	}
	if bundle.Schema != accountBundleSchema || bundle.Server != expectedACMEServer || bundle.Email != expectedEmail {
		return accountBundle{}, errors.New("ACME account bundle does not match the fixed schema, server, and email")
	}
	if err := validateLegoAccount(bundle.Account, expectedEmail); err != nil {
		return accountBundle{}, err
	}
	if err := validateAccountKey([]byte(bundle.AccountKeyPEM)); err != nil {
		return accountBundle{}, err
	}
	return bundle, nil
}

func validateEmail(value string) error {
	if len(value) == 0 || len(value) > 254 || strings.ContainsAny(value, "/\\\r\n\t") || strings.Contains(value, "..") {
		return errors.New("ACME email is invalid for fixed lego storage")
	}
	address, err := mail.ParseAddress(value)
	if err != nil || address.Address != value || strings.ContainsAny(value, " <>") {
		return errors.New("ACME email must be one plain mailbox address")
	}
	return nil
}

func validateLegoAccount(raw json.RawMessage, expectedEmail string) error {
	if len(raw) == 0 || len(raw) > 64<<10 {
		return errors.New("lego account JSON has an invalid size")
	}
	var account legoAccount
	if err := json.Unmarshal(raw, &account); err != nil {
		return fmt.Errorf("decode lego account JSON: %w", err)
	}
	if account.Email != expectedEmail || account.Registration == nil || account.Registration.Body.Status != "valid" {
		return errors.New("lego account is not the expected valid identity")
	}
	registrationURL, err := url.Parse(account.Registration.URI)
	if err != nil || registrationURL.Scheme != "https" || registrationURL.Host != expectedACMEHost || registrationURL.User != nil || registrationURL.Opaque != "" || registrationURL.ForceQuery || !accountURIPathPattern.MatchString(registrationURL.EscapedPath()) || registrationURL.RawQuery != "" || registrationURL.Fragment != "" {
		return errors.New("lego account registration URI is outside the fixed Let's Encrypt account boundary")
	}
	if orders := account.Registration.Body.Orders; orders != "" {
		accountID := strings.TrimPrefix(registrationURL.Path, "/acme/acct/")
		if orders != "https://"+expectedACMEHost+"/acme/acct/"+accountID+"/orders" {
			return errors.New("lego account orders URI does not match the fixed account identity")
		}
	}
	wantContact := "mailto:" + expectedEmail
	if len(account.Registration.Body.Contact) > 0 && (len(account.Registration.Body.Contact) != 1 || account.Registration.Body.Contact[0] != wantContact) {
		return errors.New("lego account contact does not exactly match DOCS_ACME_EMAIL")
	}
	return nil
}

func validateAccountKey(keyPEM []byte) error {
	block, rest := pem.Decode(keyPEM)
	if block == nil || len(bytes.TrimSpace(rest)) != 0 {
		return errors.New("ACME account bundle must contain exactly one PEM private key")
	}
	key, err := parsePrivateKey(block)
	if err != nil {
		return fmt.Errorf("parse ACME account private key: %w", err)
	}
	switch value := key.(type) {
	case *ecdsa.PrivateKey:
		if value.Curve == nil || value.Curve.Params().BitSize < 256 {
			return errors.New("ACME account EC key is too small")
		}
	case *rsa.PrivateKey:
		if value.N.BitLen() < 2048 {
			return errors.New("ACME account RSA key is too small")
		}
	default:
		return fmt.Errorf("unsupported ACME account key type %T", key)
	}
	return nil
}

func parsePrivateKey(block *pem.Block) (any, error) {
	switch block.Type {
	case "EC PRIVATE KEY":
		return x509.ParseECPrivateKey(block.Bytes)
	case "RSA PRIVATE KEY":
		return x509.ParsePKCS1PrivateKey(block.Bytes)
	case "PRIVATE KEY":
		return x509.ParsePKCS8PrivateKey(block.Bytes)
	default:
		return nil, fmt.Errorf("unsupported PEM block %q", block.Type)
	}
}

func validateEmptyStateDirectory(stateDir string) error {
	if !filepath.IsAbs(stateDir) || filepath.Clean(stateDir) == string(filepath.Separator) {
		return errors.New("lego state directory must be absolute and non-root")
	}
	if info, err := os.Lstat(stateDir); err == nil {
		if !info.IsDir() {
			return errors.New("lego state path already exists and is not a directory")
		}
		if info.Mode().Perm()&0o077 != 0 {
			return errors.New("lego state directory must not grant group or other permissions")
		}
		entries, readErr := os.ReadDir(stateDir)
		if readErr != nil {
			return fmt.Errorf("inspect lego state directory: %w", readErr)
		}
		if len(entries) != 0 {
			return errors.New("lego state directory must be empty")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect lego state directory: %w", err)
	}
	return nil
}

func restoreAccount(bundle accountBundle, stateDir string) error {
	if err := validateEmptyStateDirectory(stateDir); err != nil {
		return err
	}

	accountDir := filepath.Join(stateDir, "accounts", expectedACMEHost, bundle.Email)
	keysDir := filepath.Join(accountDir, "keys")
	if err := os.MkdirAll(keysDir, 0o700); err != nil {
		return fmt.Errorf("create lego account directory: %w", err)
	}
	if err := writeExclusive(filepath.Join(accountDir, "account.json"), append(bytes.TrimSpace(bundle.Account), '\n')); err != nil {
		return fmt.Errorf("write lego account JSON: %w", err)
	}
	if err := writeExclusive(filepath.Join(keysDir, bundle.Email+".key"), []byte(bundle.AccountKeyPEM)); err != nil {
		return fmt.Errorf("write lego account key: %w", err)
	}
	return nil
}

func packAccount(stateDir, email, outputPath string) error {
	if err := validateEmail(email); err != nil {
		return err
	}
	if !filepath.IsAbs(stateDir) || !filepath.IsAbs(outputPath) || filepath.Clean(outputPath) == string(filepath.Separator) {
		return errors.New("pack-account paths must be absolute and the output must be non-root")
	}
	accountDir := filepath.Join(stateDir, "accounts", expectedACMEHost, email)
	accountJSON, err := readBoundedFile(filepath.Join(accountDir, "account.json"), 64<<10)
	if err != nil {
		return fmt.Errorf("read lego account JSON: %w", err)
	}
	if err := validateLegoAccount(accountJSON, email); err != nil {
		return err
	}
	accountKey, err := readBoundedFile(filepath.Join(accountDir, "keys", email+".key"), 64<<10)
	if err != nil {
		return fmt.Errorf("read lego account key: %w", err)
	}
	if err := validateAccountKey(accountKey); err != nil {
		return err
	}
	bundleJSON, err := json.Marshal(accountBundle{
		Schema: accountBundleSchema, Server: expectedACMEServer, Email: email,
		Account: json.RawMessage(accountJSON), AccountKeyPEM: string(accountKey),
	})
	if err != nil {
		return fmt.Errorf("encode ACME account bundle: %w", err)
	}
	encoded := make([]byte, base64.StdEncoding.EncodedLen(len(bundleJSON)))
	base64.StdEncoding.Encode(encoded, bundleJSON)
	encoded = append(encoded, '\n')
	if err := writeExclusive(outputPath, encoded); err != nil {
		return fmt.Errorf("write ACME account bundle: %w", err)
	}
	return nil
}

func writeExclusive(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func readCDNResponse(path string) (cdnResponse, error) {
	data, err := readBoundedFile(path, 512<<10)
	if err != nil {
		return cdnResponse{}, fmt.Errorf("read Alibaba CDN response: %w", err)
	}
	var envelope struct {
		RequestID string `json:"RequestId"`
		CertInfos *struct {
			CertInfo json.RawMessage `json:"CertInfo"`
		} `json:"CertInfos"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return cdnResponse{}, fmt.Errorf("decode Alibaba CDN response: %w", err)
	}
	if !requestIDPattern.MatchString(envelope.RequestID) || envelope.CertInfos == nil || len(envelope.CertInfos.CertInfo) == 0 {
		return cdnResponse{}, errors.New("Alibaba CDN response is missing its request ID or certificate list")
	}
	var certificates []cdnCertificateInfo
	if err := json.Unmarshal(envelope.CertInfos.CertInfo, &certificates); err != nil || certificates == nil {
		return cdnResponse{}, errors.New("Alibaba CDN response certificate list is not an array")
	}
	var response cdnResponse
	response.RequestID = envelope.RequestID
	response.CertInfos.CertInfo = certificates
	if len(response.CertInfos.CertInfo) > 1 {
		return cdnResponse{}, fmt.Errorf("Alibaba CDN returned %d certificates, want at most one", len(response.CertInfos.CertInfo))
	}
	return response, nil
}

func inspectCDNCertificate(response cdnResponse, now time.Time, allowMissing bool) (certificateSummary, error) {
	if len(response.CertInfos.CertInfo) == 0 {
		if !allowMissing {
			return certificateSummary{}, errors.New("Alibaba CDN has no active documentation certificate")
		}
		return certificateSummary{
			CertificatePresent: false, NotAfter: "missing", SecondsRemaining: 0,
			DaysRemaining: 0, RenewalRequired: true,
		}, nil
	}
	info := response.CertInfos.CertInfo[0]
	if info.ServerCertificateStatus == "off" && strings.TrimSpace(info.ServerCertificate) == "" {
		if info.DomainName != "" && info.DomainName != expectedDomain {
			return certificateSummary{}, errors.New("Alibaba CDN returned a missing certificate for the wrong domain")
		}
		if !allowMissing {
			return certificateSummary{}, errors.New("Alibaba CDN has no active documentation certificate")
		}
		return certificateSummary{
			CertificatePresent: false, NotAfter: "missing", SecondsRemaining: 0,
			DaysRemaining: 0, RenewalRequired: true,
		}, nil
	}
	if info.DomainName != expectedDomain || info.ServerCertificateStatus != "on" || !validCDNState(info) {
		return certificateSummary{}, errors.New("Alibaba CDN certificate is not active on the exact documentation domain")
	}
	leaf, _, err := parseCertificateChain([]byte(info.ServerCertificate))
	if err != nil {
		return certificateSummary{}, fmt.Errorf("parse Alibaba CDN certificate: %w", err)
	}
	if err := leaf.VerifyHostname(expectedDomain); err != nil {
		return certificateSummary{}, fmt.Errorf("Alibaba CDN certificate does not cover %s: %w", expectedDomain, err)
	}
	if err := verifyReportedExpiry(info.CertExpireTime, leaf.NotAfter); err != nil {
		return certificateSummary{}, err
	}
	remaining := leaf.NotAfter.Sub(now)
	days := int64(math.Floor(remaining.Hours() / 24))
	return certificateSummary{
		CertificatePresent: true, Fingerprint: certificateFingerprint(leaf), DomainCNAMEStatus: info.DomainCnameStatus,
		NotAfter:         leaf.NotAfter.UTC().Format(time.RFC3339),
		SecondsRemaining: int64(remaining.Seconds()), DaysRemaining: days,
		RenewalRequired: remaining <= renewalWindow,
	}, nil
}

func validateIssuedCertificate(certificatePEM, keyPEM []byte, now time.Time) (certificateSummary, error) {
	leaf, chain, err := parseCertificateChain(certificatePEM)
	if err != nil {
		return certificateSummary{}, err
	}
	if len(chain) < 2 {
		return certificateSummary{}, errors.New("issued certificate must include at least one intermediate")
	}
	if len(leaf.DNSNames) != 1 || leaf.DNSNames[0] != expectedDomain || len(leaf.IPAddresses) != 0 || len(leaf.EmailAddresses) != 0 || len(leaf.URIs) != 0 {
		return certificateSummary{}, errors.New("issued certificate SANs are not exactly docs.githubim.com")
	}
	if err := leaf.VerifyHostname(expectedDomain); err != nil {
		return certificateSummary{}, fmt.Errorf("issued certificate hostname validation failed: %w", err)
	}
	if leaf.NotBefore.After(now.Add(5*time.Minute)) || leaf.NotAfter.Sub(now) < minimumIssuedValidity {
		return certificateSummary{}, errors.New("issued certificate is not currently valid for at least 30 days")
	}
	for index := 0; index+1 < len(chain); index++ {
		if err := chain[index].CheckSignatureFrom(chain[index+1]); err != nil {
			return certificateSummary{}, fmt.Errorf("issued certificate chain link %d is invalid: %w", index, err)
		}
	}
	if _, err := tls.X509KeyPair(certificatePEM, keyPEM); err != nil {
		return certificateSummary{}, fmt.Errorf("issued certificate and private key do not match: %w", err)
	}
	fingerprint := certificateFingerprint(leaf)
	return certificateSummary{
		CertificatePresent: true, Fingerprint: fingerprint, NotAfter: leaf.NotAfter.UTC().Format(time.RFC3339),
		SecondsRemaining: int64(leaf.NotAfter.Sub(now).Seconds()),
		DaysRemaining:    int64(math.Floor(leaf.NotAfter.Sub(now).Hours() / 24)),
		RenewalRequired:  false,
		CertificateName:  "wukongim-docs-le-" + leaf.NotAfter.UTC().Format("20060102") + "-" + fingerprint[:12],
	}, nil
}

func verifyCDNDeployment(response cdnResponse, expectedPEM []byte, expectedName string) error {
	if !regexp.MustCompile(`^wukongim-docs-le-[0-9]{8}-[0-9a-f]{12}$`).MatchString(expectedName) {
		return errors.New("expected certificate name is outside the controlled namespace")
	}
	expectedLeaf, _, err := parseCertificateChain(expectedPEM)
	if err != nil {
		return fmt.Errorf("parse expected certificate: %w", err)
	}
	if len(response.CertInfos.CertInfo) != 1 {
		return errors.New("Alibaba CDN did not return exactly one deployed certificate")
	}
	info := response.CertInfos.CertInfo[0]
	if info.DomainName != expectedDomain || info.CertDomainName != expectedDomain || info.CertName != expectedName || info.CertType != "upload" || info.ServerCertificateStatus != "on" || !validCDNState(info) {
		return errors.New("Alibaba CDN has not activated the exact uploaded documentation certificate")
	}
	actualLeaf, _, err := parseCertificateChain([]byte(info.ServerCertificate))
	if err != nil {
		return fmt.Errorf("parse deployed Alibaba CDN certificate: %w", err)
	}
	if certificateFingerprint(actualLeaf) != certificateFingerprint(expectedLeaf) || !actualLeaf.NotAfter.Equal(expectedLeaf.NotAfter) {
		return errors.New("Alibaba CDN certificate fingerprint or expiry does not match the issued certificate")
	}
	if err := verifyReportedExpiry(info.CertExpireTime, expectedLeaf.NotAfter); err != nil {
		return err
	}
	return nil
}

func validCDNState(info cdnCertificateInfo) bool {
	validCNAMEState := info.DomainCnameStatus == "ok" || info.DomainCnameStatus == "cname_error" || info.DomainCnameStatus == "top_domain_cname_error"
	validCertificateState := info.Status == "success" || info.Status == "cname_error" || info.Status == "top_domain_cname_error"
	return validCNAMEState && validCertificateState
}

func verifyReportedExpiry(value string, expected time.Time) error {
	reported, err := time.Parse(time.RFC3339, value)
	if err != nil || !reported.Equal(expected) {
		return errors.New("Alibaba CDN reported expiry does not match the embedded certificate")
	}
	return nil
}

func parseCertificateChain(data []byte) (*x509.Certificate, []*x509.Certificate, error) {
	remaining := data
	var certificates []*x509.Certificate
	for {
		block, rest := pem.Decode(remaining)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			return nil, nil, fmt.Errorf("unexpected PEM block %q in certificate chain", block.Type)
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, nil, err
		}
		certificates = append(certificates, certificate)
		remaining = rest
	}
	if len(certificates) == 0 || len(bytes.TrimSpace(remaining)) != 0 {
		return nil, nil, errors.New("certificate data is not a clean PEM chain")
	}
	return certificates[0], certificates, nil
}

func certificateFingerprint(certificate *x509.Certificate) string {
	digest := sha256.Sum256(certificate.Raw)
	return hex.EncodeToString(digest[:])
}

func readBoundedFile(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("file exceeds the allowed size")
	}
	return data, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("ACME account bundle contains multiple JSON values")
		}
		return fmt.Errorf("decode trailing ACME account bundle data: %w", err)
	}
	return nil
}

func writeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}
