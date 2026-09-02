//go:build integration

package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestRegisterAccountWithFakeACMEServerCreatesOnlyAccountState(t *testing.T) {
	fake := newFakeACMEService(t)
	server := httptest.NewTLSServer(fake)
	t.Cleanup(server.Close)

	stateDir := filepath.Join(t.TempDir(), "state")
	email := "tangtaoit@githubim.com"
	fake.expectedEmail = email
	if err := registerAccount(t.Context(), email, stateDir, expectedACMETerms, routedACMEHTTPClient(t, server)); err != nil {
		t.Fatalf("registerAccount() error = %v", err)
	}
	if got := fake.directoryRequests.Load(); got != 1 {
		t.Fatalf("directory requests = %d, want 1", got)
	}
	if got := fake.registrationRequests.Load(); got != 1 {
		t.Fatalf("registration requests = %d, want 1", got)
	}
	if got := fake.lookupRequests.Load(); got != 1 {
		t.Fatalf("account lookup requests = %d, want 1", got)
	}
	if got := fake.newNonceRequests.Load(); got != 0 {
		t.Fatalf("newNonce requests = %d, want 0 because directory supplied a nonce", got)
	}
	if got := fake.newOrderRequests.Load(); got != 0 {
		t.Fatalf("newOrder requests = %d, want 0", got)
	}
	if got := fake.unexpectedRequests.Load(); got != 0 {
		t.Fatalf("unexpected ACME requests = %d, want 0", got)
	}

	accountDir := filepath.Join(stateDir, "accounts", expectedACMEHost, email)
	accountPath := filepath.Join(accountDir, "account.json")
	accountJSON, err := os.ReadFile(accountPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(accountJSON), `"contact"`) {
		t.Fatalf("account JSON fabricated contact omitted by server: %s", accountJSON)
	}
	if err := validateLegoAccount(accountJSON, email); err != nil {
		t.Fatalf("validateLegoAccount() error = %v", err)
	}
	var saved legoAccount
	if err := json.Unmarshal(accountJSON, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Registration == nil || saved.Registration.URI != fake.accountLocation || saved.Registration.Body.Status != "valid" || len(saved.Registration.Body.Contact) != 0 {
		t.Fatalf("saved account = %+v", saved)
	}

	keyPath := filepath.Join(accountDir, "keys", email+".key")
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	block, rest := pem.Decode(keyPEM)
	if block == nil || len(rest) != 0 {
		t.Fatal("registered key is not exactly one clean PEM block")
	}
	key, err := parsePrivateKey(block)
	if err != nil {
		t.Fatal(err)
	}
	ecKey, ok := key.(*ecdsa.PrivateKey)
	if !ok || ecKey.Curve != elliptic.P256() {
		t.Fatalf("registered key = %T, want EC P-256", key)
	}
	for _, path := range []string{stateDir, accountDir, filepath.Join(accountDir, "keys")} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Fatalf("directory %s mode = %#o, want 0700", path, got)
		}
	}
	for _, path := range []string{accountPath, keyPath} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("file %s mode = %#o, want 0600", path, got)
		}
	}
	if _, err := os.Stat(filepath.Join(stateDir, "certificates")); !os.IsNotExist(err) {
		t.Fatalf("registration created certificate state: %v", err)
	}

	bundlePath := filepath.Join(t.TempDir(), "account.bundle.b64")
	if err := packAccount(stateDir, email, bundlePath); err != nil {
		t.Fatalf("packAccount() after registration error = %v", err)
	}
	bundle, err := readBundle(bundlePath, email)
	if err != nil {
		t.Fatalf("readBundle() after registration error = %v", err)
	}
	restoredState := filepath.Join(t.TempDir(), "restored-state")
	if err := restoreAccount(bundle, restoredState); err != nil {
		t.Fatalf("restoreAccount() after registration error = %v", err)
	}
}

