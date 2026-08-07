package cloudlease

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// Controller enforces provider-neutral Cloud Lease identity, budget, expiry,
// and reconciliation invariants.
type Controller struct {
	// provider is the sole infrastructure authority for this Controller.
	provider Provider
	// now supplies an injectable lifecycle clock; every use is normalized to UTC.
	now func() time.Time
}

// NewController creates one Cloud Lease lifecycle authority.
func NewController(provider Provider, now func() time.Time) *Controller {
	if now == nil {
		now = time.Now
	}
	return &Controller{provider: provider, now: now}
}

// Quote validates a Plan and obtains one side-effect-free provider decision.
func (c *Controller) Quote(ctx context.Context, plan Plan) (Quote, error) {
	now := c.nowUTC()
	normalized, digest, err := normalizeAndValidatePlan(plan, now)
	if err != nil {
		return Quote{}, err
	}
	if c == nil || c.provider == nil || c.provider.Name() != normalized.Provider {
		return Quote{}, ErrInvalidPlan
	}
	quote, err := c.provider.Quote(ctx, QuoteRequest{Plan: clonePlan(normalized), PlanDigest: digest})
	if err != nil {
		return Quote{}, fmt.Errorf("cloudlease quote: %w", err)
	}
	if quote.PlanDigest == "" {
		quote.PlanDigest = digest
	}
	if err := validateQuote(normalized, digest, quote, now); err != nil {
		return Quote{}, err
	}
	return cloneQuote(quote), nil
}

// Acquire creates a Lease once or returns the matching provider inventory after
// an exact idempotent retry.
func (c *Controller) Acquire(ctx context.Context, plan Plan, quote Quote) (Receipt, error) {
	return c.acquire(ctx, plan, quote, BootstrapAccess{})
}

// AcquireWithBootstrap creates a Lease whose first boot installs the exact
// normalized public identities. Their digest participates in retry identity.
func (c *Controller) AcquireWithBootstrap(ctx context.Context, plan Plan, quote Quote, access BootstrapAccess) (Receipt, error) {
	return c.acquire(ctx, plan, quote, access)
}

func (c *Controller) acquire(ctx context.Context, plan Plan, quote Quote, access BootstrapAccess) (Receipt, error) {
	now := c.nowUTC()
	normalized, digest, err := normalizeAndValidatePlan(plan, now)
	if err != nil {
		return Receipt{}, err
	}
	keys, accessDigest, err := normalizeBootstrapAccess(access)
	if err != nil {
		return Receipt{}, err
	}
	expectedTags := baseTags(normalized, digest, now)
	if accessDigest != "" {
		expectedTags[TagBootstrapAccessDigest] = accessDigest
	}
	if c == nil || c.provider == nil || c.provider.Name() != normalized.Provider {
		return Receipt{}, ErrInvalidPlan
	}
	selector := selectorFor(normalized, digest)
	existing, inspectErr := c.provider.Inspect(ctx, selector)
	switch {
	case inspectErr == nil:
		return reconcileExistingAcquireReceipt(normalized, digest, existing, expectedTags)
	case !errors.Is(inspectErr, ErrLeaseNotFound):
		return Receipt{}, fmt.Errorf("cloudlease acquire inspect: %w", inspectErr)
	}
	if err := validateQuote(normalized, digest, quote, now); err != nil {
		return Receipt{}, err
	}

	request := AcquireRequest{
		Plan: clonePlan(normalized), PlanDigest: digest, RequestedAt: now,
		Quote: cloneQuote(quote), BaseTags: expectedTags, BootstrapAuthorizedKeys: keys,
	}
	acquired, acquireErr := c.provider.Acquire(ctx, request)
	if acquireErr == nil {
		receipt, receiptErr := reconcileNewAcquireReceipt(normalized, quote, acquired, expectedTags)
		if receiptErr != nil {
			if errors.Is(receiptErr, ErrResidualResources) {
				return receipt, errors.Join(ErrAcquireIncomplete, receiptErr)
			}
			return receipt, receiptErr
		}
		return receipt, nil
	}

	recovered, recoverErr := c.provider.Inspect(ctx, selector)
	if recoverErr == nil {
		receipt, receiptErr := reconcileExistingAcquireReceipt(normalized, digest, recovered, expectedTags)
		if receiptErr != nil {
			if !errors.Is(receiptErr, ErrAcquireIncomplete) && !errors.Is(receiptErr, ErrResidualResources) {
				return receipt, receiptErr
			}
			return receipt, fmt.Errorf("%w: %v", errors.Join(ErrAcquireIncomplete, receiptErr), acquireErr)
		}
		if receipt.State == StateActive {
			return receipt, nil
		}
		return receipt, fmt.Errorf("%w: %v", ErrAcquireIncomplete, acquireErr)
	}
	if !errors.Is(recoverErr, ErrLeaseNotFound) {
		return Receipt{}, fmt.Errorf("cloudlease acquire: %v; inspect: %w", acquireErr, recoverErr)
	}
	return Receipt{}, fmt.Errorf("cloudlease acquire: %w", acquireErr)
}

