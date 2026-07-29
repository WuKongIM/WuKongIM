package issueagentgithub

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const githubAPIVersion = "2022-11-28"

var issueAgentAppPermissions = map[string]string{
	"actions":       "write",
	"contents":      "write",
	"issues":        "write",
	"metadata":      "read",
	"pull_requests": "write",
}

// AppTokenConfig binds token minting to one App installation and repository.
type AppTokenConfig struct {
	BaseURL        string
	AppID          int64
	InstallationID int64
	RepositoryID   int64
	Repository     string
	PrivateKeyPEM  []byte
}

// InstallationToken is retained in memory only by the trusted Publisher.
type InstallationToken struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// AppTokenMinter creates one short-lived repository-scoped installation token.
type AppTokenMinter struct {
	config     AppTokenConfig
	baseURL    *url.URL
	privateKey *rsa.PrivateKey
	client     *http.Client
	now        func() time.Time
}

// NewAppTokenMinter validates immutable App and HTTP configuration.
func NewAppTokenMinter(
	config AppTokenConfig,
	client *http.Client,
	now func() time.Time,
) (*AppTokenMinter, error) {
	if config.AppID <= 0 ||
		config.InstallationID <= 0 ||
		config.RepositoryID <= 0 ||
		config.Repository == "" ||
		client == nil ||
		now == nil {
		return nil, errors.New("GitHub App token configuration is incomplete")
	}
	baseURL, err := url.Parse(config.BaseURL)
	if err != nil ||
		baseURL.Host == "" ||
		baseURL.RawQuery != "" ||
		baseURL.Fragment != "" ||
		baseURL.User != nil ||
		(baseURL.Scheme != "https" && !isLoopbackHTTP(baseURL)) {
		return nil, errors.New("GitHub App API base URL is invalid")
	}
	privateKey, err := parseRSAPrivateKey(config.PrivateKeyPEM)
	if err != nil {
		return nil, errors.New("GitHub App private key is invalid")
	}
	httpClient := *client
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("GitHub App token request redirect rejected")
	}
	if httpClient.Timeout == 0 {
		httpClient.Timeout = 15 * time.Second
	}
	return &AppTokenMinter{
		config:     config,
		baseURL:    baseURL,
		privateKey: privateKey,
		client:     &httpClient,
		now:        now,
	}, nil
}

// Mint requests the exact reviewed permission and repository scope.
func (minter *AppTokenMinter) Mint(ctx context.Context) (InstallationToken, error) {
	if minter == nil {
		return InstallationToken{}, errors.New("GitHub App token minter is nil")
	}
	now := minter.now().UTC()
	claims := jwt.RegisteredClaims{
		Issuer:    strconv.FormatInt(minter.config.AppID, 10),
		IssuedAt:  jwt.NewNumericDate(now.Add(-time.Minute)),
		ExpiresAt: jwt.NewNumericDate(now.Add(9 * time.Minute)),
	}
	appJWT, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(
		minter.privateKey,
	)
	if err != nil {
		return InstallationToken{}, errors.New("sign GitHub App JWT")
	}
	requestBody := struct {
		RepositoryIDs []int64           `json:"repository_ids"`
		Permissions   map[string]string `json:"permissions"`
	}{
		RepositoryIDs: []int64{minter.config.RepositoryID},
		Permissions:   clonePermissions(issueAgentAppPermissions),
	}
	encoded, err := json.Marshal(requestBody)
	if err != nil {
		return InstallationToken{}, errors.New("encode GitHub App token request")
	}
	endpoint := *minter.baseURL
	endpoint.Path = strings.TrimSuffix(endpoint.Path, "/") +
		"/app/installations/" +
		strconv.FormatInt(minter.config.InstallationID, 10) +
		"/access_tokens"
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		endpoint.String(),
		bytes.NewReader(encoded),
	)
	if err != nil {
		return InstallationToken{}, errors.New("create GitHub App token request")
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	request.Header.Set("Authorization", "Bearer "+appJWT)

	response, err := minter.client.Do(request)
	if err != nil {
		return InstallationToken{}, fmt.Errorf("request GitHub App token: %w", redactHTTPError(err))
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		return InstallationToken{}, fmt.Errorf(
			"GitHub App token request failed with status %d",
			response.StatusCode,
		)
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return InstallationToken{}, errors.New("GitHub App token response has unexpected content type")
	}
	var payload struct {
		Token               string            `json:"token"`
		ExpiresAt           time.Time         `json:"expires_at"`
		Permissions         map[string]string `json:"permissions"`
		RepositorySelection string            `json:"repository_selection"`
		Repositories        []json.RawMessage `json:"repositories"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return InstallationToken{}, errors.New("decode GitHub App token response")
	}
	var repository struct {
		ID       int64  `json:"id"`
		FullName string `json:"full_name"`
	}
	if len(payload.Repositories) != 1 ||
		json.Unmarshal(payload.Repositories[0], &repository) != nil {
		return InstallationToken{}, errors.New("GitHub App token response scope is invalid")
	}
	if payload.Token == "" ||
		len(payload.Token) > 4096 ||
		!payload.ExpiresAt.After(now) ||
		payload.ExpiresAt.After(now.Add(65*time.Minute)) ||
		!samePermissions(payload.Permissions, issueAgentAppPermissions) ||
		payload.RepositorySelection != "selected" ||
		repository.ID != minter.config.RepositoryID ||
		repository.FullName != minter.config.Repository {
		return InstallationToken{}, errors.New("GitHub App token response scope is invalid")
	}
	return InstallationToken{
		Token:     payload.Token,
		ExpiresAt: payload.ExpiresAt,
	}, nil
}

func parseRSAPrivateKey(encoded []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(encoded)
	if block == nil {
		return nil, errors.New("PEM block is missing")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("private key is not RSA")
	}
	return key, nil
}

func isLoopbackHTTP(endpoint *url.URL) bool {
	if endpoint.Scheme != "http" {
		return false
	}
	host := endpoint.Hostname()
	return host == "localhost" || net.ParseIP(host).IsLoopback()
}

func clonePermissions(source map[string]string) map[string]string {
	cloned := make(map[string]string, len(source))
	for name, level := range source {
		cloned[name] = level
	}
	return cloned
}

func samePermissions(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for name, level := range right {
		if left[name] != level {
			return false
		}
	}
	return true
}

func redactHTTPError(err error) error {
	if err == nil {
		return nil
	}
	return errors.New("GitHub API request failed")
}
