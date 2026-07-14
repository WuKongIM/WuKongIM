package alibaba

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	openapiutil "github.com/alibabacloud-go/darabonba-openapi/v2/utils"
	ims "github.com/alibabacloud-go/ims-20190815/v4/client"
	ram "github.com/alibabacloud-go/ram-20150501/v2/client"
	"github.com/alibabacloud-go/tea/dara"
	"github.com/aliyun/credentials-go/credentials"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudsim"
)

// CloudShellBootstrapAPI implements the one-time account bootstrap with the
// official RAM and IMS SDKs and the CloudShell ambient credential chain.
type CloudShellBootstrapAPI struct {
	ims       *ims.Client
	ram       *ram.Client
	inventory *Provider
}

// NewCloudShellBootstrapAPI creates the administrative bootstrap boundary.
// No AccessKey is generated: the SDK uses CloudShell's ambient credential chain.
func NewCloudShellBootstrapAPI(region string, inventory *Provider) (*CloudShellBootstrapAPI, error) {
	if strings.TrimSpace(region) == "" || inventory == nil {
		return nil, ErrBootstrapConfig
	}
	credential, err := credentials.NewCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("load CloudShell credential: %w", err)
	}
	config := &openapiutil.Config{Credential: credential, RegionId: dara.String(region)}
	imsClient, err := ims.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("create IMS client: %w", err)
	}
	ramClient, err := ram.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("create RAM client: %w", err)
	}
	return &CloudShellBootstrapAPI{ims: imsClient, ram: ramClient, inventory: inventory}, nil
}

// ReadBootstrapState reads only the five repository-owned identity resources.
func (a *CloudShellBootstrapAPI) ReadBootstrapState(ctx context.Context, config BootstrapConfig) (BootstrapState, error) {
	desired, err := DesiredBootstrapState(config)
	if err != nil {
		return BootstrapState{}, err
	}
	var state BootstrapState
	response, err := a.ims.GetOIDCProviderWithContext(ctx,
		(&ims.GetOIDCProviderRequest{}).SetOIDCProviderName(config.OIDCProviderName), nil)
	if err == nil && response.Body != nil && response.Body.OIDCProvider != nil {
		provider := response.Body.OIDCProvider
		state.OIDCProvider = OIDCProviderSpec{
			Name: deref(provider.OIDCProviderName), ARN: deref(provider.Arn), IssuerURL: deref(provider.IssuerUrl),
			Audiences: splitCSV(deref(provider.ClientIds)), Fingerprints: splitCSV(deref(provider.Fingerprints)),
		}
	} else if err != nil && !bootstrapNotFound(err) {
		return BootstrapState{}, fmt.Errorf("read OIDC provider: %w", err)
	}
	state.ProvisionerRole, err = a.readRole(ctx, desired.ProvisionerRole)
	if err != nil {
		return BootstrapState{}, err
	}
	state.AnalyzerRole, err = a.readRole(ctx, desired.AnalyzerRole)
	if err != nil {
		return BootstrapState{}, err
	}
	state.ProvisionerPolicy, err = a.readPolicy(ctx, desired.ProvisionerPolicy)
	if err != nil {
		return BootstrapState{}, err
	}
	state.AnalyzerPolicy, err = a.readPolicy(ctx, desired.AnalyzerPolicy)
	if err != nil {
		return BootstrapState{}, err
	}
	return state, nil
}

// ApplyBootstrapState converges provider, roles, policies, and attachments.
func (a *CloudShellBootstrapAPI) ApplyBootstrapState(ctx context.Context, desired BootstrapState) error {
	if err := a.upsertOIDCProvider(ctx, desired.OIDCProvider); err != nil {
		return err
	}
	for _, role := range []RoleSpec{desired.ProvisionerRole, desired.AnalyzerRole} {
		if err := a.upsertRole(ctx, role); err != nil {
			return err
		}
	}
	for _, policy := range []PolicySpec{desired.ProvisionerPolicy, desired.AnalyzerPolicy} {
		if err := a.upsertPolicy(ctx, policy); err != nil {
			return err
		}
	}
	return nil
}

