package alibaba

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudsim"
)

var (
	// ErrBootstrapConfig reports unsafe or incomplete cloud-account onboarding input.
	ErrBootstrapConfig = errors.New("internal/infra/cloudsim/alibaba: invalid bootstrap config")
	// ErrBootstrapActiveRuns prevents deleting workflow identity while tagged runs exist.
	ErrBootstrapActiveRuns = errors.New("internal/infra/cloudsim/alibaba: active runs prevent bootstrap removal")
)

// BootstrapConfig contains only non-secret cloud-account and GitHub trust identifiers.
type BootstrapConfig struct {
	// AccountID is the Alibaba account ID visible inside authenticated CloudShell.
	AccountID string `json:"account_id"`
	// Region is the default Simulation Run region printed for GitHub Variables.
	Region string `json:"region"`
	// Repository is the exact GitHub owner/name identity.
	Repository string `json:"repository"`
	// DefaultBranch is the only trusted branch for cloud workflow identity.
	DefaultBranch string `json:"default_branch"`
	// OIDCProviderName is the account-local RAM OIDC provider name.
	OIDCProviderName string `json:"oidc_provider_name"`
	// OIDCAudience is the fixed audience accepted for Alibaba role exchange.
	OIDCAudience string `json:"oidc_audience"`
	// OIDCFingerprints contains verified SHA-1 root CA fingerprints for GitHub's OIDC issuer.
	// CloudShell resolves this list over a system-trusted TLS connection when it is omitted.
	OIDCFingerprints []string `json:"oidc_fingerprints,omitempty"`
	// ProvisionEnvironment is the protected GitHub Environment for billable mutation.
	ProvisionEnvironment string `json:"provision_environment"`
	// CleanupEnvironment is the unattended GitHub Environment used only for lease reconciliation.
	CleanupEnvironment string `json:"cleanup_environment"`
	// AnalysisEnvironment is the protected GitHub Environment for live read access.
	AnalysisEnvironment string `json:"analysis_environment"`
	// ProvisionerRoleName is the least-privilege lifecycle role.
	ProvisionerRoleName string `json:"provisioner_role_name"`
	// AnalyzerRoleName is the inventory and temporary-ingress role.
	AnalyzerRoleName string `json:"analyzer_role_name"`
}

// OIDCProviderSpec is the desired GitHub identity provider.
type OIDCProviderSpec struct {
	// Name is the repository-owned RAM OIDC provider name.
	Name string `json:"name"`
	// ARN is the expected provider ARN in the bound account.
	ARN string `json:"arn"`
	// IssuerURL is GitHub Actions' exact OIDC issuer.
	IssuerURL string `json:"issuer_url"`
	// Audiences contains the single repository bootstrap audience.
	Audiences []string `json:"audiences"`
	// Fingerprints contains the validated issuer root CA fingerprints required by RAM.
	Fingerprints []string `json:"fingerprints"`
}

// RoleSpec is one desired RAM role and exact GitHub trust policy.
type RoleSpec struct {
	// Name is the repository-owned RAM role name.
	Name string `json:"name"`
	// ARN is the expected role ARN in the bound account.
	ARN string `json:"arn"`
	// TrustPolicy is the canonical least-privilege GitHub OIDC trust document.
	TrustPolicy string `json:"trust_policy"`
}

// PolicySpec is one desired custom RAM policy attached to exactly one role.
type PolicySpec struct {
	// Name is the repository-owned custom RAM policy name.
	Name string `json:"name"`
	// Document is the canonical least-privilege permission policy.
	Document string `json:"document"`
	// AttachedRole is the single role that receives this policy.
	AttachedRole string `json:"attached_role"`
}

// BootstrapState is the complete, narrow cloud-account binding owned by this repository.
type BootstrapState struct {
	// OIDCProvider is the desired GitHub identity provider.
	OIDCProvider OIDCProviderSpec `json:"oidc_provider"`
	// ProvisionerRole is used by provision and cleanup workflows.
	ProvisionerRole RoleSpec `json:"provisioner_role"`
	// AnalyzerRole is used only by the live analysis workflow.
	AnalyzerRole RoleSpec `json:"analyzer_role"`
	// ProvisionerPolicy owns billable lifecycle and cleanup operations.
	ProvisionerPolicy PolicySpec `json:"provisioner_policy"`
	// AnalyzerPolicy owns inventory reads and temporary MCP ingress only.
	AnalyzerPolicy PolicySpec `json:"analyzer_policy"`
}

// BootstrapChange describes one idempotent create or update operation.
type BootstrapChange struct {
	// Resource identifies the repository-owned account binding component.
	Resource string `json:"resource"`
	// Action is create or update.
	Action string `json:"action"`
}

