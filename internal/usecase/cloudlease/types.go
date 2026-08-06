// Package cloudlease owns provider-neutral orchestration for temporary cloud
// infrastructure. Product deployment and workload concepts stay outside this
// package.
package cloudlease

import (
	"context"
	"errors"
	"net/netip"
	"time"
)

const (
	// PlanSchemaV1 is the first strict Cloud Lease Plan schema.
	PlanSchemaV1 = "wukongim.cloud_lease/v1"
	// ManagedByValue is the stable ownership tag for Cloud Lease resources.
	ManagedByValue = "wukongim-cloud-lease"
)

const (
	// TagManagedBy identifies resources owned by the Cloud Lease control plane.
	TagManagedBy = "wukongim-managed-by"
	// TagLeaseID identifies one exact Cloud Lease.
	TagLeaseID = "wukongim-lease-id"
	// TagRequestID links sequential Leases to one operator request.
	TagRequestID = "wukongim-request-id"
	// TagRepository records the trusted repository identity.
	TagRepository = "wukongim-repository"
	// TagOperator records the operator identity that authorized the Lease.
	TagOperator = "wukongim-operator"
	// TagProvider records the Cloud Provider Adapter identity.
	TagProvider = "wukongim-provider"
	// TagRegion records the provider inventory boundary.
	TagRegion = "wukongim-region"
	// TagPlanDigest identifies the immutable normalized Lease Plan.
	TagPlanDigest = "wukongim-lease-plan-digest"
	// TagSourceSHA records an immutable source revision when applicable.
	TagSourceSHA = "wukongim-source-sha"
	// TagBundleDigest records an immutable deployment bundle when applicable.
	TagBundleDigest = "wukongim-bundle-digest"
	// TagCreatedAt records the Lease creation request in RFC3339Nano form.
	TagCreatedAt = "wukongim-created-at"
	// TagExpiresAt records immutable Lease expiry in RFC3339Nano form.
	TagExpiresAt = "wukongim-expires-at"
	// TagResourceRole records the logical role of one provider resource.
	TagResourceRole = "wukongim-resource-role"
)

var (
	// ErrInvalidPlan reports a malformed or unsafe Lease Plan.
	ErrInvalidPlan = errors.New("internal/usecase/cloudlease: invalid plan")
	// ErrInvalidQuote reports a quote that does not bind to the requested Plan.
	ErrInvalidQuote = errors.New("internal/usecase/cloudlease: invalid quote")
	// ErrCostLimitExceeded reports that committed plus estimated cost exceeds the Budget.
	ErrCostLimitExceeded = errors.New("internal/usecase/cloudlease: cost limit exceeded")
	// ErrCapacityUnavailable reports insufficient provider capacity.
	ErrCapacityUnavailable = errors.New("internal/usecase/cloudlease: capacity unavailable")
	// ErrQuotaUnavailable reports insufficient provider quota.
	ErrQuotaUnavailable = errors.New("internal/usecase/cloudlease: quota unavailable")
	// ErrLeaseNotFound reports that provider inventory has no exact matching Lease.
	ErrLeaseNotFound = errors.New("internal/usecase/cloudlease: lease not found")
	// ErrLeaseConflict reports an identity collision with a different immutable Plan.
	ErrLeaseConflict = errors.New("internal/usecase/cloudlease: lease conflict")
	// ErrLeaseReleased reports an attempt to reacquire an already released Lease identity.
	ErrLeaseReleased = errors.New("internal/usecase/cloudlease: lease released")
	// ErrAcquireIncomplete reports matching partial inventory after an ambiguous acquisition.
	ErrAcquireIncomplete = errors.New("internal/usecase/cloudlease: acquire incomplete")
	// ErrInvalidAccess reports an invalid or conflicting access grant.
	ErrInvalidAccess = errors.New("internal/usecase/cloudlease: invalid access")
	// ErrResidualResources reports that Release has not yet proved empty inventory.
	ErrResidualResources = errors.New("internal/usecase/cloudlease: residual resources")
	// ErrProviderInvariant reports provider output that violates the Cloud Lease contract.
	ErrProviderInvariant = errors.New("internal/usecase/cloudlease: provider invariant")
)

