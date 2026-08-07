package alibaba

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
)

const (
	// CloudLeaseProvisionEnvironment is the only GitHub Environment allowed to
	// assume the billable provisioning role.
	CloudLeaseProvisionEnvironment = "cloud-lease-provision"
	// CloudLeaseObserveEnvironment is the read-only GitHub Environment.
	CloudLeaseObserveEnvironment = "cloud-lease-observe"
	// CloudLeaseReleaseEnvironment is the unattended cleanup Environment.
	CloudLeaseReleaseEnvironment = "cloud-lease-release"
	// CloudDeploymentEnvironment owns SSH-only deployment credentials.
	CloudDeploymentEnvironment = "cloud-deployment"

	CloudLeaseProvisionerRole = "CloudLeaseProvisioner"
	CloudLeaseObserverRole    = "CloudLeaseObserver"
	CloudLeaseReleaserRole    = "CloudLeaseReleaser"

	CloudLeaseOIDCSetupWorkflow = ".github/workflows/cloud-lease-oidc-setup.yml"
	CloudLeaseProvisionWorkflow = ".github/workflows/cloud-lease-provision.yml"
	CloudLeaseObserveWorkflow   = ".github/workflows/cloud-lease-observe.yml"
	CloudLeaseReleaseWorkflow   = ".github/workflows/cloud-lease-release.yml"
)

var (
	// ErrIdentityBootstrapConfig reports invalid repository/cloud identity input.
	ErrIdentityBootstrapConfig = errors.New("internal/infra/cloudlease/alibaba: invalid identity bootstrap config")
	// ErrIdentityBootstrapActiveLeases prevents removing identities while any
	// related provider resource still exists.
	ErrIdentityBootstrapActiveLeases = errors.New("internal/infra/cloudlease/alibaba: tagged leases prevent identity removal")

	identityAccountIDPattern = regexp.MustCompile(`^[0-9]{6,32}$`)
	identityNamePattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
)

// IdentityBootstrapConfig contains only non-secret Alibaba and GitHub trust
// coordinates. AccessKeys are loaded separately by the bootstrap adapter.
type IdentityBootstrapConfig struct {
	// Region is the single reviewed Alibaba control-plane region.
	Region string `json:"region"`
	// Repository is the exact owner/name allowed in every GitHub subject.
	Repository string `json:"repository"`
	// DefaultBranch is embedded in immutable ordinary-workflow references.
	DefaultBranch string `json:"default_branch"`
	// OIDCProviderName identifies the repository-owned Alibaba provider.
	OIDCProviderName string `json:"oidc_provider_name"`
	// OIDCAudience must match the workflow token exchange audience.
	OIDCAudience string `json:"oidc_audience"`
	// ExpectedAccountIDHash prevents repair or removal in a different account.
	ExpectedAccountIDHash string `json:"expected_account_id_hash,omitempty"`
	// OIDCFingerprints contains the bounded GitHub issuer root CA fingerprints.
	OIDCFingerprints []string `json:"oidc_fingerprints,omitempty"`
}

// IdentityOIDCProviderSpec is the exact repository-owned Alibaba OIDC provider.
type IdentityOIDCProviderSpec struct {
	// Name and ARN identify the exact Alibaba provider.
	Name string `json:"name"`
	ARN  string `json:"arn"`
	// IssuerURL is the system-TLS-verified GitHub token endpoint.
	IssuerURL string `json:"issuer_url"`
	// Audiences and Fingerprints are complete replacement sets.
	Audiences    []string `json:"audiences"`
	Fingerprints []string `json:"fingerprints"`
}

// IdentityRoleSpec is one workflow-conditioned RAM role.
type IdentityRoleSpec struct {
	// Name and ARN identify one closed workflow role.
	Name string `json:"name"`
	ARN  string `json:"arn"`
	// TrustPolicy contains the exact setup and ordinary-workflow subjects.
	TrustPolicy string `json:"trust_policy"`
	// MaxSessionDuration bounds every temporary credential to one hour.
	MaxSessionDuration int64 `json:"max_session_duration_seconds"`
}

// IdentityPolicySpec is one exact custom policy attached to one exact role.
type IdentityPolicySpec struct {
	// Name identifies the sole custom policy on AttachedRole.
	Name string `json:"name"`
	// Document is the canonical non-wildcard action allowlist.
	Document string `json:"document"`
	// AttachedRole is the only role allowed to carry this policy.
	AttachedRole string `json:"attached_role"`
}

