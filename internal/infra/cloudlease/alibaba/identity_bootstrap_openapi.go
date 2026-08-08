package alibaba

import (
	"context"
	"crypto/sha1"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"

	openapiutil "github.com/alibabacloud-go/darabonba-openapi/v2/utils"
	ecs "github.com/alibabacloud-go/ecs-20140526/v7/client"
	ims "github.com/alibabacloud-go/ims-20190815/v4/client"
	ram "github.com/alibabacloud-go/ram-20150501/v2/client"
	sts "github.com/alibabacloud-go/sts-20150401/v2/client"
	"github.com/alibabacloud-go/tea/dara"
	vpc "github.com/alibabacloud-go/vpc-20160428/v6/client"
)

var (
	// ErrIdentityBootstrapCredentials reports that the existing repository
	// AccessKey Secret pair is missing or partial.
	ErrIdentityBootstrapCredentials = errors.New("internal/infra/cloudlease/alibaba: complete bootstrap AccessKey pair required")
)

// IdentityBootstrapOpenAPI implements only the one-time OIDC/RAM bootstrap
// port plus read-only tagged inventory used by the removal guard.
type IdentityBootstrapOpenAPI struct {
	ims       *ims.Client
	ram       *ram.Client
	sts       *sts.Client
	inventory *OpenAPI
}

var _ IdentityBootstrapAPI = (*IdentityBootstrapOpenAPI)(nil)

// NewIdentityBootstrapOpenAPIFromAccessKeyEnvironment requires the existing
// complete repository Secret pair. It never accepts a partial pair or silently
// falls back to another credential source.
func NewIdentityBootstrapOpenAPIFromAccessKeyEnvironment(region string) (*IdentityBootstrapOpenAPI, error) {
	accessKeyID := strings.TrimSpace(os.Getenv(credentialAccessKeyIDEnv))
	accessKeySecret := os.Getenv(credentialAccessKeySecretEnv)
	if accessKeyID == "" || accessKeySecret == "" {
		if accessKeyID == "" && accessKeySecret == "" {
			return nil, fmt.Errorf("%w: configure both %s and %s repository Secrets", ErrIdentityBootstrapCredentials, credentialAccessKeyIDEnv, credentialAccessKeySecretEnv)
		}
		return nil, fmt.Errorf("%w: %s and %s must be configured together", ErrIdentityBootstrapCredentials, credentialAccessKeyIDEnv, credentialAccessKeySecretEnv)
	}
	if region != RegionHangzhou {
		return nil, ErrIdentityBootstrapConfig
	}
	config := (&openapiutil.Config{}).
		SetAccessKeyId(accessKeyID).
		SetAccessKeySecret(accessKeySecret).
		SetRegionId(region)
	imsClient, err := ims.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("create IMS bootstrap client: %w", err)
	}
	ramClient, err := ram.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("create RAM bootstrap client: %w", err)
	}
	stsClient, err := sts.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("create STS bootstrap client: %w", err)
	}
	ecsClient, err := ecs.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("create ECS bootstrap inventory client: %w", err)
	}
	vpcClient, err := vpc.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("create VPC bootstrap inventory client: %w", err)
	}
	return &IdentityBootstrapOpenAPI{
		ims: imsClient, ram: ramClient, sts: stsClient,
		inventory: &OpenAPI{region: region, ecs: ecsClient, vpc: vpcClient},
	}, nil
}

