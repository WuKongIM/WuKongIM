package main

import (
	"context"
	"crypto/rsa"
	"errors"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/app"
	cloudanalysisinfra "github.com/WuKongIM/WuKongIM/internal/infra/cloudanalysis"
	cloudanalysis "github.com/WuKongIM/WuKongIM/internal/usecase/cloudanalysis"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestSelfCheckProbesDependenciesAndReportsOnlyFailures(t *testing.T) {
	cfg := serveConfig{gateway: appCloudAnalysisGatewayConfigForSelfCheck()}
	requested := make(map[string]bool)
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requested[request.URL.String()] = true
		switch request.URL.String() {
		case "http://prometheus.test/-/ready", "http://node-3.test/readyz":
			if request.Method != http.MethodGet {
				t.Fatalf("%s method = %s, want GET", request.URL, request.Method)
			}
			return response(http.StatusOK, "ready"), nil
		case "http://node-1.test/readyz":
			return response(http.StatusServiceUnavailable, "not ready"), nil
		case "http://node-2.test/readyz":
			return nil, errors.New("node unavailable")
		case "http://manager.test/manager/login":
			if request.Method != http.MethodPost {
				t.Fatalf("manager method = %s, want POST", request.Method)
			}
			if got := request.Header.Get("Content-Type"); got != "application/json" {
				t.Fatalf("manager content type = %q", got)
			}
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatalf("read manager body: %v", err)
			}
			if got := string(body); got != `{"password":"password-1","username":"operator"}` {
				t.Fatalf("manager body = %s", got)
			}
			return response(http.StatusNoContent, ""), nil
		default:
			t.Fatalf("unexpected self-check request %s", request.URL)
			return nil, errors.New("unexpected request")
		}
	})}

	failures := selfCheckWithClient(context.Background(), cfg, client)
	sort.Strings(failures)
	if got, want := strings.Join(failures, ","), "node-1,node-2"; got != want {
		t.Fatalf("self-check failures = %q, want %q", got, want)
	}
	if len(requested) != 5 {
		t.Fatalf("self-check request count = %d, want 5", len(requested))
	}
}

func TestSelfCheckRejectsMalformedDependencyURL(t *testing.T) {
	cfg := serveConfig{gateway: appCloudAnalysisGatewayConfigForSelfCheck()}
	cfg.gateway.ManagerAuth = cloudanalysisManagerAuthZero()
	cfg.gateway.NodeAPIBaseURLs = map[uint64]string{7: "://invalid"}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "http://prometheus.test/-/ready" {
			t.Fatalf("unexpected request %s", request.URL)
		}
		return response(http.StatusOK, "ready"), nil
	})}

	if got := selfCheckWithClient(context.Background(), cfg, client); len(got) != 1 || got[0] != "node-7" {
		t.Fatalf("self-check failures = %#v, want [node-7]", got)
	}
}

func TestSelfCheckDefaultClientRejectsMalformedURLsWithoutDialing(t *testing.T) {
	cfg := serveConfig{gateway: app.CloudAnalysisGatewayConfig{
		PrometheusBaseURL: "://invalid",
		NodeAPIBaseURLs:   map[uint64]string{1: "://invalid"},
	}}
	failures := selfCheck(context.Background(), cfg)
	sort.Strings(failures)
	if got := strings.Join(failures, ","); got != "node-1,prometheus" {
		t.Fatalf("self-check failures = %q", got)
	}
}