func normalizeBootstrapAccess(access BootstrapAccess) ([]string, string, error) {
	if len(access.AuthorizedKeys) == 0 {
		return nil, "", nil
	}
	if len(access.AuthorizedKeys) > 8 {
		return nil, "", ErrInvalidPlan
	}
	keys := make([]string, 0, len(access.AuthorizedKeys))
	seen := make(map[string]struct{}, len(access.AuthorizedKeys))
	for _, raw := range access.AuthorizedKeys {
		trimmed := strings.TrimSpace(raw)
		publicKey, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(trimmed))
		if err != nil || publicKey.Type() != ssh.KeyAlgoED25519 || len(strings.TrimSpace(string(rest))) != 0 || strings.ContainsAny(trimmed, "\r\n") {
			return nil, "", ErrInvalidPlan
		}
		key := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(publicKey)))
		if _, exists := seen[key]; exists {
			return nil, "", ErrInvalidPlan
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	slices.Sort(keys)
	sum := sha256.Sum256([]byte(strings.Join(keys, "\n")))
	return keys, "sha256:" + hex.EncodeToString(sum[:]), nil
}

// Inspect validates one exact provider-reconciled Lease Receipt.
func (c *Controller) Inspect(ctx context.Context, selector Selector) (Receipt, error) {
	if c == nil || c.provider == nil || validateSelector(selector) != nil ||
		c.provider.Name() != selector.Provider {
		return Receipt{}, ErrInvalidPlan
	}
	receipt, err := c.provider.Inspect(ctx, selector)
	if err != nil {
		return Receipt{}, err
	}
	if err := validateReceipt(selector, receipt); err != nil {
		return Receipt{}, err
	}
	return cloneReceipt(receipt), nil
}

// ValidateReceipt independently checks a provider-reconciled Receipt against
// its own exact selector identity. Consumers such as Deployment can validate a
// persisted non-secret Receipt without receiving a cloud Provider capability.
func ValidateReceipt(receipt Receipt) error {
	return validateReceipt(selectorFromReceipt(receipt), receipt)
}

// GrantAccess creates one typed access rule or returns the existing exact grant.
func (c *Controller) GrantAccess(ctx context.Context, selector Selector, grant AccessGrant) (Receipt, error) {
	receipt, err := c.Inspect(ctx, selector)
	if err != nil {
		return Receipt{}, err
	}
	now := c.nowUTC()
	if receipt.State != StateActive || !receipt.ExpiresAt.After(now) {
		return Receipt{}, ErrInvalidAccess
	}
	roles := make(map[string]struct{}, len(receipt.Resources))
	for _, resource := range receipt.Resources {
		roles[resource.Role] = struct{}{}
	}
	grant.ID = strings.TrimSpace(grant.ID)
	grant.TargetRole = strings.TrimSpace(grant.TargetRole)
	grant.Until = grant.Until.UTC()
	if err := validateAccessGrant(grant, now, receipt.ExpiresAt, roles); err != nil {
		return Receipt{}, err
	}
	for _, existing := range receipt.AccessGrants {
		if existing.ID != grant.ID {
			continue
		}
		if existing == grant {
			return receipt, nil
		}
		return Receipt{}, ErrInvalidAccess
	}
	updated, err := c.provider.GrantAccess(ctx, selector, grant)
	if err != nil {
		return Receipt{}, fmt.Errorf("cloudlease grant access: %w", err)
	}
	if err := validateReceipt(selector, updated); err != nil {
		return Receipt{}, err
	}
	count := 0
	for _, existing := range updated.AccessGrants {
		if existing.ID == grant.ID {
			if existing != grant {
				return Receipt{}, ErrProviderInvariant
			}
			count++
		}
	}
	if count != 1 {
		return Receipt{}, ErrProviderInvariant
	}
	return cloneReceipt(updated), nil
}