// IdentityBootstrapState is the complete Alibaba identity boundary owned by
// the Cloud Lease workflows in one repository.
type IdentityBootstrapState struct {
	// OIDCProvider is the complete repository-owned provider state.
	OIDCProvider IdentityOIDCProviderSpec `json:"oidc_provider"`
	// Roles and Policies are the exact ordered three-role binding.
	Roles    []IdentityRoleSpec   `json:"roles"`
	Policies []IdentityPolicySpec `json:"policies"`
}

// IdentityBootstrapChange describes one idempotent create or update.
type IdentityBootstrapChange struct {
	// Resource is one exact provider, role, or policy identity.
	Resource string `json:"resource"`
	// Action is create or update; removal is a separate guarded operation.
	Action string `json:"action"`
}

// IdentityBootstrapPlan is a non-mutating diff of repository-owned identity.
type IdentityBootstrapPlan struct {
	// Changes is empty only when provider state exactly matches the binding.
	Changes []IdentityBootstrapChange `json:"changes"`
}

// IdentityBootstrapResult contains only non-secret values copied into GitHub
// repository Variables after all three live role checks pass.
type IdentityBootstrapResult struct {
	// Region and AccountIDHash bind later operations without exposing account ID.
	Region        string `json:"region"`
	AccountIDHash string `json:"account_id_hash"`
	// OIDCProviderARN and OIDCAudience configure temporary token exchange.
	OIDCProviderARN string `json:"oidc_provider_arn"`
	OIDCAudience    string `json:"oidc_audience"`
	// ProvisionerRoleARN, ObserverRoleARN, and ReleaserRoleARN are published as
	// non-secret repository Variables only after all three live checks pass.
	ProvisionerRoleARN string `json:"provisioner_role_arn"`
	ObserverRoleARN    string `json:"observer_role_arn"`
	ReleaserRoleARN    string `json:"releaser_role_arn"`
}

// IdentityBootstrapAPI is the one-time Alibaba administrative boundary. It
// exposes identity mutation and tagged inventory, but no infrastructure create.
type IdentityBootstrapAPI interface {
	CallerAccountID(context.Context) (string, error)
	ReadIdentityBootstrapState(context.Context, IdentityBootstrapState) (IdentityBootstrapState, error)
	ApplyIdentityBootstrapState(context.Context, IdentityBootstrapState) error
	RemoveIdentityBootstrapState(context.Context, IdentityBootstrapState) error
	ListAssets(context.Context, InventoryQuery) ([]LifecycleAsset, error)
}

// IdentityBootstrapper reconciles the workflow-conditioned Alibaba identities.
type IdentityBootstrapper struct {
	config IdentityBootstrapConfig
	api    IdentityBootstrapAPI
}

// NewIdentityBootstrapper validates the non-secret identity configuration.
func NewIdentityBootstrapper(config IdentityBootstrapConfig, api IdentityBootstrapAPI) (*IdentityBootstrapper, error) {
	if api == nil || !validIdentityBootstrapConfig(config) {
		return nil, ErrIdentityBootstrapConfig
	}
	return &IdentityBootstrapper{config: cloneIdentityBootstrapConfig(config), api: api}, nil
}

// Plan returns the deterministic repository identity changes without mutation.
func (b *IdentityBootstrapper) Plan(ctx context.Context) (IdentityBootstrapPlan, error) {
	desired, _, err := b.desired(ctx)
	if err != nil {
		return IdentityBootstrapPlan{}, err
	}
	current, err := b.api.ReadIdentityBootstrapState(ctx, desired)
	if err != nil {
		return IdentityBootstrapPlan{}, err
	}
	return identityBootstrapPlan(current, desired), nil
}

// Apply idempotently creates or repairs the provider, three roles, and three
// exact policies, then reads them back before returning identifiers.
func (b *IdentityBootstrapper) Apply(ctx context.Context) (IdentityBootstrapResult, error) {
	desired, accountID, err := b.desired(ctx)
	if err != nil {
		return IdentityBootstrapResult{}, err
	}
	if err := b.api.ApplyIdentityBootstrapState(ctx, desired); err != nil {
		return IdentityBootstrapResult{}, err
	}
	actual, err := b.api.ReadIdentityBootstrapState(ctx, desired)
	if err != nil {
		return IdentityBootstrapResult{}, err
	}
	if !reflect.DeepEqual(actual, desired) {
		return IdentityBootstrapResult{}, ErrIdentityBootstrapConfig
	}
	return identityBootstrapResult(b.config, accountID, desired), nil
}