func response(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func appCloudAnalysisGatewayConfigForSelfCheck() app.CloudAnalysisGatewayConfig {
	return app.CloudAnalysisGatewayConfig{
		RunState:          cloudanalysis.RunState("running"),
		PrometheusBaseURL: "http://prometheus.test",
		ManagerBaseURL:    "http://manager.test",
		ManagerAuth: cloudanalysisinfra.ManagerAuth{
			Username: "operator",
			Password: "password-1",
		},
		NodeAPIBaseURLs: map[uint64]string{
			1: "http://node-1.test",
			2: "http://node-2.test",
			3: "http://node-3.test",
		},
	}
}

func cloudanalysisManagerAuthZero() cloudanalysisinfra.ManagerAuth {
	return cloudanalysisinfra.ManagerAuth{}
}

func TestAnalysisConfigParsingBoundaries(t *testing.T) {
	defaultURLs, err := parseNodeURLs("  ")
	if err != nil || len(defaultURLs) != 3 || defaultURLs[1] != "http://wk-node1:5001" {
		t.Fatalf("default node URLs = %#v, %v", defaultURLs, err)
	}
	validURLs, err := parseNodeURLs(`{"1":"http://one","2":"http://two","3":"http://three"}`)
	if err != nil || validURLs[3] != "http://three" {
		t.Fatalf("parsed node URLs = %#v, %v", validURLs, err)
	}
	for name, raw := range map[string]string{
		"invalid json": `{`,
		"wrong count":  `{"1":"http://one"}`,
		"invalid id":   `{"1":"http://one","2":"http://two","4":"http://four"}`,
		"empty URL":    `{"1":"http://one","2":"http://two","3":" "}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseNodeURLs(raw); !errors.Is(err, errInvalidAnalysisConfig) {
				t.Fatalf("parseNodeURLs() error = %v", err)
			}
		})
	}

	fallbackTime := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	if got, err := optionalTime("", fallbackTime); err != nil || !got.Equal(fallbackTime) {
		t.Fatalf("optionalTime fallback = %v, %v", got, err)
	}
	if got, err := optionalTime(" 2026-09-02T11:00:00Z ", fallbackTime); err != nil || got.Hour() != 11 {
		t.Fatalf("optionalTime parsed = %v, %v", got, err)
	}
	if _, err := optionalTime("invalid", fallbackTime); err == nil {
		t.Fatal("invalid optional time unexpectedly accepted")
	}

	for _, test := range []struct {
		raw      string
		fallback int
		want     int
		wantErr  bool
	}{
		{raw: "", fallback: 12, want: 12},
		{raw: "0", fallback: 12, want: 0},
		{raw: "7", fallback: 12, want: 7},
		{raw: "-1", wantErr: true},
		{raw: "nope", wantErr: true},
	} {
		got, err := optionalPositiveInt(test.raw, test.fallback)
		if test.wantErr {
			if !errors.Is(err, errInvalidAnalysisConfig) {
				t.Fatalf("optionalPositiveInt(%q) error = %v", test.raw, err)
			}
			continue
		}
		if err != nil || got != test.want {
			t.Fatalf("optionalPositiveInt(%q) = %d, %v; want %d", test.raw, got, err, test.want)
		}
	}

	env := map[string]string{"SET": "  configured  "}
	getenv := func(key string) string { return env[key] }
	if got := envDefault(getenv, "SET", "fallback"); got != "configured" {
		t.Fatalf("configured env = %q", got)
	}
	if got := envDefault(getenv, "MISSING", "fallback"); got != "fallback" {
		t.Fatalf("missing env = %q", got)
	}
	if _, err := loadServeConfig(nil, nil); !errors.Is(err, errInvalidAnalysisConfig) {
		t.Fatalf("nil getenv error = %v", err)
	}
}

func TestLoadServeConfigRejectsInvalidBoundaryConfiguration(t *testing.T) {
	now := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	scenarioPath := testScenarioPath(t)
	base := map[string]string{
		"WK_ANALYSIS_RUN_ID":        "run-1",
		"WK_ANALYSIS_MCP_TOKEN":     "run-token-0123456789-0123456789-ab",
		"WK_ANALYSIS_SCENARIO_PATH": scenarioPath,
	}
	invalidScenarioPath := filepath.Join(t.TempDir(), "invalid.yaml")
	if err := os.WriteFile(invalidScenarioPath, []byte("version: other\nrun:\n  random_seed: 0\n"), 0o600); err != nil {
		t.Fatalf("write invalid scenario: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(map[string]string)
	}{
		{name: "missing scenario", mutate: func(env map[string]string) { delete(env, "WK_ANALYSIS_SCENARIO_PATH") }},
		{name: "unreadable scenario", mutate: func(env map[string]string) {
			env["WK_ANALYSIS_SCENARIO_PATH"] = filepath.Join(t.TempDir(), "missing.yaml")
		}},
		{name: "scenario digest mismatch", mutate: func(env map[string]string) { env["WK_ANALYSIS_SCENARIO_DIGEST"] = "sha256:wrong" }},
		{name: "invalid effective scenario", mutate: func(env map[string]string) { env["WK_ANALYSIS_SCENARIO_PATH"] = invalidScenarioPath }},
		{name: "unreadable locator", mutate: func(env map[string]string) {
			env["WK_ANALYSIS_FAKE_INVENTORY_PATH"] = "inventory.json"
			env["WK_ANALYSIS_RUN_LOCATOR_PATH"] = filepath.Join(t.TempDir(), "missing-locator.json")
		}},
		{name: "invalid listen address", mutate: func(env map[string]string) { env["WK_ANALYSIS_LISTEN_ADDR"] = "missing-port" }},
		{name: "short run lease", mutate: func(env map[string]string) {
			env["WK_ANALYSIS_RUN_EXPIRES_AT"] = now.Add(29 * time.Minute).Format(time.RFC3339)
		}},
		{name: "invalid node URLs", mutate: func(env map[string]string) { env["WK_ANALYSIS_NODE_API_URLS"] = "[]" }},
		{name: "negative inventory count", mutate: func(env map[string]string) { env["WK_ANALYSIS_INVENTORY_COUNT"] = "-1" }},
		{name: "partial manager credentials", mutate: func(env map[string]string) { env["WK_ANALYSIS_MANAGER_USERNAME"] = "operator" }},
		{name: "partial TLS credentials", mutate: func(env map[string]string) { env["WK_ANALYSIS_TLS_CERT_FILE"] = "server.crt" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			env := make(map[string]string, len(base)+3)
			for key, value := range base {
				env[key] = value
			}
			test.mutate(env)
			_, err := loadServeConfig(func(key string) string { return env[key] }, func() time.Time { return now })
			if !errors.Is(err, errInvalidAnalysisConfig) {
				t.Fatalf("loadServeConfig() error = %v", err)
			}
		})
	}
}

func TestAnalysisSessionStoreRejectsUnsafeIssueAndVerifyInputs(t *testing.T) {
	now := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	store := newAnalysisSessionStore(now.Add(29*time.Minute), func() time.Time { return now })
	if _, _, err := store.Issue(); !errors.Is(err, errInvalidAnalysisSession) {
		t.Fatalf("short lease issue error = %v", err)
	}

	store.runExpiresAt = now.Add(2 * time.Hour)
	store.random = strings.NewReader("short")
	if _, _, err := store.Issue(); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("random source error = %v", err)
	}
	if _, err := store.Verify(context.Background(), "short", nil); !errors.Is(err, errInvalidAnalysisSession) {
		t.Fatalf("short token verify error = %v", err)
	}
	if _, err := store.Verify(context.Background(), strings.Repeat("x", 32), nil); !errors.Is(err, errInvalidAnalysisSession) {
		t.Fatalf("unknown token verify error = %v", err)
	}
}

func TestAnalysisSessionStoreDropsExpiredTokensWhenIssuing(t *testing.T) {
	now := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	store := newAnalysisSessionStore(now.Add(2*time.Hour), func() time.Time { return now })
	store.random = strings.NewReader(strings.Repeat("a", 32) + strings.Repeat("b", 32))
	if _, _, err := store.Issue(); err != nil {
		t.Fatalf("first Issue() error = %v", err)
	}
	now = now.Add(46 * time.Minute)
	store.runExpiresAt = now.Add(2 * time.Hour)
	if _, _, err := store.Issue(); err != nil {
		t.Fatalf("second Issue() error = %v", err)
	}
	if len(store.tokens) != 1 {
		t.Fatalf("retained token count = %d, want only the live token", len(store.tokens))
	}

	defaultClockStore := newAnalysisSessionStore(time.Now().Add(time.Hour), nil)
	if defaultClockStore.now == nil {
		t.Fatal("nil clock did not receive the production default")
	}
}

func TestOIDCConfigAndVerifierDefaults(t *testing.T) {
	for _, test := range []struct {
		raw      string
		fallback bool
		want     bool
		wantErr  bool
	}{
		{raw: "", fallback: true, want: true},
		{raw: " TRUE ", want: true},
		{raw: "1", want: true},
		{raw: "false"},
		{raw: "0"},
		{raw: "yes", wantErr: true},
	} {
		got, err := strconvParseBoolDefault(test.raw, test.fallback)
		if test.wantErr != (err != nil) || got != test.want {
			t.Fatalf("strconvParseBoolDefault(%q) = %v, %v", test.raw, got, err)
		}
	}

	config, err := loadGitHubOIDCConfig(func(string) string { return "" }, "run-1")
	if err != nil || config != nil {
		t.Fatalf("disabled OIDC config = %#v, %v", config, err)
	}
	verifier := newGitHubOIDCVerifier(githubOIDCConfig{}, nil, nil)
	if verifier.client == nil || verifier.client.Timeout != 10*time.Second || verifier.now == nil {
		t.Fatalf("OIDC verifier defaults = %#v", verifier)
	}
	if err := verifier.Verify(context.Background(), "not-a-jwt"); !errors.Is(err, errInvalidGitHubOIDC) {
		t.Fatalf("malformed token error = %v", err)
	}
	if err := verifier.Verify(context.Background(), "eyJhbGciOiJSUzI1NiJ9.e30.AA"); !errors.Is(err, errInvalidGitHubOIDC) {
		t.Fatalf("missing key ID error = %v", err)
	}
	verifier.jwksURL = "://invalid"
	if _, err := verifier.key(context.Background(), "missing"); !errors.Is(err, errInvalidGitHubOIDC) {
		t.Fatalf("invalid JWKS URL error = %v", err)
	}
}

func TestGitHubOIDCKeyLoadingFailsClosedAndCachesValidKeys(t *testing.T) {
	now := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	cached := &rsa.PublicKey{N: big.NewInt(17), E: 3}
	verifier := newGitHubOIDCVerifier(githubOIDCConfig{}, &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("fresh cached key unexpectedly fetched JWKS")
			return nil, errors.New("unexpected fetch")
		}),
	}, func() time.Time { return now })
	verifier.keys["cached"] = cached
	verifier.loaded = now
	if got, err := verifier.key(context.Background(), "cached"); err != nil || got != cached {
		t.Fatalf("cached key = %#v, %v", got, err)
	}

	for _, test := range []struct {
		name      string
		transport roundTripFunc
	}{
		{name: "transport error", transport: func(*http.Request) (*http.Response, error) {
			return nil, errors.New("offline")
		}},
		{name: "non-OK status", transport: func(*http.Request) (*http.Response, error) {
			return response(http.StatusServiceUnavailable, "unavailable"), nil
		}},
		{name: "malformed document", transport: func(*http.Request) (*http.Response, error) {
			return response(http.StatusOK, "not-json"), nil
		}},
		{name: "no usable requested key", transport: func(*http.Request) (*http.Response, error) {
			return response(http.StatusOK, `{"keys":[
				{"kid":"","kty":"RSA","alg":"RS256","n":"AQ","e":"Aw"},
				{"kid":"wrong-type","kty":"EC","alg":"RS256","n":"AQ","e":"Aw"},
				{"kid":"bad-modulus","kty":"RSA","alg":"RS256","n":"!","e":"Aw"},
				{"kid":"empty-exponent","kty":"RSA","alg":"RS256","n":"AQ","e":""},
				{"kid":"small-exponent","kty":"RSA","alg":"RS256","n":"AQ","e":"Ag"},
				{"kid":"other","kty":"RSA","alg":"RS256","n":"EQ","e":"Aw"}
			]}`), nil
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			verifier := newGitHubOIDCVerifier(githubOIDCConfig{}, &http.Client{Transport: test.transport}, func() time.Time { return now })
			verifier.jwksURL = "https://oidc.test/keys"
			if _, err := verifier.key(context.Background(), "wanted"); !errors.Is(err, errInvalidGitHubOIDC) {
				t.Fatalf("key() error = %v", err)
			}
		})
	}

	if _, err := loadGitHubOIDCConfig(func(key string) string {
		if key == "WK_ANALYSIS_GITHUB_OIDC_ENABLED" {
			return "invalid"
		}
		return ""
	}, "run-1"); !errors.Is(err, errInvalidAnalysisConfig) {
		t.Fatalf("invalid enabled flag error = %v", err)
	}
}
