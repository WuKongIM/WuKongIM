// Package fake implements an in-memory Cloud Lease Provider for contract,
// lifecycle, and cleanup tests.
package fake

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudlease"
)

const (
	// ProviderName is the stable adapter identifier used by fake Lease Plans.
	ProviderName = "fake"
	fakeZone     = "fake-zone-a"
	fakeAccount  = "fake-account"
)

var (
	// ErrInjectedFailure reports an intentional deterministic adapter failure.
	ErrInjectedFailure = errors.New("internal/infra/cloudlease/fake: injected failure")
	// ErrInvalidRequest reports input that violates the Provider port contract.
	ErrInvalidRequest = errors.New("internal/infra/cloudlease/fake: invalid request")
)

// FailurePlan configures deterministic failures without changing the public
// Cloud Lease contract.
type FailurePlan struct {
	// Quote and List fail their corresponding read operations.
	Quote bool
	List  bool
	// AcquireAfterResources retains partial cleanup-pending inventory.
	AcquireAfterResources int
	// AcquireAmbiguous persists a complete Lease and then returns an error.
	AcquireAmbiguous bool
	// ReleaseResidualAttempts retains resources for this many Release calls.
	ReleaseResidualAttempts map[string]int
	// ReleaseAmbiguous clears inventory and then returns an error.
	ReleaseAmbiguous bool
	// Access fails GrantAccess and RevokeAccess.
	Access bool
}

// Options configures one isolated fake Provider.
type Options struct {
	// Now supplies deterministic UTC timestamps.
	Now func() time.Time
	// EstimatedCostMicros is the complete default Quote cost.
	EstimatedCostMicros int64
	// CapacityUnavailable and QuotaUnavailable produce negative admissions.
	CapacityUnavailable bool
	QuotaUnavailable    bool
	// Failures configures deterministic mutation and reconciliation failures.
	Failures FailurePlan
}

// Provider is a concurrency-safe, in-memory Cloud Lease adapter.
type Provider struct {
	// mu protects leases and mutable failure counters for concurrent contract tests.
	mu sync.RWMutex
	// now supplies deterministic UTC timestamps without starting timers.
	now func() time.Time
	// estimatedCost is the complete deterministic Quote cost in micros.
	estimatedCost int64
	// capacity and quota are the configured read-only admission signals.
	capacity bool
	quota    bool
	// failures contains deterministic faults and lock-protected remaining attempts.
	failures FailurePlan
	// leases retains only live or cleanup-pending inventory; release deletes entries.
	leases map[string]cloudlease.Receipt
	// released retains provider idempotency identities, never resource inventory.
	released map[string]cloudlease.Selector
}

var _ cloudlease.Provider = (*Provider)(nil)

// New creates an empty fake Provider.
func New(options Options) *Provider {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	estimated := options.EstimatedCostMicros
	if estimated <= 0 {
		estimated = 4_000_000
	}
	failures := options.Failures
	failures.ReleaseResidualAttempts = maps.Clone(failures.ReleaseResidualAttempts)
	return &Provider{
		now: now, estimatedCost: estimated,
		capacity: !options.CapacityUnavailable, quota: !options.QuotaUnavailable,
		failures: failures, leases: make(map[string]cloudlease.Receipt),
		released: make(map[string]cloudlease.Selector),
	}
}

// Name returns the stable fake provider name.
func (*Provider) Name() string { return ProviderName }

// Quote returns a deterministic side-effect-free decision.
func (p *Provider) Quote(_ context.Context, request cloudlease.QuoteRequest) (cloudlease.Quote, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.failures.Quote {
		return cloudlease.Quote{}, ErrInjectedFailure
	}
	now := p.now().UTC()
	return cloudlease.Quote{
		LeaseID: request.Plan.LeaseID, RequestID: request.Plan.RequestID,
		Provider: ProviderName, Region: request.Plan.Region, Zone: fakeZone,
		AccountIDHash: fakeAccount, PlanDigest: request.PlanDigest,
		Currency: request.Plan.Budget.Currency, EstimatedCostMicros: p.estimatedCost,
		CapacityAvailable: p.capacity, QuotaAvailable: p.quota,
		QuotedAt: now, ValidUntil: now.Add(15 * time.Minute),
		LineItems: []cloudlease.QuoteLineItem{{
			Kind: "lease", Role: "all", Quantity: 1, CostMicros: p.estimatedCost,
		}},
		Selection: map[string]string{"zone": fakeZone, "sku": "fake.exact"},
	}, nil
}