// Remove refuses to delete workflow identity while any repository-tagged
// Cloud Lease resource exists, including cleanup-pending residual inventory.
func (b *IdentityBootstrapper) Remove(ctx context.Context) (IdentityBootstrapResult, error) {
	assets, err := b.api.ListAssets(ctx, InventoryQuery{
		Region: b.config.Region, Repository: b.config.Repository,
	})
	if err != nil {
		return IdentityBootstrapResult{}, err
	}
	if len(assets) != 0 {
		return IdentityBootstrapResult{}, fmt.Errorf("%w: %d related resources remain", ErrIdentityBootstrapActiveLeases, len(assets))
	}
	desired, accountID, err := b.desired(ctx)
	if err != nil {
		return IdentityBootstrapResult{}, err
	}
	if err := b.api.RemoveIdentityBootstrapState(ctx, desired); err != nil {
		return IdentityBootstrapResult{}, err
	}
	actual, err := b.api.ReadIdentityBootstrapState(ctx, desired)
	if err != nil {
		return IdentityBootstrapResult{}, err
	}
	if !identityBootstrapStateEmpty(actual) {
		return IdentityBootstrapResult{}, ErrIdentityBootstrapConfig
	}
	return identityBootstrapResult(b.config, accountID, desired), nil
}

func identityBootstrapStateEmpty(state IdentityBootstrapState) bool {
	if !reflect.DeepEqual(state.OIDCProvider, IdentityOIDCProviderSpec{}) {
		return false
	}
	for _, role := range state.Roles {
		if !reflect.DeepEqual(role, IdentityRoleSpec{}) {
			return false
		}
	}
	for _, policy := range state.Policies {
		if !reflect.DeepEqual(policy, IdentityPolicySpec{}) {
			return false
		}
	}
	return true
}

func (b *IdentityBootstrapper) desired(ctx context.Context) (IdentityBootstrapState, string, error) {
	accountID, err := b.api.CallerAccountID(ctx)
	if err != nil {
		return IdentityBootstrapState{}, "", err
	}
	sum := sha256.Sum256([]byte(accountID))
	accountHash := "sha256:" + hex.EncodeToString(sum[:])
	if b.config.ExpectedAccountIDHash != "" && b.config.ExpectedAccountIDHash != accountHash {
		return IdentityBootstrapState{}, "", ErrIdentityBootstrapConfig
	}
	desired, err := DesiredIdentityBootstrapState(b.config, accountID)
	return desired, accountID, err
}

// DesiredIdentityBootstrapState renders the exact three-role Alibaba binding.
func DesiredIdentityBootstrapState(config IdentityBootstrapConfig, accountID string) (IdentityBootstrapState, error) {
	if !validIdentityBootstrapConfig(config) || !identityAccountIDPattern.MatchString(accountID) {
		return IdentityBootstrapState{}, ErrIdentityBootstrapConfig
	}
	providerARN := fmt.Sprintf("acs:ram::%s:oidc-provider/%s", accountID, config.OIDCProviderName)
	roles := []struct {
		name        string
		environment string
		workflow    string
		kind        IdentityPolicyKind
	}{
		{name: CloudLeaseProvisionerRole, environment: CloudLeaseProvisionEnvironment, workflow: CloudLeaseProvisionWorkflow, kind: IdentityPolicyProvisioner},
		{name: CloudLeaseObserverRole, environment: CloudLeaseObserveEnvironment, workflow: CloudLeaseObserveWorkflow, kind: IdentityPolicyObserver},
		{name: CloudLeaseReleaserRole, environment: CloudLeaseReleaseEnvironment, workflow: CloudLeaseReleaseWorkflow, kind: IdentityPolicyReleaser},
	}
	state := IdentityBootstrapState{
		OIDCProvider: IdentityOIDCProviderSpec{
			Name: config.OIDCProviderName, ARN: providerARN,
			IssuerURL: "https://token.actions.githubusercontent.com",
			Audiences: []string{config.OIDCAudience}, Fingerprints: slices.Clone(config.OIDCFingerprints),
		},
		Roles: make([]IdentityRoleSpec, 0, len(roles)), Policies: make([]IdentityPolicySpec, 0, len(roles)),
	}
	sort.Strings(state.OIDCProvider.Fingerprints)
	for _, role := range roles {
		subjects := []string{
			identityGitHubSubject(config, role.environment, CloudLeaseOIDCSetupWorkflow),
			identityGitHubSubject(config, role.environment, role.workflow),
		}
		trust, err := identityTrustPolicy(providerARN, config.OIDCAudience, subjects)
		if err != nil {
			return IdentityBootstrapState{}, err
		}
		document, err := IdentityRolePolicyDocument(role.kind)
		if err != nil {
			return IdentityBootstrapState{}, err
		}
		state.Roles = append(state.Roles, IdentityRoleSpec{
			Name: role.name, ARN: fmt.Sprintf("acs:ram::%s:role/%s", accountID, strings.ToLower(role.name)),
			TrustPolicy: trust, MaxSessionDuration: 3600,
		})
		state.Policies = append(state.Policies, IdentityPolicySpec{Name: role.name, Document: document, AttachedRole: role.name})
	}
	return state, nil
}

