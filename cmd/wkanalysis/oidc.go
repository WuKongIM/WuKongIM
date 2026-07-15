package main

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
)

const (
	githubOIDCIssuer  = "https://token.actions.githubusercontent.com"
	githubOIDCJWKSURL = githubOIDCIssuer + "/.well-known/jwks"
)

var errInvalidGitHubOIDC = errors.New("cmd/wkanalysis: invalid GitHub OIDC token")

type githubOIDCConfig struct {
	Audience    string
	Repository  string
	Ref         string
	WorkflowRef string
	Environment string
	// DefaultSubject is GitHub's repository and Environment-bound subject.
	DefaultSubject string
	// ExpectedSubject is the stricter subject emitted by the optional repository customization.
	ExpectedSubject string
}

type githubOIDCClaims struct {
	Repository     string `json:"repository"`
	Ref            string `json:"ref"`
	JobWorkflowRef string `json:"job_workflow_ref"`
	Environment    string `json:"environment"`
	jwt.RegisteredClaims
}

type githubOIDCVerifier struct {
	config  githubOIDCConfig
	client  *http.Client
	jwksURL string
	now     func() time.Time
	mu      sync.Mutex
	keys    map[string]*rsa.PublicKey
	loaded  time.Time
}

func loadGitHubOIDCConfig(getenv func(string) string, runID string) (*githubOIDCConfig, error) {
	enabled, err := strconvParseBoolDefault(getenv("WK_ANALYSIS_GITHUB_OIDC_ENABLED"), false)
	if err != nil {
		return nil, fmt.Errorf("%w: WK_ANALYSIS_GITHUB_OIDC_ENABLED", errInvalidAnalysisConfig)
	}
	if !enabled {
		return nil, nil
	}
	config := &githubOIDCConfig{
		Audience:    strings.TrimSpace(getenv("WK_ANALYSIS_GITHUB_OIDC_AUDIENCE")),
		Repository:  strings.TrimSpace(getenv("WK_ANALYSIS_GITHUB_REPOSITORY")),
		Ref:         strings.TrimSpace(getenv("WK_ANALYSIS_GITHUB_REF")),
		WorkflowRef: strings.TrimSpace(getenv("WK_ANALYSIS_GITHUB_WORKFLOW_REF")),
		Environment: strings.TrimSpace(getenv("WK_ANALYSIS_GITHUB_ENVIRONMENT")),
	}
	config.DefaultSubject = fmt.Sprintf("repo:%s:environment:%s", config.Repository, config.Environment)
	config.ExpectedSubject = fmt.Sprintf("repo:%s:environment:%s:job_workflow_ref:%s", config.Repository, config.Environment, config.WorkflowRef)
	expectedWorkflowRef := fmt.Sprintf("%s/.github/workflows/cloud-sim-analyze.yml@%s", config.Repository, config.Ref)
	if runID == "" || config.Audience != "wukongim-cloud-sim:"+runID || config.Repository == "" ||
		!strings.HasPrefix(config.Ref, "refs/heads/") || config.WorkflowRef != expectedWorkflowRef || config.Environment == "" {
		return nil, errInvalidAnalysisConfig
	}
	return config, nil
}

func newGitHubOIDCVerifier(config githubOIDCConfig, client *http.Client, now func() time.Time) *githubOIDCVerifier {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	if now == nil {
		now = time.Now
	}
	return &githubOIDCVerifier{config: config, client: client, jwksURL: githubOIDCJWKSURL, now: now, keys: make(map[string]*rsa.PublicKey)}
}

func (v *githubOIDCVerifier) Verify(ctx context.Context, raw string) error {
	claims := &githubOIDCClaims{}
	parsed, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
		kid, _ := token.Header["kid"].(string)
		if kid == "" {
			return nil, errInvalidGitHubOIDC
		}
		return v.key(ctx, kid)
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer(githubOIDCIssuer),
		jwt.WithAudience(v.config.Audience), jwt.WithExpirationRequired(), jwt.WithTimeFunc(v.now))
	if err != nil || !parsed.Valid {
		return errInvalidGitHubOIDC
	}
	validSubject := claims.Subject == v.config.DefaultSubject || claims.Subject == v.config.ExpectedSubject
	if !validSubject || claims.Repository != v.config.Repository || claims.Ref != v.config.Ref ||
		claims.JobWorkflowRef != v.config.WorkflowRef || claims.Environment != v.config.Environment {
		return errInvalidGitHubOIDC
	}
	return nil
}

func (v *githubOIDCVerifier) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.Lock()
	if key := v.keys[kid]; key != nil && v.now().Sub(v.loaded) < 10*time.Minute {
		v.mu.Unlock()
		return key, nil
	}
	v.mu.Unlock()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return nil, errInvalidGitHubOIDC
	}
	response, err := v.client.Do(request)
	if err != nil {
		return nil, errInvalidGitHubOIDC
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, errInvalidGitHubOIDC
	}
	var document struct {
		Keys []struct {
			KID string `json:"kid"`
			KTY string `json:"kty"`
			ALG string `json:"alg"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 256<<10))
	if err := decoder.Decode(&document); err != nil {
		return nil, errInvalidGitHubOIDC
	}
	keys := make(map[string]*rsa.PublicKey)
	for _, item := range document.Keys {
		if item.KID == "" || item.KTY != "RSA" || item.ALG != "RS256" {
			continue
		}
		modulus, modulusErr := base64.RawURLEncoding.DecodeString(item.N)
		exponentBytes, exponentErr := base64.RawURLEncoding.DecodeString(item.E)
		if modulusErr != nil || exponentErr != nil || len(exponentBytes) == 0 || len(exponentBytes) > 4 {
			continue
		}
		exponent := 0
		for _, value := range exponentBytes {
			exponent = exponent<<8 + int(value)
		}
		if exponent < 3 {
			continue
		}
		keys[item.KID] = &rsa.PublicKey{N: new(big.Int).SetBytes(modulus), E: exponent}
	}
	v.mu.Lock()
	v.keys = keys
	v.loaded = v.now()
	key := v.keys[kid]
	v.mu.Unlock()
	if key == nil {
		return nil, errInvalidGitHubOIDC
	}
	return key, nil
}

func strconvParseBoolDefault(raw string, fallback bool) (bool, error) {
	if strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "1":
		return true, nil
	case "false", "0":
		return false, nil
	default:
		return false, errInvalidAnalysisConfig
	}
}