// Acquire creates deterministic inventory or returns the exact existing Lease.
func (p *Provider) Acquire(_ context.Context, request cloudlease.AcquireRequest) (cloudlease.Receipt, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := validateAcquireRequest(request); err != nil {
		return cloudlease.Receipt{}, err
	}
	if existing, ok := p.leases[request.Plan.LeaseID]; ok {
		if !matchesAcquire(existing, request) {
			return cloudlease.Receipt{}, cloudlease.ErrLeaseConflict
		}
		if existing.State == cloudlease.StateReleased {
			return cloneReceipt(existing), cloudlease.ErrLeaseReleased
		}
		return cloneReceipt(existing), nil
	}
	if released, ok := p.released[request.Plan.LeaseID]; ok {
		selector := cloudlease.Selector{
			LeaseID: request.Plan.LeaseID, RequestID: request.Plan.RequestID,
			Provider: request.Plan.Provider, Region: request.Plan.Region,
			Repository: request.Plan.Repository, PlanDigest: request.PlanDigest,
		}
		if released != selector {
			return cloudlease.Receipt{}, cloudlease.ErrLeaseConflict
		}
		return cloudlease.Receipt{}, cloudlease.ErrLeaseReleased
	}

	resources := fakeResources(request)
	receipt := cloudlease.Receipt{
		LeaseID: request.Plan.LeaseID, RequestID: request.Plan.RequestID,
		Provider: ProviderName, Region: request.Plan.Region, Zone: request.Quote.Zone,
		AccountIDHash: fakeAccount, Repository: request.Plan.Repository, Operator: request.Plan.Operator,
		PlanDigest: request.PlanDigest, Provenance: request.Plan.Provenance, State: cloudlease.StateActive,
		CreatedAt: request.RequestedAt.UTC(), ExpiresAt: request.Plan.ExpiresAt.UTC(),
		Quote: cloneQuote(request.Quote), Budget: request.Plan.Budget, Tags: maps.Clone(request.BaseTags),
		Resources: resources, AccessGrants: slices.Clone(request.Plan.Network.InitialAccess),
	}
	sortAccess(receipt.AccessGrants)
	if after := p.failures.AcquireAfterResources; after > 0 && after < len(resources) {
		receipt.State = cloudlease.StateReleasePending
		receipt.Resources = cloneResources(resources[:after])
		p.leases[receipt.LeaseID] = receipt
		return cloneReceipt(receipt), ErrInjectedFailure
	}
	p.leases[receipt.LeaseID] = receipt
	if p.failures.AcquireAmbiguous {
		return cloneReceipt(receipt), ErrInjectedFailure
	}
	return cloneReceipt(receipt), nil
}

// Inspect returns one exact live or cleanup-pending Lease inventory.
func (p *Provider) Inspect(_ context.Context, selector cloudlease.Selector) (cloudlease.Receipt, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	receipt, ok := p.leases[selector.LeaseID]
	if !ok {
		return cloudlease.Receipt{}, cloudlease.ErrLeaseNotFound
	}
	if !matchesSelector(receipt, selector) {
		return cloudlease.Receipt{}, cloudlease.ErrLeaseConflict
	}
	return cloneReceipt(receipt), nil
}