// Budget carries the aggregate authorization already consumed before this
// Lease and the hard ceiling shared with its caller.
type Budget struct {
	// Currency is the required quote currency.
	Currency string `json:"currency"`
	// LimitMicros is the complete caller-authorized cost in millionths of Currency.
	LimitMicros int64 `json:"limit_micros"`
	// CommittedMicros is cost already reserved or accrued before this Lease.
	CommittedMicros int64 `json:"committed_micros"`
}

// Provenance carries optional, provider-neutral immutable input identities.
type Provenance struct {
	// SourceSHA is a lowercase hexadecimal Git object identity when applicable.
	SourceSHA string `json:"source_sha,omitempty"`
	// BundleDigest is a lowercase SHA-256 deployment bundle digest when applicable.
	BundleDigest string `json:"bundle_digest,omitempty"`
}

// ComputePlan declares provider-neutral minimum compute for one host.
type ComputePlan struct {
	// VCPUs is the exact or minimum virtual CPU count interpreted by the adapter.
	VCPUs int `json:"vcpus"`
	// MemoryBytes is the exact or minimum memory size interpreted by the adapter.
	MemoryBytes int64 `json:"memory_bytes"`
	// Architecture is the required machine architecture, such as x86_64.
	Architecture string `json:"architecture"`
	// BillingModel is the required provider-neutral billing model.
	BillingModel string `json:"billing_model"`
	// AllowBurstable permits burst-credit-backed instance families when true.
	AllowBurstable bool `json:"allow_burstable"`
}

// DiskPlan declares one disk attached to every host in a HostGroupPlan.
type DiskPlan struct {
	// Role distinguishes system and workload-specific data disks.
	Role string `json:"role"`
	// CountPerHost is the number of identical disks per host. Zero means one.
	CountPerHost int `json:"count_per_host,omitempty"`
	// SizeBytes is the minimum provider-advertised disk capacity.
	SizeBytes int64 `json:"size_bytes"`
	// Class is the provider-neutral storage class.
	Class string `json:"class"`
	// PerformanceLevel is an optional provider-neutral performance tier.
	PerformanceLevel string `json:"performance_level,omitempty"`
}

// HostGroupPlan declares interchangeable hosts with one logical role.
type HostGroupPlan struct {
	// Role is the stable resource role used in receipts and tags.
	Role string `json:"role"`
	// Count is the number of identical hosts in this group.
	Count int `json:"count"`
	// Compute describes the required host capacity.
	Compute ComputePlan `json:"compute"`
	// SystemDisk is the boot disk attached to each host.
	SystemDisk DiskPlan `json:"system_disk"`
	// DataDisks are independent non-boot disks attached to each host.
	DataDisks []DiskPlan `json:"data_disks,omitempty"`
	// PublicIPv4 requests one public IPv4 address for each host in this group.
	PublicIPv4 bool `json:"public_ipv4"`
	// InternetEgress permits provider-routed public egress for this group.
	InternetEgress bool `json:"internet_egress"`
	// PeakBandwidthMbps bounds public bandwidth when PublicIPv4 is true.
	PeakBandwidthMbps int `json:"peak_bandwidth_mbps,omitempty"`
}

// NetworkPlan declares the shared network shape of one Cloud Lease.
type NetworkPlan struct {
	// Isolated requests a Lease-owned network rather than a shared VPC.
	Isolated bool `json:"isolated"`
	// SingleZone requires every host to occupy one selected availability zone.
	SingleZone bool `json:"single_zone"`
	// InitialAccess contains reviewed ingress grants created with the Lease.
	InitialAccess []AccessGrant `json:"initial_access,omitempty"`
	// ConservativePublicEgressBytes is the quoted upper-bound public traffic
	// consumed during the complete Lease. Zero means no traffic-priced egress.
	ConservativePublicEgressBytes int64 `json:"conservative_public_egress_bytes,omitempty"`
}