// IdentityPolicyKind selects one closed Cloud Lease workflow role.
type IdentityPolicyKind string

const (
	IdentityPolicyProvisioner IdentityPolicyKind = "provisioner"
	IdentityPolicyObserver    IdentityPolicyKind = "observer"
	IdentityPolicyReleaser    IdentityPolicyKind = "releaser"
)

// IdentityRolePolicyDocument renders the exact permission allowlist for one role.
func IdentityRolePolicyDocument(kind IdentityPolicyKind) (string, error) {
	var actions []string
	switch kind {
	case IdentityPolicyProvisioner:
		actions = identityActionUnion(RequiredQuoteActions(), RequiredLifecycleProvisionActions())
	case IdentityPolicyObserver:
		actions = identityActionUnion(RequiredQuoteActions(), RequiredLifecycleObserveActions(), RequiredBillingObserveActions())
	case IdentityPolicyReleaser:
		actions = RequiredLifecycleReleaseActions()
	default:
		return "", ErrIdentityBootstrapConfig
	}
	actions = identityActionUnion(actions, []string{"ram:GetPolicyVersion", "ram:GetRole", "ram:ListPoliciesForRole"})
	document := map[string]any{
		"Version": "1",
		"Statement": []any{map[string]any{
			"Effect": "Allow", "Action": actions, "Resource": []string{"*"},
		}},
	}
	data, err := json.Marshal(document)
	return string(data), err
}

// ExpectedIdentityRoleTrust renders the exact setup and ordinary-workflow
// subjects that a live role self-check must observe.
func ExpectedIdentityRoleTrust(repository, defaultBranch, providerARN, audience, roleName string) (string, error) {
	config := IdentityBootstrapConfig{Repository: repository, DefaultBranch: defaultBranch}
	if strings.Count(repository, "/") != 1 || strings.ContainsAny(repository, " \t\r\n") ||
		strings.TrimSpace(defaultBranch) == "" || strings.ContainsAny(defaultBranch, " ~^:?*[\\") ||
		strings.TrimSpace(audience) == "" || strings.ContainsAny(audience, " \t\r\n") ||
		!strings.HasPrefix(providerARN, "acs:ram::") || !strings.Contains(providerARN, ":oidc-provider/") {
		return "", ErrIdentityBootstrapConfig
	}
	environment, workflow, ok := identityRoleCoordinates(roleName)
	if !ok {
		return "", ErrIdentityBootstrapConfig
	}
	return identityTrustPolicy(providerARN, audience, []string{
		identityGitHubSubject(config, environment, CloudLeaseOIDCSetupWorkflow),
		identityGitHubSubject(config, environment, workflow),
	})
}

func identityRoleCoordinates(roleName string) (string, string, bool) {
	switch roleName {
	case CloudLeaseProvisionerRole:
		return CloudLeaseProvisionEnvironment, CloudLeaseProvisionWorkflow, true
	case CloudLeaseObserverRole:
		return CloudLeaseObserveEnvironment, CloudLeaseObserveWorkflow, true
	case CloudLeaseReleaserRole:
		return CloudLeaseReleaseEnvironment, CloudLeaseReleaseWorkflow, true
	default:
		return "", "", false
	}
}

// CloudLeaseGitHubEnvironments returns the four exact setup targets. The
// deployment Environment deliberately has no Alibaba role.
func CloudLeaseGitHubEnvironments() []string {
	return []string{
		CloudLeaseProvisionEnvironment,
		CloudLeaseObserveEnvironment,
		CloudLeaseReleaseEnvironment,
		CloudDeploymentEnvironment,
	}
}

func identityActionUnion(groups ...[]string) []string {
	seen := make(map[string]struct{})
	for _, group := range groups {
		for _, action := range group {
			seen[action] = struct{}{}
		}
	}
	actions := make([]string, 0, len(seen))
	for action := range seen {
		actions = append(actions, action)
	}
	sort.Strings(actions)
	return actions
}