// BootstrapPlan is a non-mutating account onboarding diff.
type BootstrapPlan struct {
	// Changes contains deterministic account-binding mutations.
	Changes []BootstrapChange `json:"changes"`
}

// BootstrapResult contains only non-secret values copied to GitHub Variables.
type BootstrapResult struct {
	// AccountID is the non-secret Alibaba account identity.
	AccountID string `json:"account_id"`
	// AccountIDHash is the non-secret run locator binding.
	AccountIDHash string `json:"account_id_hash"`
	// Region is the account binding's fixed simulation region.
	Region string `json:"region"`
	// OIDCProviderARN is copied to a GitHub repository variable.
	OIDCProviderARN string `json:"oidc_provider_arn"`
	// ProvisionerRoleARN is copied to a protected GitHub variable.
	ProvisionerRoleARN string `json:"provisioner_role_arn"`
	// AnalyzerRoleARN is copied to a protected GitHub variable.
	AnalyzerRoleARN string `json:"analyzer_role_arn"`
	// OIDCAudience is copied to the workflows' OIDC action input.
	OIDCAudience string `json:"oidc_audience"`
}

// BootstrapAPI is the CloudShell-admin boundary implemented by RAM and IMS SDKs.
type BootstrapAPI interface {
	// ReadBootstrapState reads the repository-scoped OIDC and RAM bootstrap state.
	ReadBootstrapState(context.Context, BootstrapConfig) (BootstrapState, error)
	// ApplyBootstrapState creates or updates the requested bootstrap state idempotently.
	ApplyBootstrapState(context.Context, BootstrapState) error
	// RemoveBootstrapState removes the exact repository-scoped bootstrap state.
	RemoveBootstrapState(context.Context, BootstrapState) error
	// ActiveRuns returns live runs that make bootstrap removal unsafe.
	ActiveRuns(context.Context) ([]cloudsim.Run, error)
}

// Bootstrapper owns idempotent plan, apply, and protected removal.
type Bootstrapper struct {
	config BootstrapConfig
	api    BootstrapAPI
	now    func() time.Time
}

// NewBootstrapper validates CloudShell onboarding input.
func NewBootstrapper(config BootstrapConfig, api BootstrapAPI, now func() time.Time) (*Bootstrapper, error) {
	if api == nil || !validBootstrapConfig(config) {
		return nil, ErrBootstrapConfig
	}
	if now == nil {
		now = time.Now
	}
	return &Bootstrapper{config: config, api: api, now: now}, nil
}

// Plan returns the resources that Apply would create or update.
func (b *Bootstrapper) Plan(ctx context.Context) (BootstrapPlan, error) {
	desired, err := DesiredBootstrapState(b.config)
	if err != nil {
		return BootstrapPlan{}, err
	}
	current, err := b.api.ReadBootstrapState(ctx, b.config)
	if err != nil {
		return BootstrapPlan{}, err
	}
	changes := make([]BootstrapChange, 0, 5)
	appendChange := func(name string, currentValue, desiredValue any) {
		if reflect.DeepEqual(currentValue, desiredValue) {
			return
		}
		action := "update"
		if reflect.ValueOf(currentValue).IsZero() {
			action = "create"
		}
		changes = append(changes, BootstrapChange{Resource: name, Action: action})
	}
	appendChange("oidc_provider", current.OIDCProvider, desired.OIDCProvider)
	appendChange("provisioner_role", current.ProvisionerRole, desired.ProvisionerRole)
	appendChange("analyzer_role", current.AnalyzerRole, desired.AnalyzerRole)
	appendChange("provisioner_policy", current.ProvisionerPolicy, desired.ProvisionerPolicy)
	appendChange("analyzer_policy", current.AnalyzerPolicy, desired.AnalyzerPolicy)
	return BootstrapPlan{Changes: changes}, nil
}

// Apply converges the five-resource cloud-account binding and returns non-secret identifiers.
func (b *Bootstrapper) Apply(ctx context.Context) (BootstrapResult, error) {
	desired, err := DesiredBootstrapState(b.config)
	if err != nil {
		return BootstrapResult{}, err
	}
	if err := b.api.ApplyBootstrapState(ctx, desired); err != nil {
		return BootstrapResult{}, err
	}
	return bootstrapResult(b.config, desired), nil
}

// Remove deletes only the repository binding and refuses while active runs exist.
func (b *Bootstrapper) Remove(ctx context.Context) (BootstrapResult, error) {
	runs, err := b.api.ActiveRuns(ctx)
	if err != nil {
		return BootstrapResult{}, err
	}
	for _, run := range runs {
		if run.State != cloudsim.StateReleased {
			return BootstrapResult{}, fmt.Errorf("%w: %s", ErrBootstrapActiveRuns, run.ID)
		}
	}
	desired, err := DesiredBootstrapState(b.config)
	if err != nil {
		return BootstrapResult{}, err
	}
	if err := b.api.RemoveBootstrapState(ctx, desired); err != nil {
		return BootstrapResult{}, err
	}
	return bootstrapResult(b.config, desired), nil
}