// RevokeAccess removes one exact grant and succeeds without mutation when it is absent.
func (c *Controller) RevokeAccess(ctx context.Context, selector Selector, grantID string) (Receipt, error) {
	grantID = strings.TrimSpace(grantID)
	if !validIdentity(grantID) {
		return Receipt{}, ErrInvalidAccess
	}
	receipt, err := c.Inspect(ctx, selector)
	if err != nil {
		return Receipt{}, err
	}
	found := false
	for _, grant := range receipt.AccessGrants {
		if grant.ID == grantID {
			found = true
			break
		}
	}
	if !found {
		return receipt, nil
	}
	updated, err := c.provider.RevokeAccess(ctx, selector, grantID)
	if err != nil {
		return Receipt{}, fmt.Errorf("cloudlease revoke access: %w", err)
	}
	if err := validateReceipt(selector, updated); err != nil {
		return Receipt{}, err
	}
	for _, grant := range updated.AccessGrants {
		if grant.ID == grantID {
			return Receipt{}, ErrProviderInvariant
		}
	}
	return cloneReceipt(updated), nil
}

// Release requests complete teardown and succeeds only with an exact
// zero-inventory proof. One idempotent retry reconciles an ambiguous provider
// response without relying on a retained tombstone.
func (c *Controller) Release(ctx context.Context, selector Selector) (ReleaseResult, error) {
	if c == nil || c.provider == nil || validateSelector(selector) != nil ||
		c.provider.Name() != selector.Provider {
		return ReleaseResult{}, ErrInvalidPlan
	}
	result, releaseErr := c.provider.Release(ctx, selector)
	if validateReleaseResult(selector, result) == nil {
		return classifyReleaseResult(result, releaseErr)
	}
	if releaseErr == nil {
		return ReleaseResult{}, ErrProviderInvariant
	}

	retried, retryErr := c.provider.Release(ctx, selector)
	if validateReleaseResult(selector, retried) == nil {
		return classifyReleaseResult(retried, retryErr)
	}
	if retryErr == nil {
		return ReleaseResult{}, ErrProviderInvariant
	}
	return ReleaseResult{}, fmt.Errorf("cloudlease release: %v; retry: %w", releaseErr, retryErr)
}

// Sweep revokes expired access and releases expired or cleanup-pending Leases.
func (c *Controller) Sweep(ctx context.Context, request SweepRequest) (SweepResult, error) {
	result := SweepResult{
		RevokedAccess: make([]string, 0),
		Released:      make([]string, 0),
		Pending:       make([]string, 0),
		Failed:        make([]SweepFailure, 0),
	}
	repository := strings.TrimSpace(request.Repository)
	if c == nil || c.provider == nil || repository == "" {
		return result, ErrInvalidPlan
	}
	receipts, err := c.provider.List(ctx, InventoryFilter{Repository: repository})
	if err != nil {
		return result, fmt.Errorf("cloudlease sweep list: %w", err)
	}
	slices.SortFunc(receipts, func(left, right Receipt) int {
		return strings.Compare(left.LeaseID, right.LeaseID)
	})
	result.Examined = len(receipts)
	seen := make(map[string]struct{}, len(receipts))
	now := c.nowUTC()
	for _, receipt := range receipts {
		selector := selectorFromReceipt(receipt)
		if receipt.Repository != repository || receipt.Provider != c.provider.Name() ||
			validateSelector(selector) != nil || validateReceipt(selector, receipt) != nil {
			result.Failed = append(result.Failed, SweepFailure{LeaseID: receipt.LeaseID, Reason: "invalid_receipt"})
			continue
		}
		if _, exists := seen[receipt.LeaseID]; exists {
			return result, ErrProviderInvariant
		}
		seen[receipt.LeaseID] = struct{}{}
		if receipt.State == StateReleased {
			continue
		}

		grants := slices.Clone(receipt.AccessGrants)
		slices.SortFunc(grants, func(left, right AccessGrant) int {
			return strings.Compare(left.ID, right.ID)
		})
		revokeFailed := false
		for _, grant := range grants {
			if grant.Until.After(now) {
				continue
			}
			if _, revokeErr := c.RevokeAccess(ctx, selector, grant.ID); revokeErr != nil {
				result.Failed = append(result.Failed, SweepFailure{LeaseID: receipt.LeaseID, Reason: "revoke_access"})
				revokeFailed = true
				break
			}
			result.RevokedAccess = append(result.RevokedAccess, receipt.LeaseID+"/"+grant.ID)
		}
		if revokeFailed && receipt.ExpiresAt.After(now) && receipt.State != StateReleasePending {
			continue
		}
		if receipt.ExpiresAt.After(now) && receipt.State != StateReleasePending {
			continue
		}
		_, releaseErr := c.Release(ctx, selector)
		switch {
		case releaseErr == nil:
			result.Released = append(result.Released, receipt.LeaseID)
		case errors.Is(releaseErr, ErrResidualResources):
			result.Pending = append(result.Pending, receipt.LeaseID)
		default:
			result.Failed = append(result.Failed, SweepFailure{LeaseID: receipt.LeaseID, Reason: "release"})
		}
	}
	return result, nil
}

