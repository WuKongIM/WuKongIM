package issueagentgithub

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAppTokenMinterAcceptsPKCS8RSAAndRejectsOtherKeyMaterial(t *testing.T) {
	t.Parallel()

	rsaKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("rsa.GenerateKey() error = %v", err)
	}
	rsaPKCS8, err := x509.MarshalPKCS8PrivateKey(rsaKey)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey(RSA) error = %v", err)
	}
	config := AppTokenConfig{
		BaseURL:        "https://api.example.test",
		AppID:          1,
		InstallationID: 2,
		RepositoryID:   3,
		Repository:     "WuKongIM/WuKongIM",
		PrivateKeyPEM: pem.EncodeToMemory(&pem.Block{
			Type: "PRIVATE KEY", Bytes: rsaPKCS8,
		}),
	}
	minter, err := NewAppTokenMinter(
		config,
		&http.Client{Transport: boundaryRoundTripper(func(*http.Request) (*http.Response, error) {
			t.Fatal("constructor must not perform I/O")
			return nil, nil
		})},
		time.Now,
	)
	if err != nil {
		t.Fatalf("NewAppTokenMinter(PKCS8 RSA) error = %v", err)
	}
	if minter.privateKey.N.Cmp(rsaKey.N) != 0 {
		t.Fatal("parsed RSA key does not match input")
	}

	ecdsaKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey() error = %v", err)
	}
	ecdsaPKCS8, err := x509.MarshalPKCS8PrivateKey(ecdsaKey)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey(ECDSA) error = %v", err)
	}
	for _, encoded := range [][]byte{
		[]byte("not PEM"),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("invalid DER")}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: ecdsaPKCS8}),
	} {
		config.PrivateKeyPEM = encoded
		if _, err := NewAppTokenMinter(config, &http.Client{}, time.Now); err == nil {
			t.Fatalf("invalid key material was accepted: %q", encoded)
		}
	}
}

func TestAppTokenMinterRejectsUnsafeConfigurationBeforeNetwork(t *testing.T) {
	t.Parallel()

	rsaKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("rsa.GenerateKey() error = %v", err)
	}
	privatePEM := pem.EncodeToMemory(&pem.Block{
		Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(rsaKey),
	})
	valid := AppTokenConfig{
		BaseURL: "https://api.example.test", AppID: 1, InstallationID: 2,
		RepositoryID: 3, Repository: "WuKongIM/WuKongIM",
		PrivateKeyPEM: privatePEM,
	}
	tests := []struct {
		name   string
		mutate func(*AppTokenConfig)
	}{
		{name: "missing app", mutate: func(config *AppTokenConfig) { config.AppID = 0 }},
		{name: "missing installation", mutate: func(config *AppTokenConfig) { config.InstallationID = 0 }},
		{name: "missing repository id", mutate: func(config *AppTokenConfig) { config.RepositoryID = 0 }},
		{name: "missing repository", mutate: func(config *AppTokenConfig) { config.Repository = "" }},
		{name: "external HTTP", mutate: func(config *AppTokenConfig) { config.BaseURL = "http://github.example" }},
		{name: "URL credentials", mutate: func(config *AppTokenConfig) { config.BaseURL = "https://user@api.example.test" }},
		{name: "URL query", mutate: func(config *AppTokenConfig) { config.BaseURL = "https://api.example.test?token=secret" }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			config := valid
			test.mutate(&config)
			if _, err := NewAppTokenMinter(config, &http.Client{}, time.Now); err == nil {
				t.Fatal("unsafe configuration was accepted")
			}
		})
	}
	if _, err := NewAppTokenMinter(valid, nil, time.Now); err == nil {
		t.Fatal("nil HTTP client was accepted")
	}
	if _, err := NewAppTokenMinter(valid, &http.Client{}, nil); err == nil {
		t.Fatal("nil clock was accepted")
	}
}

func TestAppTokenMintFailsClosedWithoutLeakingTokenResponse(t *testing.T) {
	t.Parallel()

	rsaKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("rsa.GenerateKey() error = %v", err)
	}
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	privatePEM := pem.EncodeToMemory(&pem.Block{
		Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(rsaKey),
	})
	config := AppTokenConfig{
		BaseURL: "https://api.example.test", AppID: 1, InstallationID: 2,
		RepositoryID: 3, Repository: "WuKongIM/WuKongIM",
		PrivateKeyPEM: privatePEM,
	}
	tests := []struct {
		name      string
		roundTrip boundaryRoundTripper
		wantError string
	}{
		{
			name: "transport detail is redacted",
			roundTrip: func(*http.Request) (*http.Response, error) {
				return nil, errors.New("dial installation-secret-token@private.example")
			},
			wantError: "request GitHub App token",
		},
		{
			name: "rate limited response body is discarded",
			roundTrip: func(*http.Request) (*http.Response, error) {
				return boundaryJSONResponse(
					http.StatusTooManyRequests,
					`{"message":"installation-secret-token"}`,
				), nil
			},
			wantError: "status 429",
		},
		{
			name: "unexpected content type",
			roundTrip: func(*http.Request) (*http.Response, error) {
				return boundaryResponse(
					http.StatusCreated, "text/plain",
					io.NopCloser(strings.NewReader("installation-secret-token")),
				), nil
			},
			wantError: "unexpected content type",
		},
		{
			name: "unknown response field",
			roundTrip: func(*http.Request) (*http.Response, error) {
				return boundaryJSONResponse(
					http.StatusCreated,
					`{"token":"opaque","expires_at":"2026-08-01T12:30:00Z","permissions":{},"repository_selection":"selected","repositories":[],"unexpected":"installation-secret-token"}`,
				), nil
			},
			wantError: "decode GitHub App token response",
		},
		{
			name: "malformed repository scope",
			roundTrip: func(*http.Request) (*http.Response, error) {
				permissions, marshalErr := json.Marshal(issueAgentAppPermissions)
				if marshalErr != nil {
					t.Fatalf("json.Marshal() error = %v", marshalErr)
				}
				body := `{"token":"opaque","expires_at":"2026-08-01T12:30:00Z","permissions":` +
					string(permissions) +
					`,"repository_selection":"selected","repositories":["invalid"]}`
				return boundaryJSONResponse(http.StatusCreated, body), nil
			},
			wantError: "response scope is invalid",
		},
		{
			name: "permission scope mismatch",
			roundTrip: func(*http.Request) (*http.Response, error) {
				return boundaryJSONResponse(
					http.StatusCreated,
					`{"token":"opaque","expires_at":"2026-08-01T12:30:00Z","permissions":{"contents":"read"},"repository_selection":"selected","repositories":[{"id":3,"full_name":"WuKongIM/WuKongIM"}]}`,
				), nil
			},
			wantError: "response scope is invalid",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			minter, err := NewAppTokenMinter(
				config, &http.Client{Transport: test.roundTrip},
				func() time.Time { return now },
			)
			if err != nil {
				t.Fatalf("NewAppTokenMinter() error = %v", err)
			}
			_, err = minter.Mint(context.Background())
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Mint() error = %v, want substring %q", err, test.wantError)
			}
			if strings.Contains(err.Error(), "installation-secret-token") ||
				strings.Contains(err.Error(), "private.example") ||
				strings.Contains(err.Error(), string(privatePEM)) {
				t.Fatalf("Mint() leaked credential detail: %v", err)
			}
		})
	}
	if _, err := (*AppTokenMinter)(nil).Mint(context.Background()); err == nil {
		t.Fatal("nil minter was accepted")
	}
}