// DesiredBootstrapState renders deterministic trust and least-privilege policy documents.
func DesiredBootstrapState(config BootstrapConfig) (BootstrapState, error) {
	if !validBootstrapConfig(config) {
		return BootstrapState{}, ErrBootstrapConfig
	}
	providerARN := fmt.Sprintf("acs:ram::%s:oidc-provider/%s", config.AccountID, config.OIDCProviderName)
	provisionerARN := fmt.Sprintf("acs:ram::%s:role/%s", config.AccountID, strings.ToLower(config.ProvisionerRoleName))
	analyzerARN := fmt.Sprintf("acs:ram::%s:role/%s", config.AccountID, strings.ToLower(config.AnalyzerRoleName))
	branchRef := "refs/heads/" + config.DefaultBranch
	provisionWorkflowRef := fmt.Sprintf("%s/.github/workflows/cloud-sim-provision.yml@%s", config.Repository, branchRef)
	cleanupWorkflowRef := fmt.Sprintf("%s/.github/workflows/cloud-sim-cleanup.yml@%s", config.Repository, branchRef)
	analysisWorkflowRef := fmt.Sprintf("%s/.github/workflows/cloud-sim-analyze.yml@%s", config.Repository, branchRef)
	oidcVerificationWorkflowRef := fmt.Sprintf("%s/.github/workflows/cloud-sim-oidc-subject.yml@%s", config.Repository, branchRef)
	provisionTrust, err := trustPolicy(providerARN, config.OIDCAudience, []string{
		customGitHubSubject(config.Repository, config.ProvisionEnvironment, provisionWorkflowRef),
		customGitHubSubject(config.Repository, config.CleanupEnvironment, cleanupWorkflowRef),
	})
	if err != nil {
		return BootstrapState{}, err
	}
	analysisTrust, err := trustPolicy(providerARN, config.OIDCAudience, []string{
		customGitHubSubject(config.Repository, config.AnalysisEnvironment, analysisWorkflowRef),
		customGitHubSubject(config.Repository, config.AnalysisEnvironment, oidcVerificationWorkflowRef),
	})
	if err != nil {
		return BootstrapState{}, err
	}
	provisionDocument, err := permissionPolicy(provisionerActions())
	if err != nil {
		return BootstrapState{}, err
	}
	analysisDocument, err := permissionPolicy(analyzerActions())
	if err != nil {
		return BootstrapState{}, err
	}
	fingerprints := append([]string(nil), config.OIDCFingerprints...)
	sort.Strings(fingerprints)
	return BootstrapState{
		OIDCProvider: OIDCProviderSpec{
			Name: config.OIDCProviderName, ARN: providerARN, IssuerURL: "https://token.actions.githubusercontent.com",
			Audiences: []string{config.OIDCAudience}, Fingerprints: fingerprints,
		},
		ProvisionerRole:   RoleSpec{Name: config.ProvisionerRoleName, ARN: provisionerARN, TrustPolicy: provisionTrust},
		AnalyzerRole:      RoleSpec{Name: config.AnalyzerRoleName, ARN: analyzerARN, TrustPolicy: analysisTrust},
		ProvisionerPolicy: PolicySpec{Name: config.ProvisionerRoleName, Document: provisionDocument, AttachedRole: config.ProvisionerRoleName},
		AnalyzerPolicy:    PolicySpec{Name: config.AnalyzerRoleName, Document: analysisDocument, AttachedRole: config.AnalyzerRoleName},
	}, nil
}

func customGitHubSubject(repository, environment, workflowRef string) string {
	return fmt.Sprintf("repo:%s:environment:%s:job_workflow_ref:%s", repository, environment, workflowRef)
}

func trustPolicy(providerARN, audience string, subjects []string) (string, error) {
	statements := make([]any, 0, len(subjects))
	for _, subject := range subjects {
		statements = append(statements, map[string]any{
			"Effect": "Allow", "Action": "sts:AssumeRole",
			"Principal": map[string]any{"Federated": []string{providerARN}},
			"Condition": map[string]any{"StringEquals": map[string]any{
				"oidc:iss": "https://token.actions.githubusercontent.com", "oidc:aud": []string{audience}, "oidc:sub": subject,
			}},
		})
	}
	document := map[string]any{
		"Version": "1", "Statement": statements,
	}
	data, err := json.Marshal(document)
	return string(data), err
}

func permissionPolicy(actions []string) (string, error) {
	document := map[string]any{
		"Version":   "1",
		"Statement": []any{map[string]any{"Effect": "Allow", "Action": actions, "Resource": []string{"*"}}},
	}
	data, err := json.Marshal(document)
	return string(data), err
}