func TestRegisterAccountRejectsChangedLiveDirectoryBeforeRegistration(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*fakeACMEService)
	}{
		{name: "terms", mutate: func(fake *fakeACMEService) {
			fake.terms = "https://letsencrypt.org/documents/unreviewed.pdf"
		}},
		{name: "newAccount", mutate: func(fake *fakeACMEService) {
			fake.newAccountURL = "https://acme-staging-v02.api.letsencrypt.org/acme/new-acct"
		}},
		{name: "newNonce", mutate: func(fake *fakeACMEService) {
			fake.newNonceURL = "https://acme-staging-v02.api.letsencrypt.org/acme/new-nonce"
		}},
		{name: "newOrder", mutate: func(fake *fakeACMEService) {
			fake.newOrderURL = "https://acme-staging-v02.api.letsencrypt.org/acme/new-order"
		}},
		{name: "external account required", mutate: func(fake *fakeACMEService) {
			fake.externalAccountRequired = true
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := newFakeACMEService(t)
			test.mutate(fake)
			server := httptest.NewTLSServer(fake)
			t.Cleanup(server.Close)
			stateDir := filepath.Join(t.TempDir(), "state")
			err := registerAccount(t.Context(), "docs-ops@example.com", stateDir, expectedACMETerms, routedACMEHTTPClient(t, server))
			if err == nil {
				t.Fatal("registerAccount accepted changed production directory metadata")
			}
			if got := fake.directoryRequests.Load(); got != 1 {
				t.Fatalf("directory requests = %d, want 1", got)
			}
			if got := fake.registrationRequests.Load() + fake.lookupRequests.Load() + fake.newOrderRequests.Load(); got != 0 {
				t.Fatalf("changed directory caused %d post-discovery requests", got)
			}
			if _, statErr := os.Stat(stateDir); !os.IsNotExist(statErr) {
				t.Fatalf("changed directory mutated state: %v", statErr)
			}
		})
	}
}

func TestRegisterAccountRejectsInvalidQueriedIdentityWithoutWritingState(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*fakeACMEService)
	}{
		{name: "contact", mutate: func(fake *fakeACMEService) {
			fake.queryContact = []string{"mailto:other@example.com"}
		}},
		{name: "status", mutate: func(fake *fakeACMEService) {
			fake.queryStatus = "deactivated"
		}},
		{name: "location", mutate: func(fake *fakeACMEService) {
			fake.queryLocation = "https://" + expectedACMEHost + "/acme/acct/654321"
		}},
		{name: "malformed response", mutate: func(fake *fakeACMEService) {
			fake.malformedQuery = true
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := newFakeACMEService(t)
			test.mutate(fake)
			server := httptest.NewTLSServer(fake)
			t.Cleanup(server.Close)
			stateDir := filepath.Join(t.TempDir(), "state")
			err := registerAccount(t.Context(), "docs-ops@example.com", stateDir, expectedACMETerms, routedACMEHTTPClient(t, server))
			if err == nil {
				t.Fatal("registerAccount accepted an invalid queried identity")
			}
			if got := fake.registrationRequests.Load(); got != 1 {
				t.Fatalf("registration requests = %d, want 1", got)
			}
			if got := fake.lookupRequests.Load(); got != 1 {
				t.Fatalf("account lookup requests = %d, want 1", got)
			}
			if got := fake.newOrderRequests.Load(); got != 0 {
				t.Fatalf("newOrder requests = %d, want 0", got)
			}
			if _, statErr := os.Stat(stateDir); !os.IsNotExist(statErr) {
				t.Fatalf("invalid queried identity mutated state: %v", statErr)
			}
		})
	}
}

type fakeACMEService struct {
	t                       *testing.T
	terms                   string
	newAccountURL           string
	newNonceURL             string
	newOrderURL             string
	accountLocation         string
	queryLocation           string
	queryStatus             string
	queryContact            []string
	malformedQuery          bool
	expectedEmail           string
	externalAccountRequired bool
	directoryRequests       atomic.Int64
	registrationRequests    atomic.Int64
	lookupRequests          atomic.Int64
	newNonceRequests        atomic.Int64
	newOrderRequests        atomic.Int64
	unexpectedRequests      atomic.Int64
	mu                      sync.Mutex
	registrationJWK         string
}

func newFakeACMEService(t *testing.T) *fakeACMEService {
	t.Helper()
	location := "https://" + expectedACMEHost + "/acme/acct/123456"
	return &fakeACMEService{
		t:               t,
		terms:           expectedACMETerms,
		newAccountURL:   expectedACMENewAccount,
		newNonceURL:     expectedACMENewNonce,
		newOrderURL:     expectedACMENewOrder,
		accountLocation: location,
		queryLocation:   location,
		queryStatus:     "valid",
		expectedEmail:   "docs-ops@example.com",
	}
}

