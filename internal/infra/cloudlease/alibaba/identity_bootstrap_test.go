package alibaba

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestIdentityBootstrapPlanApplyAndRemoveAreIdempotent(t *testing.T) {
	api := &identityBootstrapAPIStub{accountID: "1234567890123456"}
	bootstrapper, err := NewIdentityBootstrapper(testIdentityBootstrapConfig(), api)
	if err != nil {
		t.Fatalf("NewIdentityBootstrapper() error = %v", err)
	}

	plan, err := bootstrapper.Plan(context.Background())
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if len(plan.Changes) != 7 {
		t.Fatalf("initial changes = %#v, want seven identity resources", plan.Changes)
	}
	result, err := bootstrapper.Apply(context.Background())
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if result.AccountIDHash == "" || result.OIDCProviderARN == "" || result.ProvisionerRoleARN == "" ||
		result.ObserverRoleARN == "" || result.ReleaserRoleARN == "" {
		t.Fatalf("Apply() result = %#v, want complete non-secret binding", result)
	}
	plan, err = bootstrapper.Plan(context.Background())
	if err != nil {
		t.Fatalf("second Plan() error = %v", err)
	}
	if len(plan.Changes) != 0 {
		t.Fatalf("second changes = %#v, want idempotent empty plan", plan.Changes)
	}
	if _, err := bootstrapper.Remove(context.Background()); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if !reflect.DeepEqual(api.state, IdentityBootstrapState{}) {
		t.Fatalf("state after Remove = %#v, want empty", api.state)
	}
}

func TestIdentityBootstrapRemovalFailsClosedWithTaggedLeaseInventory(t *testing.T) {
	api := &identityBootstrapAPIStub{
		accountID: "1234567890123456",
		assets:    []LifecycleAsset{{ID: "i-related", Kind: ResourceKindInstance}},
	}
	bootstrapper, err := NewIdentityBootstrapper(testIdentityBootstrapConfig(), api)
	if err != nil {
		t.Fatal(err)
	}
	_, err = bootstrapper.Remove(context.Background())
	if !errors.Is(err, ErrIdentityBootstrapActiveLeases) {
		t.Fatalf("Remove() error = %v, want active-Lease guard", err)
	}
	if api.removeCalls != 0 {
		t.Fatalf("remove calls = %d, want zero", api.removeCalls)
	}
}

func TestIdentityBootstrapEmptyStateAcceptsProviderReadShape(t *testing.T) {
	if !identityBootstrapStateEmpty(IdentityBootstrapState{
		Roles: make([]IdentityRoleSpec, 3), Policies: make([]IdentityPolicySpec, 3),
	}) {
		t.Fatal("zero-valued provider read shape is not empty")
	}
	if identityBootstrapStateEmpty(IdentityBootstrapState{Roles: []IdentityRoleSpec{{Name: CloudLeaseObserverRole}}}) {
		t.Fatal("nonempty role was treated as removed")
	}
}

func TestIdentityBootstrapRejectsUnexpectedAuthenticatedAccount(t *testing.T) {
	config := testIdentityBootstrapConfig()
	config.ExpectedAccountIDHash = "sha256:" + strings.Repeat("a", 64)
	api := &identityBootstrapAPIStub{accountID: "1234567890123456"}
	bootstrapper, err := NewIdentityBootstrapper(config, api)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bootstrapper.Plan(context.Background()); !errors.Is(err, ErrIdentityBootstrapConfig) {
		t.Fatalf("Plan() error = %v, want account binding rejection", err)
	}
}

