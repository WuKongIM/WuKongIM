package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
)

func TestGitHubOIDCVerifierRequiresExactRunWorkflowEnvironmentAndRef(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	exponent := big.NewInt(int64(privateKey.PublicKey.E)).Bytes()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kid": "test", "kty": "RSA", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(privateKey.PublicKey.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(exponent),
		}}})
	}))
	defer server.Close()
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	config := githubOIDCConfig{
		Audience: "wukongim-cloud-sim:run-1", Repository: "WuKongIM/WuKongIM", Ref: "refs/heads/main",
		WorkflowRef:    "WuKongIM/WuKongIM/.github/workflows/cloud-sim-analyze.yml@refs/heads/main",
		Environment:    "cloud-sim-analysis",
		DefaultSubject: "repo:WuKongIM/WuKongIM:environment:cloud-sim-analysis",
		ExpectedSubject: "repo:WuKongIM/WuKongIM:environment:cloud-sim-analysis:job_workflow_ref:" +
			"WuKongIM/WuKongIM/.github/workflows/cloud-sim-analyze.yml@refs/heads/main",
	}
	verifier := newGitHubOIDCVerifier(config, server.Client(), func() time.Time { return now })
	verifier.jwksURL = server.URL
	claims := githubOIDCClaims{
		Repository: config.Repository, Ref: config.Ref, JobWorkflowRef: config.WorkflowRef, Environment: config.Environment,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: githubOIDCIssuer, Subject: config.ExpectedSubject, Audience: jwt.ClaimStrings{config.Audience},
			ExpiresAt: jwt.NewNumericDate(now.Add(5 * time.Minute)), IssuedAt: jwt.NewNumericDate(now),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "test"
	raw, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifier.Verify(context.Background(), raw); err != nil {
		t.Fatalf("Verify(custom subject) error = %v", err)
	}
	claims.Subject = config.DefaultSubject
	token = jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "test"
	raw, _ = token.SignedString(privateKey)
	if err := verifier.Verify(context.Background(), raw); err != nil {
		t.Fatalf("Verify(default subject) error = %v", err)
	}
	claims.JobWorkflowRef = "WuKongIM/WuKongIM/.github/workflows/different.yml@refs/heads/main"
	token = jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "test"
	raw, _ = token.SignedString(privateKey)
	if err := verifier.Verify(context.Background(), raw); err == nil {
		t.Fatal("Verify(default subject with mismatched workflow) error = nil")
	}
	claims.JobWorkflowRef = config.WorkflowRef
	claims.Environment = "different-environment"
	token = jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "test"
	raw, _ = token.SignedString(privateKey)
	if err := verifier.Verify(context.Background(), raw); err == nil {
		t.Fatal("Verify(mismatched environment) error = nil")
	}
}
