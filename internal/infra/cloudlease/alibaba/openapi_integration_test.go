//go:build integration

package alibaba

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudlease"
)

func TestOpenAPIOIDCQuoteIsReadOnly(t *testing.T) {
	if os.Getenv("WK_ALIBABA_READONLY_INTEGRATION") != "1" {
		t.Skip("set WK_ALIBABA_READONLY_INTEGRATION=1 with temporary read-only OIDC credentials")
	}
	if os.Getenv("ALIBABA_CLOUD_SECURITY_TOKEN") == "" {
		t.Fatal("ALIBABA_CLOUD_SECURITY_TOKEN is required to prove temporary OIDC credentials")
	}
	expectedRole := strings.TrimSpace(os.Getenv("WK_ALIBABA_EXPECTED_QUOTE_ROLE"))
	if expectedRole == "" {
		t.Fatal("WK_ALIBABA_EXPECTED_QUOTE_ROLE is required to prove the exact OIDC role binding")
	}
	expectedPolicy := strings.TrimSpace(os.Getenv("WK_ALIBABA_EXPECTED_QUOTE_POLICY"))
	if expectedPolicy == "" {
		t.Fatal("WK_ALIBABA_EXPECTED_QUOTE_POLICY is required to prove the live role policy allowlist")
	}
	api, err := NewOpenAPIFromOIDCEnvironment(RegionHangzhou)
	if err != nil {
		t.Fatalf("NewOpenAPIFromOIDCEnvironment() error = %v", err)
	}
	principalARN, err := api.CallerPrincipalARN(context.Background())
	if err != nil {
		t.Fatalf("CallerPrincipalARN() error = %v", err)
	}
	if !principalHasRole(principalARN, expectedRole) {
		t.Fatalf("OIDC principal does not contain expected Quote role %q", expectedRole)
	}
	if err := api.AssertExactQuoteRolePolicy(context.Background(), expectedRole, expectedPolicy); err != nil {
		t.Fatalf("Quote role exact policy proof failed: %v", err)
	}
	if err := api.AssertMutationDenied(context.Background()); err != nil {
		t.Fatalf("Quote role mutation-denial proof failed: %v", err)
	}
	now := time.Now().UTC()
	controller := cloudlease.NewController(New(api, Options{Now: func() time.Time { return now }}), func() time.Time { return now })
	quote, err := controller.Quote(context.Background(), approvedPlan(now))
	if err != nil {
		t.Fatalf("read-only OIDC Quote() error = %v", err)
	}
	if quote.AccountIDHash == "" || quote.Zone == "" || quote.Selection["instance_type"] == "" || quote.Selection["image_id"] == "" {
		t.Fatalf("read-only OIDC Quote() returned incomplete evidence: %#v", quote)
	}
}