func TestDesiredIdentityBootstrapStateSeparatesExactWorkflowRoles(t *testing.T) {
	config := testIdentityBootstrapConfig()
	state, err := DesiredIdentityBootstrapState(config, "1234567890123456")
	if err != nil {
		t.Fatalf("DesiredIdentityBootstrapState() error = %v", err)
	}
	if len(state.Roles) != 3 || len(state.Policies) != 3 {
		t.Fatalf("state roles/policies = %d/%d, want 3/3", len(state.Roles), len(state.Policies))
	}
	for index, expected := range []struct {
		role        string
		environment string
		workflow    string
	}{
		{CloudLeaseProvisionerRole, CloudLeaseProvisionEnvironment, CloudLeaseProvisionWorkflow},
		{CloudLeaseObserverRole, CloudLeaseObserveEnvironment, CloudLeaseObserveWorkflow},
		{CloudLeaseReleaserRole, CloudLeaseReleaseEnvironment, CloudLeaseReleaseWorkflow},
	} {
		role := state.Roles[index]
		if role.Name != expected.role || role.MaxSessionDuration != 3600 {
			t.Fatalf("role[%d] = %#v", index, role)
		}
		ordinarySubject := identityGitHubSubject(config, expected.environment, expected.workflow)
		setupSubject := identityGitHubSubject(config, expected.environment, CloudLeaseOIDCSetupWorkflow)
		analysisSubject := identityGitHubSubject(config, expected.environment, CloudLeaseAnalysisWorkflow)
		for _, fragment := range []string{ordinarySubject, setupSubject, analysisSubject, `"oidc:aud":["wukongim-cloud-lease"]`} {
			if !strings.Contains(role.TrustPolicy, fragment) {
				t.Fatalf("role %s trust missing %q: %s", role.Name, fragment, role.TrustPolicy)
			}
		}
		if strings.Contains(role.TrustPolicy, CloudDeploymentEnvironment) {
			t.Fatalf("role %s unexpectedly trusts deployment", role.Name)
		}
	}
	if got, want := CloudLeaseGitHubEnvironments(), []string{
		CloudLeaseProvisionEnvironment, CloudLeaseObserveEnvironment, CloudLeaseReleaseEnvironment, CloudDeploymentEnvironment,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("CloudLeaseGitHubEnvironments() = %#v, want %#v", got, want)
	}
}

func TestIdentityRolePoliciesAreExactAndSeparated(t *testing.T) {
	decode := func(kind IdentityPolicyKind) []string {
		t.Helper()
		document, err := IdentityRolePolicyDocument(kind)
		if err != nil {
			t.Fatalf("IdentityRolePolicyDocument(%s) error = %v", kind, err)
		}
		var parsed struct {
			Statement []struct {
				Action []string `json:"Action"`
			} `json:"Statement"`
		}
		if err := json.Unmarshal([]byte(document), &parsed); err != nil || len(parsed.Statement) != 1 {
			t.Fatalf("decode policy %s: %v, %#v", kind, err, parsed)
		}
		return parsed.Statement[0].Action
	}
	provisioner := decode(IdentityPolicyProvisioner)
	observer := decode(IdentityPolicyObserver)
	releaser := decode(IdentityPolicyReleaser)
	for kind, actions := range map[IdentityPolicyKind][]string{
		IdentityPolicyProvisioner: provisioner, IdentityPolicyObserver: observer, IdentityPolicyReleaser: releaser,
	} {
		if !slices.IsSorted(actions) {
			t.Fatalf("%s actions are not canonical: %#v", kind, actions)
		}
		for index, action := range actions {
			if action == "" || strings.Contains(action, "*") || (index > 0 && action == actions[index-1]) {
				t.Fatalf("%s invalid action %q", kind, action)
			}
		}
		for _, action := range []string{"ram:GetPolicyVersion", "ram:GetRole", "ram:ListPoliciesForRole"} {
			if !slices.Contains(actions, action) {
				t.Fatalf("%s live verification action missing %q", kind, action)
			}
		}
	}
	for _, action := range RequiredBillingObserveActions() {
		if !slices.Contains(observer, action) || slices.Contains(provisioner, action) || slices.Contains(releaser, action) {
			t.Fatalf("billing action separation failed for %q", action)
		}
	}
	for _, action := range []string{"ecs:RunInstances", "vpc:AllocateEipAddress", "ecs:AuthorizeSecurityGroup"} {
		if !slices.Contains(provisioner, action) || slices.Contains(observer, action) || slices.Contains(releaser, action) {
			t.Fatalf("provision action separation failed for %q", action)
		}
	}
	if !slices.Contains(observer, "ecs:DescribePrice") || slices.Contains(releaser, "ecs:DescribePrice") {
		t.Fatal("observer price permission is not separated from release")
	}
	for _, action := range []string{"ecs:DeleteInstance", "vpc:ReleaseEipAddress", "vpc:DeleteRouteEntry"} {
		if !slices.Contains(releaser, action) || slices.Contains(observer, action) || slices.Contains(provisioner, action) {
			t.Fatalf("release action separation failed for %q", action)
		}
	}
}