func validateReleaseResult(selector Selector, result ReleaseResult) error {
	if (result.Receipt == nil) == (result.ZeroInventory == nil) {
		return ErrProviderInvariant
	}
	if result.Receipt != nil {
		if err := validateReceipt(selector, *result.Receipt); err != nil {
			return err
		}
		if result.Receipt.State != StateReleasePending || len(result.Receipt.Resources) == 0 {
			return ErrProviderInvariant
		}
		return nil
	}
	proof := result.ZeroInventory
	if proof.Selector != selector || proof.AccountIDHash == "" ||
		proof.AccountIDHash != strings.TrimSpace(proof.AccountIDHash) ||
		proof.ObservedAt.IsZero() || len(proof.Scopes) == 0 || !slices.IsSorted(proof.Scopes) {
		return ErrProviderInvariant
	}
	previous := ""
	for _, scope := range proof.Scopes {
		if !validIdentity(scope) || scope == previous {
			return ErrProviderInvariant
		}
		previous = scope
	}
	return nil
}

func classifyReleaseResult(result ReleaseResult, providerErr error) (ReleaseResult, error) {
	result = cloneReleaseResult(result)
	if result.ZeroInventory != nil {
		return result, nil
	}
	if providerErr != nil {
		return result, fmt.Errorf("%w: %v", ErrResidualResources, providerErr)
	}
	return result, ErrResidualResources
}

func (c *Controller) nowUTC() time.Time {
	if c == nil || c.now == nil {
		return time.Now().UTC()
	}
	return c.now().UTC()
}

func validateSelector(selector Selector) error {
	if selector.LeaseID != strings.TrimSpace(selector.LeaseID) ||
		selector.RequestID != strings.TrimSpace(selector.RequestID) ||
		selector.Provider != strings.TrimSpace(selector.Provider) ||
		selector.Region != strings.TrimSpace(selector.Region) ||
		selector.Repository != strings.TrimSpace(selector.Repository) ||
		selector.PlanDigest != strings.TrimSpace(selector.PlanDigest) ||
		!validIdentity(selector.LeaseID) || !validIdentity(selector.RequestID) ||
		selector.Provider == "" || selector.Region == "" || selector.Repository == "" || selector.PlanDigest == "" {
		return ErrInvalidPlan
	}
	return nil
}

func reconcileExistingAcquireReceipt(plan Plan, digest string, receipt Receipt, expectedTags map[string]string) (Receipt, error) {
	if err := validateAcquireReceiptForPlan(plan, digest, receipt, expectedTags); err != nil {
		return cloneReceipt(receipt), err
	}
	return classifyAcquireReceipt(receipt)
}

func reconcileNewAcquireReceipt(plan Plan, quote Quote, receipt Receipt, expectedTags map[string]string) (Receipt, error) {
	if err := validateAcquireReceiptForPlan(plan, quote.PlanDigest, receipt, expectedTags); err != nil {
		return cloneReceipt(receipt), err
	}
	if receipt.Zone != quote.Zone || !quotesMatchAdmitted(receipt.Quote, quote) ||
		receipt.AccountIDHash != quote.AccountIDHash {
		return cloneReceipt(receipt), ErrProviderInvariant
	}
	return classifyAcquireReceipt(receipt)
}

func validateAcquireReceiptForPlan(plan Plan, digest string, receipt Receipt, expectedTags map[string]string) error {
	selector := selectorFor(plan, digest)
	if err := validateReceiptIdentity(selector, receipt); err != nil {
		return ErrLeaseConflict
	}
	if !receipt.ExpiresAt.Equal(plan.ExpiresAt) || receipt.Operator != plan.Operator ||
		receipt.Provenance != plan.Provenance {
		return ErrLeaseConflict
	}
	wantTags := maps.Clone(expectedTags)
	wantTags[TagCreatedAt] = receipt.CreatedAt.UTC().Format(time.RFC3339Nano)
	if !maps.Equal(receipt.Tags, wantTags) {
		return ErrLeaseConflict
	}
	if receipt.State == StateActive && !accessGrantsMatch(plan.Network.InitialAccess, receipt.AccessGrants) {
		return ErrProviderInvariant
	}
	if err := validateReceipt(selector, receipt); err != nil {
		return err
	}
	return nil
}

func accessGrantsMatch(expected, actual []AccessGrant) bool {
	if len(expected) != len(actual) {
		return false
	}
	byID := make(map[string]AccessGrant, len(actual))
	for _, grant := range actual {
		if _, exists := byID[grant.ID]; exists {
			return false
		}
		byID[grant.ID] = grant
	}
	for _, grant := range expected {
		if actualGrant, exists := byID[grant.ID]; !exists || actualGrant != grant {
			return false
		}
	}
	return true
}