func provisionerActions() []string {
	return []string{
		"ecs:AssignPrivateIpAddresses", "ecs:AttachDisk", "ecs:AuthorizeSecurityGroup", "ecs:CreateDisk", "ecs:CreateSecurityGroup", "ecs:DeleteDisk", "ecs:DeleteInstance", "ecs:DeleteSecurityGroup",
		"ecs:DescribeAccountAttributes", "ecs:DescribeAvailableResource", "ecs:DescribeDisks", "ecs:DescribeInstances", "ecs:DescribeInstanceTypes", "ecs:DescribePrice",
		"ecs:DescribeSecurityGroupAttribute", "ecs:DescribeSecurityGroups", "ecs:RevokeSecurityGroup", "ecs:RunInstances", "ecs:TagResources",
		"vpc:AllocateEipAddress", "vpc:AssociateEipAddress", "vpc:CreateVSwitch", "vpc:CreateVpc", "vpc:DeleteVSwitch", "vpc:DeleteVpc",
		"vpc:DescribeEipAddresses", "vpc:DescribeVSwitches", "vpc:DescribeVpcAttribute", "vpc:DescribeVpcs", "vpc:ReleaseEipAddress",
		"vpc:TagResources", "vpc:UnassociateEipAddress",
	}
}

func analyzerActions() []string {
	return []string{
		"ecs:AuthorizeSecurityGroup", "ecs:DescribeDisks", "ecs:DescribeInstances", "ecs:DescribeSecurityGroupAttribute",
		"ecs:DescribeSecurityGroups", "ecs:RevokeSecurityGroup", "vpc:DescribeEipAddresses", "vpc:DescribeVSwitches", "vpc:DescribeVpcs",
	}
}

func bootstrapResult(config BootstrapConfig, state BootstrapState) BootstrapResult {
	digest := sha256.Sum256([]byte(config.AccountID))
	return BootstrapResult{
		AccountID: config.AccountID, AccountIDHash: "sha256:" + hex.EncodeToString(digest[:]), Region: config.Region,
		OIDCProviderARN: state.OIDCProvider.ARN, ProvisionerRoleARN: state.ProvisionerRole.ARN,
		AnalyzerRoleARN: state.AnalyzerRole.ARN, OIDCAudience: config.OIDCAudience,
	}
}

// ResolveGitHubOIDCFingerprints obtains the root CA fingerprints Alibaba RAM
// requires by validating GitHub's OIDC endpoint with the CloudShell trust store.
func ResolveGitHubOIDCFingerprints(ctx context.Context) ([]string, error) {
	dialer := &tls.Dialer{Config: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: "token.actions.githubusercontent.com"}}
	connection, err := dialer.DialContext(ctx, "tcp", "token.actions.githubusercontent.com:443")
	if err != nil {
		return nil, fmt.Errorf("resolve GitHub OIDC fingerprint: %w", err)
	}
	defer connection.Close()
	tlsConnection, ok := connection.(*tls.Conn)
	if !ok {
		return nil, ErrBootstrapConfig
	}
	state := tlsConnection.ConnectionState()
	seen := make(map[string]struct{})
	for _, chain := range state.VerifiedChains {
		if len(chain) == 0 {
			continue
		}
		digest := sha1.Sum(chain[len(chain)-1].Raw) // #nosec G505 -- RAM's OIDC fingerprint contract requires SHA-1.
		seen[hex.EncodeToString(digest[:])] = struct{}{}
	}
	fingerprints := make([]string, 0, len(seen))
	for fingerprint := range seen {
		fingerprints = append(fingerprints, fingerprint)
	}
	sort.Strings(fingerprints)
	if len(fingerprints) == 0 || len(fingerprints) > 5 {
		return nil, ErrBootstrapConfig
	}
	return fingerprints, nil
}

func validBootstrapConfig(config BootstrapConfig) bool {
	values := []string{
		config.AccountID, config.Region, config.Repository, config.DefaultBranch, config.OIDCProviderName, config.OIDCAudience,
		config.ProvisionEnvironment, config.CleanupEnvironment, config.AnalysisEnvironment, config.ProvisionerRoleName, config.AnalyzerRoleName,
	}
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return false
		}
	}
	if len(config.OIDCFingerprints) == 0 || len(config.OIDCFingerprints) > 5 {
		return false
	}
	for _, fingerprint := range config.OIDCFingerprints {
		if len(fingerprint) != 40 {
			return false
		}
		if _, err := hex.DecodeString(fingerprint); err != nil {
			return false
		}
	}
	return strings.Count(config.Repository, "/") == 1 && !strings.ContainsAny(config.Repository, " \t\n") &&
		!strings.ContainsAny(config.DefaultBranch, " ~^:?*[\\")
}