func TestExpectedIdentityRoleTrustIncludesSetupAndOrdinaryWorkflow(t *testing.T) {
	trust, err := ExpectedIdentityRoleTrust(
		"WuKongIM/WuKongIM", "main",
		"acs:ram::1234567890123456:oidc-provider/wukongim-cloud-lease-github",
		"wukongim-cloud-lease", CloudLeaseObserverRole,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"cloud-lease-observe:job_workflow_ref:WuKongIM/WuKongIM/.github/workflows/cloud-lease-oidc-setup.yml@refs/heads/main",
		"cloud-lease-observe:job_workflow_ref:WuKongIM/WuKongIM/.github/workflows/cloud-lease-observe.yml@refs/heads/main",
		"cloud-lease-observe:job_workflow_ref:WuKongIM/WuKongIM/.github/workflows/cloud-lease-analyze.yml@refs/heads/main",
	} {
		if !strings.Contains(trust, fragment) {
			t.Fatalf("trust missing %q: %s", fragment, trust)
		}
	}
}

func TestIdentityBootstrapAccessKeyConstructorRequiresCompletePair(t *testing.T) {
	t.Setenv(credentialAccessKeyIDEnv, "")
	t.Setenv(credentialAccessKeySecretEnv, "")
	if _, err := NewIdentityBootstrapOpenAPIFromAccessKeyEnvironment(RegionHangzhou); !errors.Is(err, ErrIdentityBootstrapCredentials) ||
		!strings.Contains(err.Error(), "configure both") {
		t.Fatalf("missing pair error = %v", err)
	}
	t.Setenv(credentialAccessKeyIDEnv, "only-id")
	if _, err := NewIdentityBootstrapOpenAPIFromAccessKeyEnvironment(RegionHangzhou); !errors.Is(err, ErrIdentityBootstrapCredentials) ||
		!strings.Contains(err.Error(), "configured together") {
		t.Fatalf("partial pair error = %v", err)
	}
	t.Setenv(credentialAccessKeyIDEnv, "")
	t.Setenv(credentialAccessKeySecretEnv, "only-secret")
	if _, err := NewIdentityBootstrapOpenAPIFromAccessKeyEnvironment(RegionHangzhou); !errors.Is(err, ErrIdentityBootstrapCredentials) {
		t.Fatalf("reverse partial pair error = %v", err)
	}
}

func testIdentityBootstrapConfig() IdentityBootstrapConfig {
	return IdentityBootstrapConfig{
		Region: RegionHangzhou, Repository: "WuKongIM/WuKongIM", DefaultBranch: "main",
		OIDCProviderName: "wukongim-cloud-lease-github", OIDCAudience: "wukongim-cloud-lease",
		OIDCFingerprints: []string{"6938fd4d98bab03faadb97b34396831e3780aea1"},
	}
}

type identityBootstrapAPIStub struct {
	accountID   string
	state       IdentityBootstrapState
	assets      []LifecycleAsset
	removeCalls int
}

func (a *identityBootstrapAPIStub) CallerAccountID(context.Context) (string, error) {
	return a.accountID, nil
}

func (a *identityBootstrapAPIStub) ReadIdentityBootstrapState(context.Context, IdentityBootstrapState) (IdentityBootstrapState, error) {
	return a.state, nil
}

func (a *identityBootstrapAPIStub) ApplyIdentityBootstrapState(_ context.Context, desired IdentityBootstrapState) error {
	a.state = desired
	return nil
}

func (a *identityBootstrapAPIStub) RemoveIdentityBootstrapState(context.Context, IdentityBootstrapState) error {
	a.removeCalls++
	a.state = IdentityBootstrapState{}
	return nil
}

func (a *identityBootstrapAPIStub) ListAssets(context.Context, InventoryQuery) ([]LifecycleAsset, error) {
	return append([]LifecycleAsset(nil), a.assets...), nil
}