func classifyAcquireReceipt(receipt Receipt) (Receipt, error) {
	if receipt.State == StateReleased {
		return cloneReceipt(receipt), ErrLeaseReleased
	}
	switch receipt.State {
	case StateActive:
		return cloneReceipt(receipt), nil
	case StateAcquiring:
		return cloneReceipt(receipt), ErrAcquireIncomplete
	case StateReleasePending:
		return cloneReceipt(receipt), ErrResidualResources
	default:
		return cloneReceipt(receipt), ErrProviderInvariant
	}
}

func validateReceipt(selector Selector, receipt Receipt) error {
	if err := validateReceiptIdentity(selector, receipt); err != nil {
		return err
	}
	if receipt.Operator == "" || receipt.Zone == "" ||
		receipt.Quote.LeaseID != receipt.LeaseID || receipt.Quote.RequestID != receipt.RequestID ||
		receipt.Quote.Provider != receipt.Provider || receipt.Quote.Region != receipt.Region ||
		receipt.Quote.Zone != receipt.Zone || receipt.Quote.PlanDigest != receipt.PlanDigest ||
		receipt.AccountIDHash != receipt.Quote.AccountIDHash ||
		receipt.Quote.Currency == "" || receipt.Quote.EstimatedCostMicros <= 0 {
		return ErrProviderInvariant
	}
	if err := validateProvenance(receipt.Provenance); err != nil {
		return ErrProviderInvariant
	}
	expectedTags := map[string]string{
		TagManagedBy:  ManagedByValue,
		TagLeaseID:    receipt.LeaseID,
		TagRequestID:  receipt.RequestID,
		TagRepository: receipt.Repository,
		TagOperator:   receipt.Operator,
		TagProvider:   receipt.Provider,
		TagRegion:     receipt.Region,
		TagPlanDigest: receipt.PlanDigest,
		TagCreatedAt:  receipt.CreatedAt.UTC().Format(time.RFC3339Nano),
		TagExpiresAt:  receipt.ExpiresAt.UTC().Format(time.RFC3339Nano),
	}
	addProvenanceTags(expectedTags, receipt.Provenance)
	for key, value := range expectedTags {
		if receipt.Tags[key] != value {
			return ErrProviderInvariant
		}
	}
	if receipt.Provenance.SourceSHA == "" {
		if _, exists := receipt.Tags[TagSourceSHA]; exists {
			return ErrProviderInvariant
		}
	}
	if receipt.Provenance.BundleDigest == "" {
		if _, exists := receipt.Tags[TagBundleDigest]; exists {
			return ErrProviderInvariant
		}
	}
	if _, exists := receipt.Tags[TagResourceRole]; exists {
		return ErrProviderInvariant
	}
	return validateReceiptResources(receipt, receipt.Tags)
}

func validateReceiptIdentity(selector Selector, receipt Receipt) error {
	if receipt.LeaseID != selector.LeaseID || receipt.RequestID != selector.RequestID ||
		receipt.Provider != selector.Provider || receipt.Region != selector.Region ||
		receipt.Repository != selector.Repository || receipt.PlanDigest != selector.PlanDigest ||
		receipt.CreatedAt.IsZero() || receipt.ExpiresAt.IsZero() || receipt.CreatedAt.After(receipt.ExpiresAt) {
		return ErrProviderInvariant
	}
	return nil
}

func validateReceiptResources(receipt Receipt, expectedBaseTags map[string]string) error {
	if receipt.State == StateReleased {
		if len(receipt.Resources) != 0 || len(receipt.AccessGrants) != 0 {
			return ErrProviderInvariant
		}
		return nil
	}
	if receipt.State != StateActive && receipt.State != StateAcquiring && receipt.State != StateReleasePending {
		return ErrProviderInvariant
	}
	if len(receipt.Resources) == 0 {
		return ErrProviderInvariant
	}
	seen := make(map[string]struct{}, len(receipt.Resources))
	roles := make(map[string]struct{}, len(receipt.Resources))
	for _, resource := range receipt.Resources {
		if resource.ID == "" || resource.Kind == "" || resource.Role == "" {
			return ErrProviderInvariant
		}
		if _, exists := seen[resource.ID]; exists {
			return ErrProviderInvariant
		}
		seen[resource.ID] = struct{}{}
		for key, value := range expectedBaseTags {
			if resource.Tags[key] != value {
				return ErrProviderInvariant
			}
		}
		if resource.Tags[TagResourceRole] != resource.Role {
			return ErrProviderInvariant
		}
		roles[resource.Role] = struct{}{}
	}
	grantIDs := make(map[string]struct{}, len(receipt.AccessGrants))
	for _, grant := range receipt.AccessGrants {
		if err := validateStoredAccessGrant(grant, receipt.ExpiresAt, roles); err != nil {
			return err
		}
		if _, exists := grantIDs[grant.ID]; exists {
			return ErrProviderInvariant
		}
		grantIDs[grant.ID] = struct{}{}
	}
	return nil
}

