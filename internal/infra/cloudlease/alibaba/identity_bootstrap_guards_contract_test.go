package alibaba

import (
	"errors"
	"testing"
)

func TestIdentityBootstrapGuardsRejectMissingAuthorityAndResidualIdentityKinds(t *testing.T) {
	if _, err := NewIdentityBootstrapper(testIdentityBootstrapConfig(), nil); !errors.Is(err, ErrIdentityBootstrapConfig) {
		t.Fatalf("NewIdentityBootstrapper(nil API) error = %v", err)
	}
	if identityBootstrapStateEmpty(IdentityBootstrapState{
		OIDCProvider: IdentityOIDCProviderSpec{Name: "residual-provider"},
	}) {
		t.Fatal("residual OIDC provider was treated as removed")
	}
	if identityBootstrapStateEmpty(IdentityBootstrapState{
		Policies: []IdentityPolicySpec{{Name: "residual-policy"}},
	}) {
		t.Fatal("residual role policy was treated as removed")
	}
}