// RemoveBootstrapState deletes only this repository's policies, roles, and provider.
func (a *CloudShellBootstrapAPI) RemoveBootstrapState(ctx context.Context, desired BootstrapState) error {
	var errs []error
	for _, policy := range []PolicySpec{desired.ProvisionerPolicy, desired.AnalyzerPolicy} {
		_, detachErr := a.ram.DetachPolicyFromRoleWithContext(ctx, (&ram.DetachPolicyFromRoleRequest{}).
			SetPolicyName(policy.Name).SetPolicyType("Custom").SetRoleName(policy.AttachedRole), nil)
		if detachErr != nil && !bootstrapNotFound(detachErr) {
			errs = append(errs, fmt.Errorf("detach policy %s: %w", policy.Name, detachErr))
		}
		_, deleteErr := a.ram.DeletePolicyWithContext(ctx,
			(&ram.DeletePolicyRequest{}).SetPolicyName(policy.Name).SetCascadingDelete(true), nil)
		if deleteErr != nil && !bootstrapNotFound(deleteErr) {
			errs = append(errs, fmt.Errorf("delete policy %s: %w", policy.Name, deleteErr))
		}
	}
	for _, role := range []RoleSpec{desired.ProvisionerRole, desired.AnalyzerRole} {
		_, deleteErr := a.ram.DeleteRoleWithContext(ctx, (&ram.DeleteRoleRequest{}).SetRoleName(role.Name), nil)
		if deleteErr != nil && !bootstrapNotFound(deleteErr) {
			errs = append(errs, fmt.Errorf("delete role %s: %w", role.Name, deleteErr))
		}
	}
	_, providerErr := a.ims.DeleteOIDCProviderWithContext(ctx,
		(&ims.DeleteOIDCProviderRequest{}).SetOIDCProviderName(desired.OIDCProvider.Name), nil)
	if providerErr != nil && !bootstrapNotFound(providerErr) {
		errs = append(errs, fmt.Errorf("delete OIDC provider: %w", providerErr))
	}
	return errors.Join(errs...)
}

// ActiveRuns delegates the protected removal check to provider inventory.
func (a *CloudShellBootstrapAPI) ActiveRuns(ctx context.Context) ([]cloudsim.Run, error) {
	return a.inventory.Inventory(ctx)
}

func (a *CloudShellBootstrapAPI) readRole(ctx context.Context, desired RoleSpec) (RoleSpec, error) {
	response, err := a.ram.GetRoleWithContext(ctx, (&ram.GetRoleRequest{}).SetRoleName(desired.Name), nil)
	if err != nil {
		if bootstrapNotFound(err) {
			return RoleSpec{}, nil
		}
		return RoleSpec{}, fmt.Errorf("read role %s: %w", desired.Name, err)
	}
	if response.Body == nil || response.Body.Role == nil {
		return RoleSpec{}, nil
	}
	return RoleSpec{Name: deref(response.Body.Role.RoleName), ARN: deref(response.Body.Role.Arn),
		TrustPolicy: normalizePolicyDocument(deref(response.Body.Role.AssumeRolePolicyDocument))}, nil
}

func (a *CloudShellBootstrapAPI) readPolicy(ctx context.Context, desired PolicySpec) (PolicySpec, error) {
	response, err := a.ram.GetPolicyWithContext(ctx,
		(&ram.GetPolicyRequest{}).SetPolicyName(desired.Name).SetPolicyType("Custom"), nil)
	if err != nil {
		if bootstrapNotFound(err) {
			return PolicySpec{}, nil
		}
		return PolicySpec{}, fmt.Errorf("read policy %s: %w", desired.Name, err)
	}
	if response.Body == nil || response.Body.Policy == nil || response.Body.DefaultPolicyVersion == nil {
		return PolicySpec{}, nil
	}
	attached := false
	list, listErr := a.ram.ListPoliciesForRoleWithContext(ctx,
		(&ram.ListPoliciesForRoleRequest{}).SetRoleName(desired.AttachedRole), nil)
	if listErr != nil && !bootstrapNotFound(listErr) {
		return PolicySpec{}, fmt.Errorf("list role policies %s: %w", desired.AttachedRole, listErr)
	}
	if listErr == nil && list.Body != nil && list.Body.Policies != nil {
		for _, item := range list.Body.Policies.Policy {
			if deref(item.PolicyName) == desired.Name && deref(item.PolicyType) == "Custom" {
				attached = true
				break
			}
		}
	}
	attachedRole := ""
	if attached {
		attachedRole = desired.AttachedRole
	}
	return PolicySpec{Name: desired.Name,
		Document: normalizePolicyDocument(deref(response.Body.DefaultPolicyVersion.PolicyDocument)), AttachedRole: attachedRole}, nil
}