// List returns deterministic repository-scoped live and cleanup-pending inventory.
func (p *Provider) List(_ context.Context, filter cloudlease.InventoryFilter) ([]cloudlease.Receipt, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.failures.List {
		return nil, ErrInjectedFailure
	}
	ids := make([]string, 0, len(p.leases))
	for id, receipt := range p.leases {
		if receipt.Repository == filter.Repository {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	receipts := make([]cloudlease.Receipt, 0, len(ids))
	for _, id := range ids {
		receipts = append(receipts, cloneReceipt(p.leases[id]))
	}
	return receipts, nil
}

// GrantAccess adds one exact idempotent ingress grant.
func (p *Provider) GrantAccess(_ context.Context, selector cloudlease.Selector, grant cloudlease.AccessGrant) (cloudlease.Receipt, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failures.Access {
		return cloudlease.Receipt{}, ErrInjectedFailure
	}
	receipt, err := p.lookupLocked(selector)
	if err != nil {
		return cloudlease.Receipt{}, err
	}
	if receipt.State != cloudlease.StateActive {
		return cloudlease.Receipt{}, cloudlease.ErrInvalidAccess
	}
	for _, existing := range receipt.AccessGrants {
		if existing.ID != grant.ID {
			continue
		}
		if existing == grant {
			return cloneReceipt(receipt), nil
		}
		return cloudlease.Receipt{}, cloudlease.ErrInvalidAccess
	}
	receipt.AccessGrants = append(receipt.AccessGrants, grant)
	sortAccess(receipt.AccessGrants)
	p.leases[receipt.LeaseID] = receipt
	return cloneReceipt(receipt), nil
}

// RevokeAccess removes one exact grant and is idempotent when it is absent.
func (p *Provider) RevokeAccess(_ context.Context, selector cloudlease.Selector, grantID string) (cloudlease.Receipt, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failures.Access {
		return cloudlease.Receipt{}, ErrInjectedFailure
	}
	receipt, err := p.lookupLocked(selector)
	if err != nil {
		return cloudlease.Receipt{}, err
	}
	filtered := make([]cloudlease.AccessGrant, 0, len(receipt.AccessGrants))
	for _, grant := range receipt.AccessGrants {
		if grant.ID != grantID {
			filtered = append(filtered, grant)
		}
	}
	receipt.AccessGrants = filtered
	p.leases[receipt.LeaseID] = receipt
	return cloneReceipt(receipt), nil
}

// Release clears all resources and access or returns deterministic residual inventory.
func (p *Provider) Release(_ context.Context, selector cloudlease.Selector) (cloudlease.ReleaseResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	receipt, exists := p.leases[selector.LeaseID]
	if !exists {
		return p.zeroInventoryProof(selector), nil
	}
	if !matchesSelector(receipt, selector) {
		return cloudlease.ReleaseResult{}, cloudlease.ErrLeaseConflict
	}
	if remaining := p.failures.ReleaseResidualAttempts[receipt.LeaseID]; remaining > 0 {
		if removeOneReleaseDependency(&receipt) {
			p.failures.ReleaseResidualAttempts[receipt.LeaseID] = remaining - 1
			receipt.State = cloudlease.StateReleasePending
			p.leases[receipt.LeaseID] = receipt
			cloned := cloneReceipt(receipt)
			return cloudlease.ReleaseResult{Receipt: &cloned}, nil
		}
	}
	delete(p.leases, receipt.LeaseID)
	p.released[receipt.LeaseID] = selector
	result := p.zeroInventoryProof(selector)
	if p.failures.ReleaseAmbiguous {
		return result, ErrInjectedFailure
	}
	return result, nil
}

func (p *Provider) zeroInventoryProof(selector cloudlease.Selector) cloudlease.ReleaseResult {
	return cloudlease.ReleaseResult{ZeroInventory: &cloudlease.ZeroInventoryProof{
		Selector: selector, AccountIDHash: fakeAccount, ObservedAt: p.now().UTC(),
		Scopes: completeReleaseScopes(),
	}}
}

func completeReleaseScopes() []string {
	return []string{"access", "compute", "disk", "network", "public-address"}
}

func removeOneReleaseDependency(receipt *cloudlease.Receipt) bool {
	if len(receipt.AccessGrants) > 0 {
		receipt.AccessGrants = []cloudlease.AccessGrant{}
		return true
	}
	if len(receipt.Resources) <= 1 {
		return false
	}
	receipt.Resources = cloneResources(receipt.Resources[:len(receipt.Resources)-1])
	return true
}

func (p *Provider) lookupLocked(selector cloudlease.Selector) (cloudlease.Receipt, error) {
	receipt, ok := p.leases[selector.LeaseID]
	if !ok {
		return cloudlease.Receipt{}, cloudlease.ErrLeaseNotFound
	}
	if !matchesSelector(receipt, selector) {
		return cloudlease.Receipt{}, cloudlease.ErrLeaseConflict
	}
	return receipt, nil
}

func validateAcquireRequest(request cloudlease.AcquireRequest) error {
	if request.Plan.Provider != ProviderName || request.PlanDigest == "" || request.RequestedAt.IsZero() ||
		request.RequestedAt.After(request.Plan.ExpiresAt) ||
		request.Quote.PlanDigest != request.PlanDigest || request.Quote.Provider != ProviderName {
		return ErrInvalidRequest
	}
	for _, key := range cloudlease.MandatoryBaseTagKeys() {
		if strings.TrimSpace(request.BaseTags[key]) == "" {
			return fmt.Errorf("%w: missing %s", ErrInvalidRequest, key)
		}
	}
	return nil
}

func matchesAcquire(receipt cloudlease.Receipt, request cloudlease.AcquireRequest) bool {
	return receipt.LeaseID == request.Plan.LeaseID && receipt.RequestID == request.Plan.RequestID &&
		receipt.Provider == request.Plan.Provider && receipt.Region == request.Plan.Region &&
		receipt.Repository == request.Plan.Repository && receipt.Operator == request.Plan.Operator &&
		receipt.PlanDigest == request.PlanDigest && receipt.ExpiresAt.Equal(request.Plan.ExpiresAt)
}

func matchesSelector(receipt cloudlease.Receipt, selector cloudlease.Selector) bool {
	return receipt.LeaseID == selector.LeaseID && receipt.RequestID == selector.RequestID &&
		receipt.Provider == selector.Provider && receipt.Region == selector.Region &&
		receipt.Repository == selector.Repository && receipt.PlanDigest == selector.PlanDigest
}

func fakeResources(request cloudlease.AcquireRequest) []cloudlease.Resource {
	// Parents precede children so reverse removal follows provider dependencies.
	resources := make([]cloudlease.Resource, 0)
	if request.Plan.Network.Isolated {
		resources = append(resources, newResource(request.BaseTags, "network", "lease-network", "network", false, 0, ""))
	}
	hostIndex := 0
	for _, group := range request.Plan.HostGroups {
		for ordinal := 1; ordinal <= group.Count; ordinal++ {
			hostIndex++
			hostID := fmt.Sprintf("compute-%s-%d", group.Role, ordinal)
			privateAddress := fmt.Sprintf("10.0.0.%d", hostIndex+9)
			resources = append(resources, newResource(request.BaseTags, hostID, group.Role, "compute", true, 0, privateAddress))
			resources = append(resources, newDiskResources(request.BaseTags, hostID, group.Role, group.SystemDisk)...)
			for _, disk := range group.DataDisks {
				resources = append(resources, newDiskResources(request.BaseTags, hostID, group.Role, disk)...)
			}
			if group.PublicIPv4 {
				address := newResource(request.BaseTags, "address-"+hostID, group.Role, "public-address", true, 0, "")
				address.PublicAddress = fmt.Sprintf("203.0.113.%d", hostIndex)
				address.ParentID = hostID
				address.Attributes = map[string]string{"peak_bandwidth_mbps": fmt.Sprint(group.PeakBandwidthMbps)}
				resources = append(resources, address)
			}
		}
	}
	return resources
}

func newDiskResources(baseTags map[string]string, hostID, role string, plan cloudlease.DiskPlan) []cloudlease.Resource {
	count := plan.CountPerHost
	if count == 0 {
		count = 1
	}
	resources := make([]cloudlease.Resource, 0, count)
	for ordinal := 1; ordinal <= count; ordinal++ {
		resource := newResource(baseTags, fmt.Sprintf("disk-%s-%s-%d", hostID, plan.Role, ordinal), role, "disk", true, plan.SizeBytes, "")
		resource.ParentID = hostID
		resource.Attributes = map[string]string{"disk_role": plan.Role, "class": plan.Class}
		if plan.PerformanceLevel != "" {
			resource.Attributes["performance_level"] = plan.PerformanceLevel
		}
		resources = append(resources, resource)
	}
	return resources
}

func newResource(baseTags map[string]string, id, role, kind string, billable bool, sizeBytes int64, privateAddress string) cloudlease.Resource {
	tags := maps.Clone(baseTags)
	tags[cloudlease.TagResourceRole] = role
	return cloudlease.Resource{
		ID: id, Kind: kind, Role: role, Billable: billable, SizeBytes: sizeBytes,
		PrivateAddress: privateAddress, Tags: tags,
	}
}

func sortAccess(grants []cloudlease.AccessGrant) {
	slices.SortFunc(grants, func(left, right cloudlease.AccessGrant) int {
		return strings.Compare(left.ID, right.ID)
	})
}

func cloneQuote(quote cloudlease.Quote) cloudlease.Quote {
	quote.LineItems = slices.Clone(quote.LineItems)
	quote.Selection = maps.Clone(quote.Selection)
	return quote
}

func cloneResources(resources []cloudlease.Resource) []cloudlease.Resource {
	cloned := slices.Clone(resources)
	for index := range cloned {
		cloned[index].Tags = maps.Clone(cloned[index].Tags)
		cloned[index].Attributes = maps.Clone(cloned[index].Attributes)
	}
	return cloned
}

func cloneReceipt(receipt cloudlease.Receipt) cloudlease.Receipt {
	receipt.Quote = cloneQuote(receipt.Quote)
	receipt.Tags = maps.Clone(receipt.Tags)
	receipt.Resources = cloneResources(receipt.Resources)
	receipt.AccessGrants = slices.Clone(receipt.AccessGrants)
	return receipt
}