func selectorFor(plan Plan, digest string) Selector {
	return Selector{
		LeaseID: plan.LeaseID, RequestID: plan.RequestID, Provider: plan.Provider,
		Region: plan.Region, Repository: plan.Repository, PlanDigest: digest,
	}
}

func selectorFromReceipt(receipt Receipt) Selector {
	return Selector{
		LeaseID: receipt.LeaseID, RequestID: receipt.RequestID, Provider: receipt.Provider,
		Region: receipt.Region, Repository: receipt.Repository, PlanDigest: receipt.PlanDigest,
	}
}

func baseTags(plan Plan, digest string, createdAt time.Time) map[string]string {
	tags := maps.Clone(plan.Tags)
	if tags == nil {
		tags = make(map[string]string, len(MandatoryBaseTagKeys()))
	}
	tags[TagManagedBy] = ManagedByValue
	tags[TagLeaseID] = plan.LeaseID
	tags[TagRequestID] = plan.RequestID
	tags[TagRepository] = plan.Repository
	tags[TagOperator] = plan.Operator
	tags[TagProvider] = plan.Provider
	tags[TagRegion] = plan.Region
	tags[TagPlanDigest] = digest
	tags[TagCreatedAt] = createdAt.UTC().Format(time.RFC3339Nano)
	tags[TagExpiresAt] = plan.ExpiresAt.UTC().Format(time.RFC3339Nano)
	addProvenanceTags(tags, plan.Provenance)
	return tags
}

func normalizeAndValidatePlan(plan Plan, now time.Time) (Plan, string, error) {
	plan = clonePlan(plan)
	plan.Schema = strings.TrimSpace(plan.Schema)
	plan.LeaseID = strings.TrimSpace(plan.LeaseID)
	plan.RequestID = strings.TrimSpace(plan.RequestID)
	plan.Provider = strings.TrimSpace(plan.Provider)
	plan.Region = strings.TrimSpace(plan.Region)
	plan.Repository = strings.TrimSpace(plan.Repository)
	plan.Operator = strings.TrimSpace(plan.Operator)
	plan.Budget.Currency = strings.TrimSpace(plan.Budget.Currency)
	plan.Provenance.SourceSHA = strings.TrimSpace(plan.Provenance.SourceSHA)
	plan.Provenance.BundleDigest = strings.TrimSpace(plan.Provenance.BundleDigest)
	plan.ExpiresAt = plan.ExpiresAt.UTC()
	if plan.Schema != PlanSchemaV1 || !validIdentity(plan.LeaseID) || !validIdentity(plan.RequestID) ||
		plan.Provider == "" || plan.Region == "" || plan.Repository == "" || plan.Operator == "" ||
		!plan.ExpiresAt.After(now) || !validCurrency(plan.Budget.Currency) || plan.Budget.LimitMicros <= 0 ||
		plan.Budget.CommittedMicros < 0 || plan.Budget.CommittedMicros >= plan.Budget.LimitMicros ||
		len(plan.HostGroups) == 0 || plan.Network.ConservativePublicEgressBytes < 0 ||
		validateProvenance(plan.Provenance) != nil {
		return Plan{}, "", ErrInvalidPlan
	}
	for key, value := range plan.Tags {
		if key == "" || key != strings.TrimSpace(key) || value == "" ||
			value != strings.TrimSpace(value) || isReservedTag(key) {
			return Plan{}, "", ErrInvalidPlan
		}
	}
	roles := make(map[string]struct{}, len(plan.HostGroups))
	hasPublicIPv4 := false
	for index := range plan.HostGroups {
		group := &plan.HostGroups[index]
		group.Role = strings.TrimSpace(group.Role)
		group.Compute.Architecture = strings.TrimSpace(group.Compute.Architecture)
		group.Compute.BillingModel = strings.TrimSpace(group.Compute.BillingModel)
		if !validIdentity(group.Role) || group.Count <= 0 || group.Compute.VCPUs <= 0 ||
			group.Compute.MemoryBytes <= 0 || group.Compute.Architecture == "" ||
			group.Compute.BillingModel == "" || group.PeakBandwidthMbps < 0 ||
			(!group.PublicIPv4 && group.PeakBandwidthMbps != 0) {
			return Plan{}, "", ErrInvalidPlan
		}
		if _, exists := roles[group.Role]; exists {
			return Plan{}, "", ErrInvalidPlan
		}
		hasPublicIPv4 = hasPublicIPv4 || group.PublicIPv4
		roles[group.Role] = struct{}{}
		if err := normalizeDisk(&group.SystemDisk); err != nil {
			return Plan{}, "", err
		}
		if group.SystemDisk.CountPerHost != 1 {
			return Plan{}, "", ErrInvalidPlan
		}
		diskRoles := map[string]struct{}{group.SystemDisk.Role: {}}
		for diskIndex := range group.DataDisks {
			disk := &group.DataDisks[diskIndex]
			if err := normalizeDisk(disk); err != nil {
				return Plan{}, "", err
			}
			if _, exists := diskRoles[disk.Role]; exists {
				return Plan{}, "", ErrInvalidPlan
			}
			diskRoles[disk.Role] = struct{}{}
		}
	}
	if plan.Network.ConservativePublicEgressBytes > 0 && !hasPublicIPv4 {
		return Plan{}, "", ErrInvalidPlan
	}
	grantIDs := make(map[string]struct{}, len(plan.Network.InitialAccess))
	for index := range plan.Network.InitialAccess {
		grant := &plan.Network.InitialAccess[index]
		grant.ID = strings.TrimSpace(grant.ID)
		grant.TargetRole = strings.TrimSpace(grant.TargetRole)
		grant.Until = grant.Until.UTC()
		if err := validateAccessGrant(*grant, now, plan.ExpiresAt, roles); err != nil {
			return Plan{}, "", err
		}
		if _, exists := grantIDs[grant.ID]; exists {
			return Plan{}, "", ErrInvalidPlan
		}
		grantIDs[grant.ID] = struct{}{}
	}
	data, err := json.Marshal(plan)
	if err != nil {
		return Plan{}, "", fmt.Errorf("%w: encode: %v", ErrInvalidPlan, err)
	}
	sum := sha256.Sum256(data)
	return plan, hex.EncodeToString(sum[:]), nil
}