func (a *CloudShellBootstrapAPI) upsertOIDCProvider(ctx context.Context, desired OIDCProviderSpec) error {
	response, err := a.ims.GetOIDCProviderWithContext(ctx,
		(&ims.GetOIDCProviderRequest{}).SetOIDCProviderName(desired.Name), nil)
	if err == nil && response.Body != nil && response.Body.OIDCProvider != nil {
		current := response.Body.OIDCProvider
		if deref(current.IssuerUrl) != desired.IssuerURL {
			return fmt.Errorf("replace OIDC issuer only through protected bootstrap remove: %w", ErrBootstrapConfig)
		}
		if !slicesEqual(splitCSV(deref(current.ClientIds)), desired.Audiences) {
			if _, updateErr := a.ims.UpdateOIDCProviderWithContext(ctx, (&ims.UpdateOIDCProviderRequest{}).
				SetOIDCProviderName(desired.Name).SetClientIds(strings.Join(desired.Audiences, ",")), nil); updateErr != nil {
				return fmt.Errorf("update OIDC audiences: %w", updateErr)
			}
		}
		currentFingerprints := splitCSV(deref(current.Fingerprints))
		for _, fingerprint := range desired.Fingerprints {
			if containsString(currentFingerprints, fingerprint) {
				continue
			}
			if _, addErr := a.ims.AddFingerprintToOIDCProviderWithContext(ctx, (&ims.AddFingerprintToOIDCProviderRequest{}).
				SetOIDCProviderName(desired.Name).SetFingerprint(fingerprint), nil); addErr != nil {
				return fmt.Errorf("add OIDC fingerprint: %w", addErr)
			}
		}
		for _, fingerprint := range currentFingerprints {
			if containsString(desired.Fingerprints, fingerprint) {
				continue
			}
			if _, removeErr := a.ims.RemoveFingerprintFromOIDCProviderWithContext(ctx, (&ims.RemoveFingerprintFromOIDCProviderRequest{}).
				SetOIDCProviderName(desired.Name).SetFingerprint(fingerprint), nil); removeErr != nil {
				return fmt.Errorf("remove OIDC fingerprint: %w", removeErr)
			}
		}
		return nil
	} else if err != nil && !bootstrapNotFound(err) {
		return fmt.Errorf("read OIDC provider: %w", err)
	}
	_, err = a.ims.CreateOIDCProviderWithContext(ctx, (&ims.CreateOIDCProviderRequest{}).
		SetOIDCProviderName(desired.Name).SetIssuerUrl(desired.IssuerURL).
		SetClientIds(strings.Join(desired.Audiences, ",")).SetFingerprints(strings.Join(desired.Fingerprints, ",")).
		SetDescription("WuKongIM cloud simulation GitHub OIDC"), nil)
	if err != nil {
		return fmt.Errorf("create OIDC provider: %w", err)
	}
	return nil
}

func (a *CloudShellBootstrapAPI) upsertRole(ctx context.Context, desired RoleSpec) error {
	current, err := a.readRole(ctx, desired)
	if err != nil {
		return err
	}
	if current.Name == "" {
		_, err = a.ram.CreateRoleWithContext(ctx, (&ram.CreateRoleRequest{}).
			SetRoleName(desired.Name).SetAssumeRolePolicyDocument(desired.TrustPolicy).
			SetMaxSessionDuration(3600).SetDescription("WuKongIM cloud simulation workflow role"), nil)
	} else if normalizePolicyDocument(current.TrustPolicy) != normalizePolicyDocument(desired.TrustPolicy) {
		_, err = a.ram.UpdateRoleWithContext(ctx, (&ram.UpdateRoleRequest{}).
			SetRoleName(desired.Name).SetNewAssumeRolePolicyDocument(desired.TrustPolicy).SetNewMaxSessionDuration(3600), nil)
	}
	if err != nil {
		return fmt.Errorf("upsert role %s: %w", desired.Name, err)
	}
	return nil
}

func (a *CloudShellBootstrapAPI) upsertPolicy(ctx context.Context, desired PolicySpec) error {
	current, err := a.readPolicy(ctx, desired)
	if err != nil {
		return err
	}
	if current.Name == "" {
		_, err = a.ram.CreatePolicyWithContext(ctx, (&ram.CreatePolicyRequest{}).
			SetPolicyName(desired.Name).SetPolicyDocument(desired.Document).
			SetDescription("WuKongIM cloud simulation least-privilege policy"), nil)
	} else if normalizePolicyDocument(current.Document) != normalizePolicyDocument(desired.Document) {
		_, err = a.ram.CreatePolicyVersionWithContext(ctx, (&ram.CreatePolicyVersionRequest{}).
			SetPolicyName(desired.Name).SetPolicyDocument(desired.Document).
			SetSetAsDefault(true).SetRotateStrategy("DeleteOldestNonDefaultVersionWhenLimitExceeded"), nil)
	}
	if err != nil {
		return fmt.Errorf("upsert policy %s: %w", desired.Name, err)
	}
	if current.AttachedRole != desired.AttachedRole {
		_, err = a.ram.AttachPolicyToRoleWithContext(ctx, (&ram.AttachPolicyToRoleRequest{}).
			SetPolicyName(desired.Name).SetPolicyType("Custom").SetRoleName(desired.AttachedRole), nil)
		if err != nil {
			return fmt.Errorf("attach policy %s: %w", desired.Name, err)
		}
	}
	return nil
}

func splitCSV(value string) []string {
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

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func slicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func normalizePolicyDocument(document string) string {
	if decoded, err := url.QueryUnescape(document); err == nil {
		document = decoded
	}
	var value any
	if json.Unmarshal([]byte(document), &value) != nil {
		return document
	}
	data, _ := json.Marshal(value)
	return string(data)
}

func bootstrapNotFound(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "entitynotexist") || strings.Contains(message, "nosuchentity") ||
		strings.Contains(message, "not found") || strings.Contains(message, "notfound")
}
