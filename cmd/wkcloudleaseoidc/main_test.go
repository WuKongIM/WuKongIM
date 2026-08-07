package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/infra/cloudlease/alibaba"
)

func TestIdentityCommandsApplyAndVerifyEmitVersionedNonSecretJSON(t *testing.T) {
	api := &commandIdentityAPI{accountID: "1234567890123456"}
	dependencies := identityDependencies{
		fingerprints: func(context.Context) ([]string, error) {
			return []string{"6938fd4d98bab03faadb97b34396831e3780aea1"}, nil
		},
		bootstrapAPI: func(region string) (alibaba.IdentityBootstrapAPI, error) {
			if region != alibaba.RegionHangzhou {
				t.Fatalf("bootstrap region = %q", region)
			}
			return api, nil
		},
		verifyRole: func(_ context.Context, region, repository, providerARN, audience, role string, kind alibaba.IdentityPolicyKind) (string, error) {
			if region != alibaba.RegionHangzhou || repository != "WuKongIM/WuKongIM" ||
				providerARN != "acs:ram::1234567890123456:oidc-provider/wukongim-cloud-lease-github" ||
				audience != "wukongim-cloud-lease" || role != alibaba.CloudLeaseObserverRole || kind != alibaba.IdentityPolicyObserver {
				t.Fatalf("verify inputs = %q %q %q %q %q %q", region, repository, providerARN, audience, role, kind)
			}
			return "sha256:" + strings.Repeat("a", 64), nil
		},
	}
	configPath := writeIdentityConfig(t, false)
	var stdout bytes.Buffer
	root := newRootCommandWithDependencies(&stdout, dependencies)
	root.SetArgs([]string{"apply", "--config", configPath})
	if err := root.Execute(); err != nil {
		t.Fatalf("apply error = %v", err)
	}
	var applied identityResultDocument
	if err := json.Unmarshal(stdout.Bytes(), &applied); err != nil {
		t.Fatalf("decode apply output: %v", err)
	}
	if applied.Schema != identityResultSchemaV1 || applied.Result.ProvisionerRoleARN == "" || applied.Result.ObserverRoleARN == "" ||
		applied.Result.ReleaserRoleARN == "" || strings.Contains(stdout.String(), "test-secret") {
		t.Fatalf("apply output = %s", stdout.String())
	}

	stdout.Reset()
	root = newRootCommandWithDependencies(&stdout, dependencies)
	root.SetArgs([]string{
		"verify", "--region", alibaba.RegionHangzhou,
		"--repository", "WuKongIM/WuKongIM",
		"--oidc-provider-arn", "acs:ram::1234567890123456:oidc-provider/wukongim-cloud-lease-github",
		"--audience", "wukongim-cloud-lease",
		"--expected-role", alibaba.CloudLeaseObserverRole, "--policy-kind", "observer",
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("verify error = %v", err)
	}
	var verified identityVerifyDocument
	if err := json.Unmarshal(stdout.Bytes(), &verified); err != nil {
		t.Fatalf("decode verify output: %v", err)
	}
	if verified.Schema != identityVerifySchemaV1 || verified.Role != alibaba.CloudLeaseObserverRole || verified.AccountIDHash == "" {
		t.Fatalf("verify output = %#v", verified)
	}
}

func TestReadIdentityConfigRejectsUnknownFieldsAndTrailingDocuments(t *testing.T) {
	valid, err := os.ReadFile(writeIdentityConfig(t, true))
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"unknown":  bytes.Replace(valid, []byte(`"bootstrap":`), []byte(`"unexpected":true,"bootstrap":`), 1),
		"trailing": append(append([]byte(nil), valid...), []byte(`{"second":true}`)...),
		"schema":   bytes.Replace(valid, []byte(identityConfigSchemaV1), []byte("unknown/v1"), 1),
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := readIdentityConfig(path); err == nil {
				t.Fatal("invalid identity config was accepted")
			}
		})
	}
}

func writeIdentityConfig(t *testing.T, fingerprints bool) string {
	t.Helper()
	config := alibaba.IdentityBootstrapConfig{
		Region: alibaba.RegionHangzhou, Repository: "WuKongIM/WuKongIM", DefaultBranch: "main",
		OIDCProviderName: "wukongim-cloud-lease-github", OIDCAudience: "wukongim-cloud-lease",
	}
	if fingerprints {
		config.OIDCFingerprints = []string{"6938fd4d98bab03faadb97b34396831e3780aea1"}
	}
	data, err := json.Marshal(identityConfigDocument{Schema: identityConfigSchemaV1, Bootstrap: config})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "identity.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

type commandIdentityAPI struct {
	accountID string
	state     alibaba.IdentityBootstrapState
}

func (a *commandIdentityAPI) CallerAccountID(context.Context) (string, error) {
	return a.accountID, nil
}

func (a *commandIdentityAPI) ReadIdentityBootstrapState(context.Context, alibaba.IdentityBootstrapState) (alibaba.IdentityBootstrapState, error) {
	return a.state, nil
}

func (a *commandIdentityAPI) ApplyIdentityBootstrapState(_ context.Context, desired alibaba.IdentityBootstrapState) error {
	a.state = desired
	return nil
}

func (a *commandIdentityAPI) RemoveIdentityBootstrapState(context.Context, alibaba.IdentityBootstrapState) error {
	a.state = alibaba.IdentityBootstrapState{}
	return nil
}

func (a *commandIdentityAPI) ListAssets(context.Context, alibaba.InventoryQuery) ([]alibaba.LifecycleAsset, error) {
	return nil, nil
}