func normalizeDisk(disk *DiskPlan) error {
	disk.Role = strings.TrimSpace(disk.Role)
	disk.Class = strings.TrimSpace(disk.Class)
	disk.PerformanceLevel = strings.TrimSpace(disk.PerformanceLevel)
	if disk.CountPerHost == 0 {
		disk.CountPerHost = 1
	}
	if !validIdentity(disk.Role) || disk.CountPerHost <= 0 || disk.SizeBytes <= 0 || disk.Class == "" {
		return ErrInvalidPlan
	}
	return nil
}

func validateQuote(plan Plan, digest string, quote Quote, now time.Time) error {
	quote.LeaseID = strings.TrimSpace(quote.LeaseID)
	quote.RequestID = strings.TrimSpace(quote.RequestID)
	quote.Provider = strings.TrimSpace(quote.Provider)
	quote.Region = strings.TrimSpace(quote.Region)
	quote.Zone = strings.TrimSpace(quote.Zone)
	quote.Currency = strings.TrimSpace(quote.Currency)
	if quote.LeaseID != plan.LeaseID || quote.RequestID != plan.RequestID ||
		quote.Provider != plan.Provider || quote.Region != plan.Region ||
		quote.Zone == "" || quote.PlanDigest != digest || quote.Currency != plan.Budget.Currency ||
		quote.EstimatedCostMicros <= 0 || quote.QuotedAt.IsZero() ||
		!quote.ValidUntil.After(now) || quote.ValidUntil.Before(quote.QuotedAt) {
		return ErrInvalidQuote
	}
	if !quote.CapacityAvailable {
		return ErrCapacityUnavailable
	}
	if !quote.QuotaAvailable {
		return ErrQuotaUnavailable
	}
	if quote.EstimatedCostMicros > plan.Budget.LimitMicros-plan.Budget.CommittedMicros {
		return fmt.Errorf("%w: committed=%d estimate=%d limit=%d",
			ErrCostLimitExceeded, plan.Budget.CommittedMicros, quote.EstimatedCostMicros, plan.Budget.LimitMicros)
	}
	return nil
}

func validateAccessGrant(grant AccessGrant, now, expiresAt time.Time, roles map[string]struct{}) error {
	if !validIdentity(grant.ID) || !validIdentity(grant.TargetRole) ||
		(grant.Protocol != ProtocolTCP && grant.Protocol != ProtocolUDP) ||
		grant.PortFrom == 0 || grant.PortTo < grant.PortFrom ||
		!grant.SourcePrefix.IsValid() || grant.SourcePrefix != grant.SourcePrefix.Masked() ||
		!grant.Until.After(now) || grant.Until.After(expiresAt) {
		return ErrInvalidAccess
	}
	if _, exists := roles[grant.TargetRole]; !exists {
		return ErrInvalidAccess
	}
	return nil
}