// Plan is the immutable, provider-neutral request for one temporary Cloud Lease.
type Plan struct {
	// Schema selects the strict Lease Plan version.
	Schema string `json:"schema"`
	// LeaseID identifies this exact temporary infrastructure allocation.
	LeaseID string `json:"lease_id"`
	// RequestID groups sequential Leases under one operator request.
	RequestID string `json:"request_id"`
	// Provider selects the Cloud Provider Adapter.
	Provider string `json:"provider"`
	// Region is the provider region in which resources may be created.
	Region string `json:"region"`
	// Repository is the trusted owner/name identity.
	Repository string `json:"repository"`
	// Operator is the identity that authorized this Lease.
	Operator string `json:"operator"`
	// ExpiresAt is the immutable provider-side cleanup deadline.
	ExpiresAt time.Time `json:"expires_at"`
	// Budget is the aggregate caller authorization used for admission.
	Budget Budget `json:"budget"`
	// Provenance binds applicable source and deployment artifacts.
	Provenance Provenance `json:"provenance,omitempty"`
	// Network defines shared isolation and initial ingress.
	Network NetworkPlan `json:"network"`
	// HostGroups define generic compute and attached storage.
	HostGroups []HostGroupPlan `json:"host_groups"`
	// Tags are caller-owned non-secret tags in addition to mandatory tags.
	Tags map[string]string `json:"tags,omitempty"`
}

// QuoteLineItem is one auditable component of the provider estimate.
type QuoteLineItem struct {
	// Kind identifies the charged resource category.
	Kind string `json:"kind"`
	// Role identifies the Plan role receiving the resource.
	Role string `json:"role"`
	// Quantity is the number of identical charged resources.
	Quantity int `json:"quantity"`
	// CostMicros is the worst-case cost of this line item.
	CostMicros int64 `json:"cost_micros"`
}

// Quote is a bounded, immutable price and availability decision for one Plan.
type Quote struct {
	// LeaseID and RequestID bind this quote to the caller identity.
	LeaseID   string `json:"lease_id"`
	RequestID string `json:"request_id"`
	// Provider, Region, and Zone identify the selected inventory boundary.
	Provider string `json:"provider"`
	Region   string `json:"region"`
	Zone     string `json:"zone"`
	// AccountIDHash is a non-secret provider account binding.
	AccountIDHash string `json:"account_id_hash,omitempty"`
	// PlanDigest binds the quote to the normalized immutable Plan.
	PlanDigest string `json:"plan_digest"`
	// Currency and EstimatedCostMicros express the worst-case Lease estimate.
	Currency            string `json:"currency"`
	EstimatedCostMicros int64  `json:"estimated_cost_micros"`
	// CapacityAvailable and QuotaAvailable are explicit admission results.
	CapacityAvailable bool `json:"capacity_available"`
	QuotaAvailable    bool `json:"quota_available"`
	// QuotedAt and ValidUntil bound price staleness.
	QuotedAt   time.Time `json:"quoted_at"`
	ValidUntil time.Time `json:"valid_until"`
	// LineItems provide an auditable estimate breakdown.
	LineItems []QuoteLineItem `json:"line_items,omitempty"`
	// Selection records non-secret provider choices such as image or SKU IDs.
	Selection map[string]string `json:"selection,omitempty"`
}

// State is the provider-reconciled lifecycle state of a Cloud Lease.
type State string

const (
	// StateAcquiring means provider mutation began but active inventory is not yet complete.
	StateAcquiring State = "acquiring"
	// StateActive means all requested resources exist.
	StateActive State = "active"
	// StateReleasePending means cleanup is required and residual resources remain.
	StateReleasePending State = "release_pending"
	// StateReleased means exact provider reconciliation proves empty inventory.
	StateReleased State = "released"
)

// Resource is one non-secret provider inventory item belonging to a Lease.
type Resource struct {
	// ID is the provider resource identifier.
	ID string `json:"id"`
	// Kind is the provider-neutral category such as compute, disk, address, or VPC.
	Kind string `json:"kind"`
	// Role is the logical Plan role assigned to this resource.
	Role string `json:"role"`
	// ParentID optionally records a provider attachment relationship.
	ParentID string `json:"parent_id,omitempty"`
	// Billable reports whether this resource can accrue cost.
	Billable bool `json:"billable"`
	// SizeBytes records provider-advertised storage capacity when applicable.
	SizeBytes int64 `json:"size_bytes,omitempty"`
	// PrivateAddress and PublicAddress are non-secret current inventory.
	PrivateAddress string `json:"private_address,omitempty"`
	PublicAddress  string `json:"public_address,omitempty"`
	// Tags carry mandatory and caller-owned resource identity.
	Tags map[string]string `json:"tags"`
	// Attributes contain bounded non-secret provider selections.
	Attributes map[string]string `json:"attributes,omitempty"`
}