// CallerAccountID returns the authenticated Alibaba account binding.
func (a *IdentityBootstrapOpenAPI) CallerAccountID(ctx context.Context) (string, error) {
	if a == nil || a.sts == nil {
		return "", ErrIdentityBootstrapConfig
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	response, err := a.sts.GetCallerIdentity()
	if err != nil || response == nil || response.Body == nil {
		return "", fmt.Errorf("bootstrap GetCallerIdentity: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	accountID := strings.TrimSpace(stringValue(response.Body.AccountId))
	if !identityAccountIDPattern.MatchString(accountID) || strings.TrimSpace(stringValue(response.Body.Arn)) == "" {
		return "", ErrIdentityBootstrapConfig
	}
	return accountID, nil
}

// ListAssets delegates only to the tagged read-only inventory implementation.
func (a *IdentityBootstrapOpenAPI) ListAssets(ctx context.Context, query InventoryQuery) ([]LifecycleAsset, error) {
	if a == nil || a.inventory == nil {
		return nil, ErrIdentityBootstrapConfig
	}
	return a.inventory.ListAssets(ctx, query)
}

// ReadIdentityBootstrapState reads the exact provider, roles, policies, and
// attachment cardinality owned by the desired binding.
func (a *IdentityBootstrapOpenAPI) ReadIdentityBootstrapState(ctx context.Context, desired IdentityBootstrapState) (IdentityBootstrapState, error) {
	if a == nil || a.ims == nil || a.ram == nil {
		return IdentityBootstrapState{}, ErrIdentityBootstrapConfig
	}
	var state IdentityBootstrapState
	response, err := a.ims.GetOIDCProviderWithContext(ctx,
		(&ims.GetOIDCProviderRequest{}).SetOIDCProviderName(desired.OIDCProvider.Name), &dara.RuntimeOptions{})
	if err == nil && response.Body != nil && response.Body.OIDCProvider != nil {
		provider := response.Body.OIDCProvider
		state.OIDCProvider = IdentityOIDCProviderSpec{
			Name: strings.TrimSpace(stringValue(provider.OIDCProviderName)), ARN: strings.TrimSpace(stringValue(provider.Arn)),
			IssuerURL: strings.TrimSpace(stringValue(provider.IssuerUrl)),
			Audiences: identitySplitCSV(stringValue(provider.ClientIds)), Fingerprints: identitySplitCSV(stringValue(provider.Fingerprints)),
		}
	} else if err != nil && !identityBootstrapNotFound(err) {
		return IdentityBootstrapState{}, fmt.Errorf("read Cloud Lease OIDC provider: %w", err)
	}
	state.Roles = make([]IdentityRoleSpec, len(desired.Roles))
	state.Policies = make([]IdentityPolicySpec, len(desired.Policies))
	for index, role := range desired.Roles {
		state.Roles[index], err = a.readIdentityRole(ctx, role)
		if err != nil {
			return IdentityBootstrapState{}, err
		}
	}
	for index, policy := range desired.Policies {
		state.Policies[index], err = a.readIdentityPolicy(ctx, policy)
		if err != nil {
			return IdentityBootstrapState{}, err
		}
	}
	return state, nil
}

// ApplyIdentityBootstrapState converges only the seven repository-owned
// identity resources and exact policy attachments.
func (a *IdentityBootstrapOpenAPI) ApplyIdentityBootstrapState(ctx context.Context, desired IdentityBootstrapState) error {
	if err := a.upsertIdentityOIDCProvider(ctx, desired.OIDCProvider); err != nil {
		return err
	}
	for _, role := range desired.Roles {
		if err := a.upsertIdentityRole(ctx, role); err != nil {
			return err
		}
	}
	for _, policy := range desired.Policies {
		if err := a.upsertIdentityPolicy(ctx, policy); err != nil {
			return err
		}
	}
	return nil
}

// RemoveIdentityBootstrapState removes only the exact policies, roles, and
// provider after the caller has proved zero tagged Lease inventory.
func (a *IdentityBootstrapOpenAPI) RemoveIdentityBootstrapState(ctx context.Context, desired IdentityBootstrapState) error {
	var errs []error
	for _, policy := range desired.Policies {
		_, detachErr := a.ram.DetachPolicyFromRoleWithContext(ctx, (&ram.DetachPolicyFromRoleRequest{}).
			SetPolicyName(policy.Name).SetPolicyType("Custom").SetRoleName(policy.AttachedRole), &dara.RuntimeOptions{})
		if detachErr != nil && !identityBootstrapNotFound(detachErr) {
			errs = append(errs, fmt.Errorf("detach identity policy %s: %w", policy.Name, detachErr))
		}
		_, deleteErr := a.ram.DeletePolicyWithContext(ctx,
			(&ram.DeletePolicyRequest{}).SetPolicyName(policy.Name).SetCascadingDelete(true), &dara.RuntimeOptions{})
		if deleteErr != nil && !identityBootstrapNotFound(deleteErr) {
			errs = append(errs, fmt.Errorf("delete identity policy %s: %w", policy.Name, deleteErr))
		}
	}
	for _, role := range desired.Roles {
		_, err := a.ram.DeleteRoleWithContext(ctx, (&ram.DeleteRoleRequest{}).SetRoleName(role.Name), &dara.RuntimeOptions{})
		if err != nil && !identityBootstrapNotFound(err) {
			errs = append(errs, fmt.Errorf("delete identity role %s: %w", role.Name, err))
		}
	}
	_, err := a.ims.DeleteOIDCProviderWithContext(ctx,
		(&ims.DeleteOIDCProviderRequest{}).SetOIDCProviderName(desired.OIDCProvider.Name), &dara.RuntimeOptions{})
	if err != nil && !identityBootstrapNotFound(err) {
		errs = append(errs, fmt.Errorf("delete Cloud Lease OIDC provider: %w", err))
	}
	return errors.Join(errs...)
}

func (a *IdentityBootstrapOpenAPI) readIdentityRole(ctx context.Context, desired IdentityRoleSpec) (IdentityRoleSpec, error) {
	response, err := a.ram.GetRoleWithContext(ctx, (&ram.GetRoleRequest{}).SetRoleName(desired.Name), &dara.RuntimeOptions{})
	if err != nil {
		if identityBootstrapNotFound(err) {
			return IdentityRoleSpec{}, nil
		}
		return IdentityRoleSpec{}, fmt.Errorf("read identity role %s: %w", desired.Name, err)
	}
	if response.Body == nil || response.Body.Role == nil {
		return IdentityRoleSpec{}, nil
	}
	role := response.Body.Role
	return IdentityRoleSpec{
		Name: strings.TrimSpace(stringValue(role.RoleName)), ARN: strings.TrimSpace(stringValue(role.Arn)),
		TrustPolicy:        normalizeRAMPolicyDocument(stringValue(role.AssumeRolePolicyDocument)),
		MaxSessionDuration: dara.Int64Value(role.MaxSessionDuration),
	}, nil
}

func (a *IdentityBootstrapOpenAPI) readIdentityPolicy(ctx context.Context, desired IdentityPolicySpec) (IdentityPolicySpec, error) {
	response, err := a.ram.GetPolicyWithContext(ctx,
		(&ram.GetPolicyRequest{}).SetPolicyName(desired.Name).SetPolicyType("Custom"), &dara.RuntimeOptions{})
	if err != nil {
		if identityBootstrapNotFound(err) {
			return IdentityPolicySpec{}, nil
		}
		return IdentityPolicySpec{}, fmt.Errorf("read identity policy %s: %w", desired.Name, err)
	}
	if response.Body == nil || response.Body.Policy == nil || response.Body.DefaultPolicyVersion == nil {
		return IdentityPolicySpec{}, nil
	}
	attachedRole := ""
	list, listErr := a.ram.ListPoliciesForRoleWithContext(ctx,
		(&ram.ListPoliciesForRoleRequest{}).SetRoleName(desired.AttachedRole), &dara.RuntimeOptions{})
	if listErr != nil && !identityBootstrapNotFound(listErr) {
		return IdentityPolicySpec{}, fmt.Errorf("list identity role policies %s: %w", desired.AttachedRole, listErr)
	}
	if listErr == nil && list != nil && list.Body != nil && list.Body.Policies != nil && len(list.Body.Policies.Policy) == 1 {
		attached := list.Body.Policies.Policy[0]
		if attached != nil && stringValue(attached.PolicyName) == desired.Name && stringValue(attached.PolicyType) == "Custom" {
			attachedRole = desired.AttachedRole
		}
	}
	return IdentityPolicySpec{
		Name:         desired.Name,
		Document:     normalizeRAMPolicyDocument(stringValue(response.Body.DefaultPolicyVersion.PolicyDocument)),
		AttachedRole: attachedRole,
	}, nil
}

func (a *IdentityBootstrapOpenAPI) upsertIdentityOIDCProvider(ctx context.Context, desired IdentityOIDCProviderSpec) error {
	response, err := a.ims.GetOIDCProviderWithContext(ctx,
		(&ims.GetOIDCProviderRequest{}).SetOIDCProviderName(desired.Name), &dara.RuntimeOptions{})
	if err == nil && response.Body != nil && response.Body.OIDCProvider != nil {
		current := response.Body.OIDCProvider
		if strings.TrimSpace(stringValue(current.IssuerUrl)) != desired.IssuerURL {
			return fmt.Errorf("replace OIDC issuer only after protected identity removal: %w", ErrIdentityBootstrapConfig)
		}
		if !slices.Equal(identitySplitCSV(stringValue(current.ClientIds)), desired.Audiences) {
			if _, updateErr := a.ims.UpdateOIDCProviderWithContext(ctx, (&ims.UpdateOIDCProviderRequest{}).
				SetOIDCProviderName(desired.Name).SetClientIds(strings.Join(desired.Audiences, ",")), &dara.RuntimeOptions{}); updateErr != nil {
				return fmt.Errorf("update Cloud Lease OIDC audiences: %w", updateErr)
			}
		}
		currentFingerprints := identitySplitCSV(stringValue(current.Fingerprints))
		for _, fingerprint := range desired.Fingerprints {
			if slices.Contains(currentFingerprints, fingerprint) {
				continue
			}
			if _, addErr := a.ims.AddFingerprintToOIDCProviderWithContext(ctx, (&ims.AddFingerprintToOIDCProviderRequest{}).
				SetOIDCProviderName(desired.Name).SetFingerprint(fingerprint), &dara.RuntimeOptions{}); addErr != nil {
				return fmt.Errorf("add Cloud Lease OIDC fingerprint: %w", addErr)
			}
		}
		for _, fingerprint := range currentFingerprints {
			if slices.Contains(desired.Fingerprints, fingerprint) {
				continue
			}
			if _, removeErr := a.ims.RemoveFingerprintFromOIDCProviderWithContext(ctx, (&ims.RemoveFingerprintFromOIDCProviderRequest{}).
				SetOIDCProviderName(desired.Name).SetFingerprint(fingerprint), &dara.RuntimeOptions{}); removeErr != nil {
				return fmt.Errorf("remove Cloud Lease OIDC fingerprint: %w", removeErr)
			}
		}
		return nil
	}
	if err != nil && !identityBootstrapNotFound(err) {
		return fmt.Errorf("read Cloud Lease OIDC provider: %w", err)
	}
	_, err = a.ims.CreateOIDCProviderWithContext(ctx, (&ims.CreateOIDCProviderRequest{}).
		SetOIDCProviderName(desired.Name).SetIssuerUrl(desired.IssuerURL).
		SetClientIds(strings.Join(desired.Audiences, ",")).SetFingerprints(strings.Join(desired.Fingerprints, ",")).
		SetDescription("WuKongIM Cloud Lease GitHub OIDC"), &dara.RuntimeOptions{})
	if err != nil {
		return fmt.Errorf("create Cloud Lease OIDC provider: %w", err)
	}
	return nil
}

func (a *IdentityBootstrapOpenAPI) upsertIdentityRole(ctx context.Context, desired IdentityRoleSpec) error {
	current, err := a.readIdentityRole(ctx, desired)
	if err != nil {
		return err
	}
	if current.Name == "" {
		_, err = a.ram.CreateRoleWithContext(ctx, (&ram.CreateRoleRequest{}).
			SetRoleName(desired.Name).SetAssumeRolePolicyDocument(desired.TrustPolicy).
			SetMaxSessionDuration(desired.MaxSessionDuration).SetDescription("WuKongIM Cloud Lease workflow role"), &dara.RuntimeOptions{})
	} else if normalizeRAMPolicyDocument(current.TrustPolicy) != normalizeRAMPolicyDocument(desired.TrustPolicy) ||
		current.MaxSessionDuration != desired.MaxSessionDuration {
		_, err = a.ram.UpdateRoleWithContext(ctx, (&ram.UpdateRoleRequest{}).
			SetRoleName(desired.Name).SetNewAssumeRolePolicyDocument(desired.TrustPolicy).
			SetNewMaxSessionDuration(desired.MaxSessionDuration), &dara.RuntimeOptions{})
	}
	if err != nil {
		return fmt.Errorf("upsert identity role %s: %w", desired.Name, err)
	}
	return nil
}

func (a *IdentityBootstrapOpenAPI) upsertIdentityPolicy(ctx context.Context, desired IdentityPolicySpec) error {
	current, err := a.readIdentityPolicy(ctx, desired)
	if err != nil {
		return err
	}
	if current.Name == "" {
		_, err = a.ram.CreatePolicyWithContext(ctx, (&ram.CreatePolicyRequest{}).
			SetPolicyName(desired.Name).SetPolicyDocument(desired.Document).
			SetDescription("WuKongIM Cloud Lease least-privilege policy"), &dara.RuntimeOptions{})
	} else if normalizeRAMPolicyDocument(current.Document) != normalizeRAMPolicyDocument(desired.Document) {
		_, err = a.ram.CreatePolicyVersionWithContext(ctx, (&ram.CreatePolicyVersionRequest{}).
			SetPolicyName(desired.Name).SetPolicyDocument(desired.Document).
			SetSetAsDefault(true).SetRotateStrategy("DeleteOldestNonDefaultVersionWhenLimitExceeded"), &dara.RuntimeOptions{})
	}
	if err != nil {
		return fmt.Errorf("upsert identity policy %s: %w", desired.Name, err)
	}
	list, err := a.ram.ListPoliciesForRoleWithContext(ctx,
		(&ram.ListPoliciesForRoleRequest{}).SetRoleName(desired.AttachedRole), &dara.RuntimeOptions{})
	if err != nil {
		return fmt.Errorf("list attached identity policies %s: %w", desired.AttachedRole, err)
	}
	if list == nil || list.Body == nil || list.Body.Policies == nil {
		return fmt.Errorf("list attached identity policies %s: empty provider response", desired.AttachedRole)
	}
	foundDesired := false
	for _, attached := range list.Body.Policies.Policy {
		if attached == nil {
			continue
		}
		name, policyType := stringValue(attached.PolicyName), stringValue(attached.PolicyType)
		if name == desired.Name && policyType == "Custom" {
			foundDesired = true
			continue
		}
		_, detachErr := a.ram.DetachPolicyFromRoleWithContext(ctx, (&ram.DetachPolicyFromRoleRequest{}).
			SetPolicyName(name).SetPolicyType(policyType).SetRoleName(desired.AttachedRole), &dara.RuntimeOptions{})
		if detachErr != nil {
			return fmt.Errorf("detach unexpected identity policy %s from %s: %w", name, desired.AttachedRole, detachErr)
		}
	}
	if !foundDesired {
		_, err = a.ram.AttachPolicyToRoleWithContext(ctx, (&ram.AttachPolicyToRoleRequest{}).
			SetPolicyName(desired.Name).SetPolicyType("Custom").SetRoleName(desired.AttachedRole), &dara.RuntimeOptions{})
		if err != nil {
			return fmt.Errorf("attach identity policy %s: %w", desired.Name, err)
		}
	}
	return nil
}

// ResolveCloudLeaseGitHubOIDCFingerprints obtains the root CA fingerprints
// Alibaba RAM requires after validating GitHub's issuer with system trust.
func ResolveCloudLeaseGitHubOIDCFingerprints(ctx context.Context) ([]string, error) {
	dialer := &tls.Dialer{Config: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: "token.actions.githubusercontent.com"}}
	connection, err := dialer.DialContext(ctx, "tcp", "token.actions.githubusercontent.com:443")
	if err != nil {
		return nil, fmt.Errorf("resolve GitHub OIDC fingerprint: %w", err)
	}
	defer connection.Close()
	tlsConnection, ok := connection.(*tls.Conn)
	if !ok {
		return nil, ErrIdentityBootstrapConfig
	}
	state := tlsConnection.ConnectionState()
	seen := make(map[string]struct{})
	for _, chain := range state.VerifiedChains {
		if len(chain) == 0 {
			continue
		}
		digest := sha1.Sum(chain[len(chain)-1].Raw) // #nosec G505 -- Alibaba RAM's OIDC contract requires SHA-1.
		seen[hex.EncodeToString(digest[:])] = struct{}{}
	}
	fingerprints := make([]string, 0, len(seen))
	for fingerprint := range seen {
		fingerprints = append(fingerprints, fingerprint)
	}
	sort.Strings(fingerprints)
	if len(fingerprints) == 0 || len(fingerprints) > 5 {
		return nil, ErrIdentityBootstrapConfig
	}
	return fingerprints, nil
}

func identitySplitCSV(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	sort.Strings(result)
	return result
}

func identityBootstrapNotFound(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "entitynotexist") || strings.Contains(message, "nosuchentity") ||
		strings.Contains(message, "not found") || strings.Contains(message, "notfound")
}