func identityGitHubSubject(config IdentityBootstrapConfig, environment, workflow string) string {
	workflowRef := fmt.Sprintf("%s/%s@refs/heads/%s", config.Repository, workflow, config.DefaultBranch)
	return fmt.Sprintf("repo:%s:environment:%s:job_workflow_ref:%s", config.Repository, environment, workflowRef)
}

func identityTrustPolicy(providerARN, audience string, subjects []string) (string, error) {
	statements := make([]any, 0, len(subjects))
	for _, subject := range subjects {
		statements = append(statements, map[string]any{
			"Effect": "Allow", "Action": "sts:AssumeRole",
			"Principal": map[string]any{"Federated": []string{providerARN}},
			"Condition": map[string]any{"StringEquals": map[string]any{
				"oidc:iss": "https://token.actions.githubusercontent.com",
				"oidc:aud": []string{audience}, "oidc:sub": subject,
			}},
		})
	}
	data, err := json.Marshal(map[string]any{"Version": "1", "Statement": statements})
	return string(data), err
}

func identityBootstrapPlan(current, desired IdentityBootstrapState) IdentityBootstrapPlan {
	changes := make([]IdentityBootstrapChange, 0, 7)
	appendChange := func(resource string, currentValue, desiredValue any) {
		if reflect.DeepEqual(currentValue, desiredValue) {
			return
		}
		action := "update"
		if reflect.ValueOf(currentValue).IsZero() {
			action = "create"
		}
		changes = append(changes, IdentityBootstrapChange{Resource: resource, Action: action})
	}
	appendChange("oidc_provider", current.OIDCProvider, desired.OIDCProvider)
	for index, role := range desired.Roles {
		var existing IdentityRoleSpec
		if index < len(current.Roles) {
			existing = current.Roles[index]
		}
		appendChange("role:"+role.Name, existing, role)
	}
	for index, policy := range desired.Policies {
		var existing IdentityPolicySpec
		if index < len(current.Policies) {
			existing = current.Policies[index]
		}
		appendChange("policy:"+policy.Name, existing, policy)
	}
	return IdentityBootstrapPlan{Changes: changes}
}

func identityBootstrapResult(config IdentityBootstrapConfig, accountID string, state IdentityBootstrapState) IdentityBootstrapResult {
	roleARNs := make(map[string]string, len(state.Roles))
	for _, role := range state.Roles {
		roleARNs[role.Name] = role.ARN
	}
	sum := sha256.Sum256([]byte(accountID))
	return IdentityBootstrapResult{
		Region: config.Region, AccountIDHash: "sha256:" + hex.EncodeToString(sum[:]),
		OIDCProviderARN: state.OIDCProvider.ARN, OIDCAudience: config.OIDCAudience,
		ProvisionerRoleARN: roleARNs[CloudLeaseProvisionerRole],
		ObserverRoleARN:    roleARNs[CloudLeaseObserverRole], ReleaserRoleARN: roleARNs[CloudLeaseReleaserRole],
	}
}

func validIdentityBootstrapConfig(config IdentityBootstrapConfig) bool {
	if config.Region != RegionHangzhou || strings.Count(config.Repository, "/") != 1 ||
		strings.ContainsAny(config.Repository, " \t\r\n") || strings.TrimSpace(config.DefaultBranch) == "" ||
		strings.ContainsAny(config.DefaultBranch, " ~^:?*[\\") || !identityNamePattern.MatchString(config.OIDCProviderName) ||
		strings.TrimSpace(config.OIDCAudience) == "" || strings.ContainsAny(config.OIDCAudience, " \t\r\n") ||
		len(config.OIDCFingerprints) == 0 || len(config.OIDCFingerprints) > 5 {
		return false
	}
	if config.ExpectedAccountIDHash != "" {
		encoded := strings.TrimPrefix(config.ExpectedAccountIDHash, "sha256:")
		if encoded == config.ExpectedAccountIDHash || len(encoded) != 64 {
			return false
		}
		if _, err := hex.DecodeString(encoded); err != nil {
			return false
		}
	}
	seen := make(map[string]struct{}, len(config.OIDCFingerprints))
	for _, fingerprint := range config.OIDCFingerprints {
		if len(fingerprint) != 40 {
			return false
		}
		if _, err := hex.DecodeString(fingerprint); err != nil {
			return false
		}
		if _, exists := seen[fingerprint]; exists {
			return false
		}
		seen[fingerprint] = struct{}{}
	}
	return true
}

func cloneIdentityBootstrapConfig(config IdentityBootstrapConfig) IdentityBootstrapConfig {
	config.OIDCFingerprints = slices.Clone(config.OIDCFingerprints)
	return config
}