// Protocol is the network protocol admitted by an AccessGrant.
type Protocol string

const (
	// ProtocolTCP admits TCP traffic.
	ProtocolTCP Protocol = "tcp"
	// ProtocolUDP admits UDP traffic.
	ProtocolUDP Protocol = "udp"
)

// AccessGrant is one typed, expiring provider network rule.
type AccessGrant struct {
	// ID is unique inside one Lease and makes GrantAccess idempotent.
	ID string `json:"id"`
	// TargetRole selects a HostGroupPlan role.
	TargetRole string `json:"target_role"`
	// Protocol and port range define the admitted traffic.
	Protocol Protocol `json:"protocol"`
	PortFrom uint16   `json:"port_from"`
	PortTo   uint16   `json:"port_to"`
	// SourcePrefix is the admitted IPv4 or IPv6 network.
	SourcePrefix netip.Prefix `json:"source_prefix"`
	// Until is no later than immutable Lease expiry.
	Until time.Time `json:"until"`
}

// Receipt is the non-secret provider inventory and lifecycle proof for one Lease.
type Receipt struct {
	// LeaseID and RequestID bind the Receipt to the caller identity.
	LeaseID   string `json:"lease_id"`
	RequestID string `json:"request_id"`
	// Provider, Region, and Zone identify the reconciled inventory boundary.
	Provider string `json:"provider"`
	Region   string `json:"region"`
	Zone     string `json:"zone"`
	// AccountIDHash binds this receipt to a non-secret provider account identity.
	AccountIDHash string `json:"account_id_hash,omitempty"`
	// Repository and Operator record the trusted ownership boundary.
	Repository string `json:"repository"`
	Operator   string `json:"operator"`
	// PlanDigest binds all reconstructed inventory to one immutable Plan.
	PlanDigest string `json:"plan_digest"`
	// Provenance repeats applicable immutable input identities from the Plan.
	Provenance Provenance `json:"provenance,omitempty"`
	// State is derived from reconciled provider inventory.
	State State `json:"state"`
	// CreatedAt is the immutable Lease creation request timestamp.
	CreatedAt time.Time `json:"created_at"`
	// ExpiresAt is the immutable cleanup deadline.
	ExpiresAt time.Time `json:"expires_at"`
	// Quote is the admitted provider decision used to acquire this Lease.
	Quote Quote `json:"quote"`
	// Tags contain the mandatory Lease identity and reviewed consumer tags.
	Tags map[string]string `json:"tags"`
	// Resources is the complete current provider inventory for this Lease.
	Resources []Resource `json:"resources"`
	// AccessGrants is the complete current typed ingress inventory.
	AccessGrants []AccessGrant `json:"access_grants,omitempty"`
}

// Selector binds a lifecycle mutation to one exact known Lease identity.
type Selector struct {
	// LeaseID and RequestID identify the exact caller allocation.
	LeaseID   string `json:"lease_id"`
	RequestID string `json:"request_id"`
	// Provider and Region constrain inventory discovery.
	Provider string `json:"provider"`
	Region   string `json:"region"`
	// Repository prevents cross-repository mutation.
	Repository string `json:"repository"`
	// PlanDigest prevents mutation after an identity collision.
	PlanDigest string `json:"plan_digest"`
}

// ZeroInventoryProof records one exact provider inventory query that found no
// resources for a Lease Selector.
type ZeroInventoryProof struct {
	// Selector is the exact immutable identity whose inventory was queried.
	Selector Selector `json:"selector"`
	// AccountIDHash binds the proof to a non-secret provider account identity.
	AccountIDHash string `json:"account_id_hash"`
	// ObservedAt is the UTC time at which all declared scopes were empty.
	ObservedAt time.Time `json:"observed_at"`
	// Scopes names every provider inventory category queried by the adapter.
	Scopes []string `json:"scopes"`
}