func validateStoredAccessGrant(grant AccessGrant, expiresAt time.Time, roles map[string]struct{}) error {
	if !validIdentity(grant.ID) || !validIdentity(grant.TargetRole) ||
		(grant.Protocol != ProtocolTCP && grant.Protocol != ProtocolUDP) ||
		grant.PortFrom == 0 || grant.PortTo < grant.PortFrom ||
		!grant.SourcePrefix.IsValid() || grant.SourcePrefix != grant.SourcePrefix.Masked() ||
		grant.Until.IsZero() || grant.Until.After(expiresAt) {
		return ErrProviderInvariant
	}
	if _, exists := roles[grant.TargetRole]; !exists {
		return ErrProviderInvariant
	}
	return nil
}

func quotesMatchAdmitted(left, right Quote) bool {
	return left.LeaseID == right.LeaseID && left.RequestID == right.RequestID &&
		left.Provider == right.Provider && left.Region == right.Region && left.Zone == right.Zone &&
		left.AccountIDHash == right.AccountIDHash && left.PlanDigest == right.PlanDigest &&
		left.Currency == right.Currency && left.EstimatedCostMicros == right.EstimatedCostMicros &&
		left.CapacityAvailable == right.CapacityAvailable && left.QuotaAvailable == right.QuotaAvailable &&
		left.QuotedAt.Equal(right.QuotedAt) && left.ValidUntil.Equal(right.ValidUntil) &&
		slices.Equal(left.LineItems, right.LineItems) && maps.Equal(left.Selection, right.Selection)
}

func validIdentity(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for index, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || (index > 0 && strings.ContainsRune("._:-", char)) {
			continue
		}
		return false
	}
	return true
}

func validCurrency(value string) bool {
	if len(value) != 3 {
		return false
	}
	for _, char := range value {
		if char < 'A' || char > 'Z' {
			return false
		}
	}
	return true
}

func validateProvenance(provenance Provenance) error {
	if provenance.SourceSHA != "" && !validLowerHexDigest(provenance.SourceSHA, 40, 64) {
		return ErrInvalidPlan
	}
	if provenance.BundleDigest != "" && !validBundleDigest(provenance.BundleDigest) {
		return ErrInvalidPlan
	}
	return nil
}

func validBundleDigest(value string) bool {
	if strings.HasPrefix(value, "sha256:") {
		value = strings.TrimPrefix(value, "sha256:")
	}
	return validLowerHexDigest(value, 64)
}

func validLowerHexDigest(value string, lengths ...int) bool {
	if !slices.Contains(lengths, len(value)) {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func addProvenanceTags(tags map[string]string, provenance Provenance) {
	if provenance.SourceSHA != "" {
		tags[TagSourceSHA] = provenance.SourceSHA
	}
	if provenance.BundleDigest != "" {
		tags[TagBundleDigest] = provenance.BundleDigest
	}
}

func isReservedTag(key string) bool {
	return slices.Contains(MandatoryResourceTagKeys(), key) ||
		key == TagSourceSHA || key == TagBundleDigest || strings.HasPrefix(key, "wukongim-")
}

func clonePlan(plan Plan) Plan {
	plan.Tags = maps.Clone(plan.Tags)
	plan.HostGroups = slices.Clone(plan.HostGroups)
	for index := range plan.HostGroups {
		plan.HostGroups[index].DataDisks = slices.Clone(plan.HostGroups[index].DataDisks)
	}
	plan.Network.InitialAccess = slices.Clone(plan.Network.InitialAccess)
	return plan
}

func cloneQuote(quote Quote) Quote {
	quote.LineItems = slices.Clone(quote.LineItems)
	quote.Selection = maps.Clone(quote.Selection)
	return quote
}

func cloneReceipt(receipt Receipt) Receipt {
	receipt.Quote = cloneQuote(receipt.Quote)
	receipt.Tags = maps.Clone(receipt.Tags)
	receipt.Resources = slices.Clone(receipt.Resources)
	for index := range receipt.Resources {
		receipt.Resources[index].Tags = maps.Clone(receipt.Resources[index].Tags)
		receipt.Resources[index].Attributes = maps.Clone(receipt.Resources[index].Attributes)
	}
	receipt.AccessGrants = slices.Clone(receipt.AccessGrants)
	return receipt
}

func cloneReleaseResult(result ReleaseResult) ReleaseResult {
	if result.Receipt != nil {
		receipt := cloneReceipt(*result.Receipt)
		result.Receipt = &receipt
	}
	if result.ZeroInventory != nil {
		proof := *result.ZeroInventory
		proof.Scopes = slices.Clone(proof.Scopes)
		result.ZeroInventory = &proof
	}
	return result
}