func (fake *fakeACMEService) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Replay-Nonce", fmt.Sprintf("nonce-%s-%d", strings.Trim(request.URL.Path, "/"), fake.directoryRequests.Load()+fake.registrationRequests.Load()+fake.lookupRequests.Load()+1))
	response.Header().Set("Content-Type", "application/json")
	switch request.URL.Path {
	case "/directory":
		fake.directoryRequests.Add(1)
		if request.Method != http.MethodGet {
			fake.t.Errorf("directory method = %s, want GET", request.Method)
		}
		writeFakeJSON(response, http.StatusOK, map[string]any{
			"newAccount": fake.newAccountURL,
			"newNonce":   fake.newNonceURL,
			"newOrder":   fake.newOrderURL,
			"meta": map[string]any{
				"termsOfService":          fake.terms,
				"externalAccountRequired": fake.externalAccountRequired,
			},
		})
	case "/acme/new-acct":
		payload, protected, err := decodeFakeACMEJWS(request.Body)
		if err != nil {
			fake.t.Errorf("decode newAccount JWS: %v", err)
			http.Error(response, "invalid JWS", http.StatusBadRequest)
			return
		}
		if request.Method != http.MethodPost {
			fake.t.Errorf("newAccount method = %s, want POST", request.Method)
		}
		if existing, _ := payload["onlyReturnExisting"].(bool); existing {
			fake.lookupRequests.Add(1)
			if len(payload) != 1 {
				fake.t.Errorf("account lookup payload = %#v, want only onlyReturnExisting", payload)
			}
			fake.requireSameJWK(protected)
			response.Header().Set("Location", fake.queryLocation)
			if fake.malformedQuery {
				response.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(response, "{")
				return
			}
			body := map[string]any{
				"status": fake.queryStatus,
				"orders": fake.accountLocation + "/orders",
			}
			if fake.queryContact != nil {
				body["contact"] = fake.queryContact
			}
			writeFakeJSON(response, http.StatusOK, body)
			return
		}

		fake.registrationRequests.Add(1)
		if agreed, _ := payload["termsOfServiceAgreed"].(bool); !agreed {
			fake.t.Errorf("registration payload did not accept fixed terms: %#v", payload)
		}
		contacts, ok := payload["contact"].([]any)
		if !ok || len(contacts) != 1 || contacts[0] != "mailto:"+fake.expectedEmail {
			fake.t.Errorf("registration contact = %#v", payload["contact"])
		}
		fake.rememberRegistrationJWK(protected)
		response.Header().Set("Location", fake.accountLocation)
		writeFakeJSON(response, http.StatusCreated, map[string]any{
			"status": "valid",
			"orders": fake.accountLocation + "/orders",
		})
	case "/acme/new-order":
		fake.newOrderRequests.Add(1)
		http.Error(response, "newOrder must not be called", http.StatusInternalServerError)
	case "/acme/new-nonce":
		fake.newNonceRequests.Add(1)
		if request.Method != http.MethodHead {
			fake.t.Errorf("newNonce method = %s, want HEAD", request.Method)
		}
		response.WriteHeader(http.StatusOK)
	default:
		fake.unexpectedRequests.Add(1)
		http.NotFound(response, request)
	}
}

func (fake *fakeACMEService) rememberRegistrationJWK(protected map[string]any) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	encoded, err := json.Marshal(protected["jwk"])
	if err != nil {
		fake.t.Errorf("marshal registration JWK: %v", err)
		return
	}
	fake.registrationJWK = string(encoded)
}

func (fake *fakeACMEService) requireSameJWK(protected map[string]any) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	encoded, err := json.Marshal(protected["jwk"])
	if err != nil {
		fake.t.Errorf("marshal lookup JWK: %v", err)
		return
	}
	if fake.registrationJWK == "" || string(encoded) != fake.registrationJWK {
		fake.t.Errorf("account lookup did not use the registration key")
	}
}

func decodeFakeACMEJWS(body io.Reader) (map[string]any, map[string]any, error) {
	var envelope struct {
		Protected string `json:"protected"`
		Payload   string `json:"payload"`
	}
	if err := json.NewDecoder(io.LimitReader(body, 64<<10)).Decode(&envelope); err != nil {
		return nil, nil, err
	}
	decode := func(value string) (map[string]any, error) {
		data, err := base64.RawURLEncoding.DecodeString(value)
		if err != nil {
			return nil, err
		}
		var result map[string]any
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, err
		}
		return result, nil
	}
	payload, err := decode(envelope.Payload)
	if err != nil {
		return nil, nil, err
	}
	protected, err := decode(envelope.Protected)
	if err != nil {
		return nil, nil, err
	}
	if protected["url"] != expectedACMENewAccount {
		return nil, nil, fmt.Errorf("protected URL = %v", protected["url"])
	}
	return payload, protected, nil
}

func writeFakeJSON(response http.ResponseWriter, status int, value any) {
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func routedACMEHTTPClient(t *testing.T, server *httptest.Server) *http.Client {
	t.Helper()
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := server.Client().Transport
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Scheme != "https" || request.URL.Host != expectedACMEHost {
			t.Errorf("ACME request escaped production origin: %s", request.URL)
		}
		clone := request.Clone(request.Context())
		redirectedURL := *request.URL
		redirectedURL.Scheme = target.Scheme
		redirectedURL.Host = target.Host
		clone.URL = &redirectedURL
		clone.Host = request.URL.Host
		return transport.RoundTrip(clone)
	})}
}