// ReleaseResult contains either residual inventory or an exact zero-inventory
// proof. Exactly one field must be present.
type ReleaseResult struct {
	// Receipt contains cleanup-pending residual inventory.
	Receipt *Receipt `json:"receipt,omitempty"`
	// ZeroInventory proves the exact Selector has no related resources.
	ZeroInventory *ZeroInventoryProof `json:"zero_inventory,omitempty"`
}

// QuoteRequest is the normalized request delivered to a Provider.
type QuoteRequest struct {
	// Plan is normalized and detached from caller-owned memory.
	Plan Plan `json:"plan"`
	// PlanDigest is the controller-computed immutable Plan identity.
	PlanDigest string `json:"plan_digest"`
}

// AcquireRequest is the accepted immutable Plan and Quote delivered to a Provider.
type AcquireRequest struct {
	// Plan is the exact normalized Plan admitted by Quote.
	Plan Plan `json:"plan"`
	// PlanDigest binds Plan, Quote, tags, and resulting Receipt.
	PlanDigest string `json:"plan_digest"`
	// RequestedAt is the immutable logical Lease creation timestamp.
	RequestedAt time.Time `json:"requested_at"`
	// Quote is the still-valid provider decision being accepted.
	Quote Quote `json:"quote"`
	// BaseTags must be repeated by every resource in the Lease.
	BaseTags map[string]string `json:"base_tags"`
}

// InventoryFilter scopes provider discovery for Sweep.
type InventoryFilter struct {
	// Repository is the mandatory ownership boundary for discovery.
	Repository string `json:"repository"`
}

// SweepRequest scopes one deterministic expiry reconciliation.
type SweepRequest struct {
	// Repository is the mandatory ownership boundary for reconciliation.
	Repository string `json:"repository"`
}

// SweepFailure records one Lease whose reconciliation did not complete.
type SweepFailure struct {
	// LeaseID identifies the Receipt that could not be reconciled.
	LeaseID string `json:"lease_id"`
	// Reason is a stable non-secret failure category.
	Reason string `json:"reason"`
}

// SweepResult reports deterministic access-revocation and release outcomes.
type SweepResult struct {
	// Examined is the number of provider Receipts returned for the repository.
	Examined int `json:"examined"`
	// RevokedAccess contains deterministic lease/grant identities.
	RevokedAccess []string `json:"revoked_access"`
	// Released contains Leases proved to have zero inventory during this Sweep.
	Released []string `json:"released"`
	// Pending contains Leases that still have residual provider inventory.
	Pending []string `json:"pending"`
	// Failed contains stable reconciliation categories without secrets.
	Failed []SweepFailure `json:"failed"`
}

// Provider is the narrow infrastructure port implemented by cloud adapters.
type Provider interface {
	// Name returns the stable provider identifier accepted by Lease Plans.
	Name() string
	// Quote performs only read operations and returns current admission data.
	Quote(context.Context, QuoteRequest) (Quote, error)
	// Acquire creates or reconstructs one exact idempotent Lease.
	Acquire(context.Context, AcquireRequest) (Receipt, error)
	// Inspect reconstructs one exact Lease from provider inventory.
	Inspect(context.Context, Selector) (Receipt, error)
	// List reconstructs repository-scoped inventory for Sweep.
	List(context.Context, InventoryFilter) ([]Receipt, error)
	// GrantAccess creates one exact typed ingress rule idempotently.
	GrantAccess(context.Context, Selector, AccessGrant) (Receipt, error)
	// RevokeAccess removes one exact typed ingress rule idempotently.
	RevokeAccess(context.Context, Selector, string) (Receipt, error)
	// Release removes the dependency graph and returns residual inventory or an exact absence proof.
	Release(context.Context, Selector) (ReleaseResult, error)
}

// MandatoryBaseTagKeys returns Lease-level tag keys in deterministic order.
func MandatoryBaseTagKeys() []string {
	return []string{
		TagManagedBy,
		TagLeaseID,
		TagRequestID,
		TagRepository,
		TagOperator,
		TagProvider,
		TagRegion,
		TagPlanDigest,
		TagCreatedAt,
		TagExpiresAt,
	}
}

// MandatoryResourceTagKeys returns resource-level tag keys in deterministic order.
func MandatoryResourceTagKeys() []string {
	return append(MandatoryBaseTagKeys(), TagResourceRole)
}
