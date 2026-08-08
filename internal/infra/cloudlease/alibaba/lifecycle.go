package alibaba

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudlease"
)

const (
	ResourceKindVPC            = "vpc"
	ResourceKindVSwitch        = "vswitch"
	ResourceKindSecurityGroup  = "security-group"
	ResourceKindSecurityRule   = "security-rule"
	ResourceKindInstance       = "instance"
	ResourceKindDisk           = "disk"
	ResourceKindDiskAttachment = "disk-attachment"
	ResourceKindENI            = "eni"
	ResourceKindEIP            = "eip"
	ResourceKindEIPAssociation = "eip-association"
	ResourceKindNATGateway     = "nat-gateway"
	ResourceKindRouteEntry     = "route-entry"
	AccessRulePrivate          = AccessRuleKind("private")
	AccessRuleGrant            = AccessRuleKind("grant")
	lifecycleStateAcquiring    = "acquiring"
	lifecycleStateActive       = "active"
	lifecycleStateCleanup      = "cleanup_pending"
	lifecycleStateTag          = "wukongim-alibaba-lifecycle"
	manifestTag                = "wukongim-alibaba-manifest"
	accountHashTag             = "wukongim-alibaba-account-hash"
	zoneTag                    = "wukongim-alibaba-zone"
	quoteCostTag               = "wukongim-alibaba-quote-cost-micros"
	adapterTagPrefix           = "wukongim-alibaba-"
	leaseVPCIPv4CIDR           = "10.42.0.0/16"
	leaseVSwitchIPv4CIDR       = "10.42.0.0/24"
)

var ErrAmbiguousInventory = errors.New("internal/infra/cloudlease/alibaba: ambiguous inventory")

// LifecycleAsset is one actual or relationship-derived Alibaba resource.
type LifecycleAsset struct {
	ID                string
	Kind              string
	Role              string
	ParentID          string
	Billable          bool
	SizeBytes         int64
	PrivateAddress    string
	PublicAddress     string
	Tags              map[string]string
	Attributes        map[string]string
	Grant             *cloudlease.AccessGrant
	IdentityInherited bool
}

// InventoryQuery bounds lifecycle discovery by region and exact ownership tags.
type InventoryQuery struct {
	Region     string
	LeaseID    string
	Repository string
}

// NetworkCreateRequest creates one isolated VPC, vSwitch, and security group.
type NetworkCreateRequest struct {
	Region          string
	Zone            string
	VPCIPv4CIDR     string
	VSwitchIPv4CIDR string
	ClientToken     string
	Tags            map[string]string
}

// HostCreateRequest creates one private PostPaid host and its exact disks.
type HostCreateRequest struct {
	Region                  string
	Zone                    string
	Role                    string
	Ordinal                 int
	InstanceType            string
	ImageID                 string
	VSwitchID               string
	SecurityGroupID         string
	SystemDiskGiB           int
	DataDiskGiB             int
	PublicIPv4              bool
	AutoReleaseAt           time.Time
	ClientToken             string
	BootstrapAuthorizedKeys []string
	Tags                    map[string]string
}

// PublicAddressCreateRequest creates one traffic-billed EIP.
type PublicAddressCreateRequest struct {
	Region             string
	Role               string
	PeakBandwidthMbps  int
	InternetChargeType string
	ClientToken        string
	Tags               map[string]string
}

// PublicAddressAssociationRequest binds the Lease EIP to the public host.
type PublicAddressAssociationRequest struct {
	Region       string
	Role         string
	AllocationID string
	InstanceID   string
	ClientToken  string
	Tags         map[string]string
}

// AccessRuleKind distinguishes the required private rule from typed grants.
type AccessRuleKind string

// AccessRuleRequest creates or removes one Lease-owned ingress rule.
type AccessRuleRequest struct {
	Region            string
	Kind              AccessRuleKind
	ID                string
	SecurityGroupID   string
	TargetRole        string
	Protocol          cloudlease.Protocol
	PortFrom          uint16
	PortTo            uint16
	SourcePrefix      netip.Prefix
	DestinationPrefix netip.Prefix
	Until             time.Time
	Remove            bool
	Grant             *cloudlease.AccessGrant
	Tags              map[string]string
}

// LifecycleAPI is the paid mutation and exhaustive inventory boundary. It is
// deliberately separate from ReadAPI so Quote stays on a read-only interface.
type LifecycleAPI interface {
	ListAssets(context.Context, InventoryQuery) ([]LifecycleAsset, error)
	CreateNetwork(context.Context, NetworkCreateRequest) ([]LifecycleAsset, error)
	CreateHost(context.Context, HostCreateRequest) ([]LifecycleAsset, error)
	CreatePublicAddress(context.Context, PublicAddressCreateRequest) (LifecycleAsset, error)
	AssociatePublicAddress(context.Context, PublicAddressAssociationRequest) error
	SetAccessRule(context.Context, AccessRuleRequest) error
	SetLifecycleState(context.Context, InventoryQuery, string) error
	DeleteAsset(context.Context, LifecycleAsset) error
}

// Acquire creates the selected topology and then reconstructs it from provider inventory.
func (p *Provider) Acquire(ctx context.Context, request cloudlease.AcquireRequest) (cloudlease.Receipt, error) {
	if p == nil || p.api == nil || p.lifecycle == nil {
		return cloudlease.Receipt{}, ErrReadOnly
	}
	shape, err := providerShapeFor(request.Plan)
	if err != nil {
		return cloudlease.Receipt{}, err
	}
	if err := validateLifecycleAdmission(request, shape); err != nil {
		return cloudlease.Receipt{}, err
	}
	accountHash, err := p.api.AccountIDHash(ctx)
	if err != nil || accountHash != request.Quote.AccountIDHash {
		return cloudlease.Receipt{}, errors.Join(ErrInvalidConfig, err)
	}
	providerTags := lifecycleTags(request, shape)
	query := InventoryQuery{Region: request.Plan.Region, LeaseID: request.Plan.LeaseID, Repository: request.Plan.Repository}
	network, err := p.lifecycle.CreateNetwork(ctx, NetworkCreateRequest{
		Region: request.Plan.Region, Zone: request.Quote.Zone,
		VPCIPv4CIDR: leaseVPCIPv4CIDR, VSwitchIPv4CIDR: leaseVSwitchIPv4CIDR,
		ClientToken: lifecycleClientToken(request.Plan.LeaseID, "network", "", 0), Tags: providerTags,
	})
	if err != nil {
		return cloudlease.Receipt{}, fmt.Errorf("create Lease network: %w", err)
	}
	vSwitchID, err := exactAssetID(network, ResourceKindVSwitch)
	if err != nil {
		return cloudlease.Receipt{}, err
	}
	securityGroupID, err := exactAssetID(network, ResourceKindSecurityGroup)
	if err != nil {
		return cloudlease.Receipt{}, err
	}

	var publicInstance LifecycleAsset
	for _, group := range shape.groups {
		for ordinal := 1; ordinal <= group.count; ordinal++ {
			created, createErr := p.lifecycle.CreateHost(ctx, HostCreateRequest{
				Region: request.Plan.Region, Zone: request.Quote.Zone, Role: group.role, Ordinal: ordinal,
				InstanceType: request.Quote.Selection["instance_type"], ImageID: request.Quote.Selection["image_id"],
				VSwitchID: vSwitchID, SecurityGroupID: securityGroupID,
				SystemDiskGiB: group.systemDiskGiB, DataDiskGiB: group.dataDiskGiB,
				PublicIPv4: false, AutoReleaseAt: request.Plan.ExpiresAt,
				ClientToken:             lifecycleClientToken(request.Plan.LeaseID, "host", group.role, ordinal),
				BootstrapAuthorizedKeys: slices.Clone(request.BootstrapAuthorizedKeys), Tags: providerTags,
			})
			if createErr != nil {
				return cloudlease.Receipt{}, fmt.Errorf("create host %s/%d: %w", group.role, ordinal, createErr)
			}
			instance, instanceErr := exactAsset(created, ResourceKindInstance)
			if instanceErr != nil {
				return cloudlease.Receipt{}, instanceErr
			}
			if group.role == shape.publicRole {
				publicInstance = instance
			}
		}
	}
	publicPrivateAddress, parseAddressErr := netip.ParseAddr(publicInstance.PrivateAddress)
	if publicInstance.ID == "" || parseAddressErr != nil ||
		!netip.MustParsePrefix(leaseVSwitchIPv4CIDR).Contains(publicPrivateAddress) {
		return cloudlease.Receipt{}, ErrAmbiguousInventory
	}
	eip, err := p.lifecycle.CreatePublicAddress(ctx, PublicAddressCreateRequest{
		Region: request.Plan.Region, Role: shape.publicRole, PeakBandwidthMbps: shape.peakBandwidthMbps,
		InternetChargeType: providerInternetPayTraffic,
		ClientToken:        lifecycleClientToken(request.Plan.LeaseID, "eip", shape.publicRole, 1), Tags: providerTags,
	})
	if err != nil {
		return cloudlease.Receipt{}, fmt.Errorf("create EIP: %w", err)
	}
	if err := p.lifecycle.AssociatePublicAddress(ctx, PublicAddressAssociationRequest{
		Region: request.Plan.Region, Role: shape.publicRole, AllocationID: eip.ID, InstanceID: publicInstance.ID,
		ClientToken: lifecycleClientToken(request.Plan.LeaseID, "eip-association", shape.publicRole, 1), Tags: providerTags,
	}); err != nil {
		return cloudlease.Receipt{}, fmt.Errorf("associate EIP: %w", err)
	}
	privatePrefix := netip.MustParsePrefix(leaseVSwitchIPv4CIDR)
	if err := p.lifecycle.SetAccessRule(ctx, AccessRuleRequest{
		Region: request.Plan.Region, Kind: AccessRulePrivate, ID: "private-vswitch",
		SecurityGroupID: securityGroupID, TargetRole: "network", Protocol: cloudlease.ProtocolTCP,
		PortFrom: 1, PortTo: 65535, SourcePrefix: privatePrefix, DestinationPrefix: privatePrefix,
		Until: request.Plan.ExpiresAt, Tags: providerTags,
	}); err != nil {
		return cloudlease.Receipt{}, fmt.Errorf("create private ingress: %w", err)
	}
	destination := netip.PrefixFrom(publicPrivateAddress, 32)
	for index := range request.Plan.Network.InitialAccess {
		grant := request.Plan.Network.InitialAccess[index]
		if err := p.lifecycle.SetAccessRule(ctx, AccessRuleRequest{
			Region: request.Plan.Region, Kind: AccessRuleGrant, ID: grant.ID,
			SecurityGroupID: securityGroupID, TargetRole: grant.TargetRole,
			Protocol: grant.Protocol, PortFrom: grant.PortFrom, PortTo: grant.PortTo,
			SourcePrefix: grant.SourcePrefix, DestinationPrefix: destination,
			Until: grant.Until, Grant: &grant, Tags: providerTags,
		}); err != nil {
			return cloudlease.Receipt{}, fmt.Errorf("create ingress %s: %w", grant.ID, err)
		}
	}
	if err := p.lifecycle.SetLifecycleState(ctx, query, lifecycleStateActive); err != nil {
		return cloudlease.Receipt{}, fmt.Errorf("mark Lease active: %w", err)
	}
	receipt, err := p.Inspect(ctx, selectorFromAcquire(request))
	if err != nil {
		return cloudlease.Receipt{}, err
	}
	if receipt.State != cloudlease.StateActive {
		return receipt, cloudlease.ErrAcquireIncomplete
	}
	receipt.Quote = cloneLifecycleQuote(request.Quote)
	return receipt, nil
}

func validateLifecycleAdmission(request cloudlease.AcquireRequest, shape providerPlanShape) error {
	selection := request.Quote.Selection
	_, bootstrapDigest, bootstrapErr := lifecycleBootstrapIdentity(request.BootstrapAuthorizedKeys)
	if request.PlanDigest == "" || request.Quote.PlanDigest != request.PlanDigest ||
		request.Quote.Zone == "" || selection["zone"] != request.Quote.Zone ||
		selection["instance_type"] == "" || selection["image_id"] == "" ||
		selection["billing_model"] != providerBillingPostPaid ||
		selection["internet_charge_type"] != providerInternetPayTraffic ||
		bootstrapErr != nil || request.BaseTags[cloudlease.TagBootstrapAccessDigest] != bootstrapDigest {
		return cloudlease.ErrInvalidQuote
	}
	if excludedOffer(request.Plan.Placement.ExcludedOffers, request.Quote.Zone, selection["instance_type"]) {
		return cloudlease.ErrInvalidQuote
	}
	for _, grant := range request.Plan.Network.InitialAccess {
		if grant.TargetRole != shape.publicRole || !grant.SourcePrefix.Addr().Is4() {
			return ErrUnsupportedPlan
		}
	}
	return nil
}

func lifecycleTags(request cloudlease.AcquireRequest, shape providerPlanShape) map[string]string {
	tags := maps.Clone(request.BaseTags)
	tags[lifecycleStateTag] = lifecycleStateAcquiring
	tags[manifestTag] = lifecycleManifest(shape, len(request.Plan.Network.InitialAccess))
	tags[accountHashTag] = request.Quote.AccountIDHash
	tags[zoneTag] = request.Quote.Zone
	tags[quoteCostTag] = strconv.FormatInt(request.Quote.EstimatedCostMicros, 10)
	return tags
}

func lifecycleManifest(shape providerPlanShape, grantCount int) string {
	return fmt.Sprintf("v1/%d/%d/%d/%d/%d/%d/%d/1/1/1",
		shape.totalHosts, shape.totalHosts*2, shape.totalHosts,
		shape.totalHosts, shape.publicHosts, shape.publicHosts, grantCount+1)
}

func lifecycleClientToken(leaseID, kind, role string, ordinal int) string {
	sum := sha256.Sum256([]byte(leaseID + "\x00" + kind + "\x00" + role + "\x00" + strconv.Itoa(ordinal)))
	return hex.EncodeToString(sum[:])
}

func exactAssetID(assets []LifecycleAsset, kind string) (string, error) {
	asset, err := exactAsset(assets, kind)
	return asset.ID, err
}

func exactAsset(assets []LifecycleAsset, kind string) (LifecycleAsset, error) {
	var result LifecycleAsset
	count := 0
	for _, asset := range assets {
		if asset.Kind == kind {
			result = asset
			count++
		}
	}
	if count != 1 || strings.TrimSpace(result.ID) == "" {
		return LifecycleAsset{}, ErrAmbiguousInventory
	}
	return result, nil
}

func selectorFromAcquire(request cloudlease.AcquireRequest) cloudlease.Selector {
	return cloudlease.Selector{
		LeaseID: request.Plan.LeaseID, RequestID: request.Plan.RequestID,
		Provider: request.Plan.Provider, Region: request.Plan.Region,
		Repository: request.Plan.Repository, PlanDigest: request.PlanDigest,
	}
}

// Inspect reconstructs one exact Lease from exhaustive provider inventory.
func (p *Provider) Inspect(ctx context.Context, selector cloudlease.Selector) (cloudlease.Receipt, error) {
	if p == nil || p.api == nil || p.lifecycle == nil {
		return cloudlease.Receipt{}, ErrReadOnly
	}
	assets, err := p.lifecycle.ListAssets(ctx, InventoryQuery{
		Region: selector.Region, LeaseID: selector.LeaseID, Repository: selector.Repository,
	})
	if err != nil {
		return cloudlease.Receipt{}, err
	}
	if len(assets) == 0 {
		return cloudlease.Receipt{}, cloudlease.ErrLeaseNotFound
	}
	accountHash, err := p.api.AccountIDHash(ctx)
	if err != nil {
		return cloudlease.Receipt{}, err
	}
	return reconcileLifecycleAssets(assets, accountHash, &selector)
}

// List reconstructs every Lease belonging to one repository for Sweep.
func (p *Provider) List(ctx context.Context, filter cloudlease.InventoryFilter) ([]cloudlease.Receipt, error) {
	if p == nil || p.api == nil || p.lifecycle == nil {
		return nil, ErrReadOnly
	}
	if strings.TrimSpace(filter.Repository) == "" || filter.Repository != strings.TrimSpace(filter.Repository) {
		return nil, ErrInvalidConfig
	}
	assets, err := p.lifecycle.ListAssets(ctx, InventoryQuery{Region: RegionHangzhou, Repository: filter.Repository})
	if err != nil {
		return nil, err
	}
	accountHash, err := p.api.AccountIDHash(ctx)
	if err != nil {
		return nil, err
	}
	grouped := make(map[string][]LifecycleAsset)
	for _, asset := range assets {
		leaseID := strings.TrimSpace(asset.Tags[cloudlease.TagLeaseID])
		if leaseID == "" {
			return nil, ErrAmbiguousInventory
		}
		grouped[leaseID] = append(grouped[leaseID], asset)
	}
	leaseIDs := make([]string, 0, len(grouped))
	for leaseID := range grouped {
		leaseIDs = append(leaseIDs, leaseID)
	}
	sort.Strings(leaseIDs)
	receipts := make([]cloudlease.Receipt, 0, len(leaseIDs))
	for _, leaseID := range leaseIDs {
		receipt, reconcileErr := reconcileLifecycleAssets(grouped[leaseID], accountHash, nil)
		if reconcileErr != nil {
			return nil, reconcileErr
		}
		receipts = append(receipts, receipt)
	}
	return receipts, nil
}

type expectedManifest struct {
	instances       int
	disks           int
	diskAttachments int
	enis            int
	eips            int
	eipAssociations int
	rules           int
	securityGroups  int
	vswitches       int
	vpcs            int
}

func parseLifecycleManifest(value string) (expectedManifest, error) {
	parts := strings.Split(value, "/")
	if len(parts) != 11 || parts[0] != "v1" {
		return expectedManifest{}, ErrAmbiguousInventory
	}
	values := make([]int, 10)
	for index := range values {
		parsed, err := strconv.Atoi(parts[index+1])
		if err != nil || parsed < 0 {
			return expectedManifest{}, ErrAmbiguousInventory
		}
		values[index] = parsed
	}
	return expectedManifest{
		instances: values[0], disks: values[1], diskAttachments: values[2], enis: values[3],
		eips: values[4], eipAssociations: values[5], rules: values[6],
		securityGroups: values[7], vswitches: values[8], vpcs: values[9],
	}, nil
}

func (m expectedManifest) countFor(kind string) (int, bool) {
	switch kind {
	case ResourceKindInstance:
		return m.instances, true
	case ResourceKindDisk:
		return m.disks, true
	case ResourceKindDiskAttachment:
		return m.diskAttachments, true
	case ResourceKindENI:
		return m.enis, true
	case ResourceKindEIP:
		return m.eips, true
	case ResourceKindEIPAssociation:
		return m.eipAssociations, true
	case ResourceKindSecurityRule:
		return m.rules, true
	case ResourceKindSecurityGroup:
		return m.securityGroups, true
	case ResourceKindVSwitch:
		return m.vswitches, true
	case ResourceKindVPC:
		return m.vpcs, true
	default:
		return 0, false
	}
}

func reconcileLifecycleAssets(assets []LifecycleAsset, accountHash string, selector *cloudlease.Selector) (cloudlease.Receipt, error) {
	if len(assets) == 0 || strings.TrimSpace(accountHash) == "" {
		return cloudlease.Receipt{}, ErrAmbiguousInventory
	}
	first := assets[0]
	baseTags := lifecycleBaseTags(first.Tags)
	createdAt, createdErr := time.Parse(time.RFC3339Nano, baseTags[cloudlease.TagCreatedAt])
	expiresAt, expiresErr := time.Parse(time.RFC3339Nano, baseTags[cloudlease.TagExpiresAt])
	cost, costErr := strconv.ParseInt(first.Tags[quoteCostTag], 10, 64)
	manifest, manifestErr := parseLifecycleManifest(first.Tags[manifestTag])
	if createdErr != nil || expiresErr != nil || costErr != nil || cost <= 0 || manifestErr != nil ||
		first.Tags[accountHashTag] != accountHash || first.Tags[zoneTag] == "" {
		return cloudlease.Receipt{}, ErrAmbiguousInventory
	}
	receipt := cloudlease.Receipt{
		LeaseID: baseTags[cloudlease.TagLeaseID], RequestID: baseTags[cloudlease.TagRequestID],
		Provider: baseTags[cloudlease.TagProvider], Region: baseTags[cloudlease.TagRegion], Zone: first.Tags[zoneTag],
		AccountIDHash: accountHash, Repository: baseTags[cloudlease.TagRepository], Operator: baseTags[cloudlease.TagOperator],
		PlanDigest: baseTags[cloudlease.TagPlanDigest], CreatedAt: createdAt.UTC(), ExpiresAt: expiresAt.UTC(),
		Tags: baseTags,
		Provenance: cloudlease.Provenance{
			SourceSHA: baseTags[cloudlease.TagSourceSHA], BundleDigest: baseTags[cloudlease.TagBundleDigest],
		},
	}
	receipt.Quote = cloudlease.Quote{
		LeaseID: receipt.LeaseID, RequestID: receipt.RequestID, Provider: receipt.Provider,
		Region: receipt.Region, Zone: receipt.Zone, AccountIDHash: accountHash, PlanDigest: receipt.PlanDigest,
		// The Alibaba adapter admits only CNY plans, so storing that constant on
		// every resource would consume one of the provider's 20 tag slots.
		Currency: "CNY", EstimatedCostMicros: cost,
		CapacityAvailable: true, QuotaAvailable: true,
	}
	if selector != nil && (receipt.LeaseID != selector.LeaseID || receipt.RequestID != selector.RequestID ||
		receipt.Provider != selector.Provider || receipt.Region != selector.Region ||
		receipt.Repository != selector.Repository || receipt.PlanDigest != selector.PlanDigest) {
		return cloudlease.Receipt{}, cloudlease.ErrLeaseConflict
	}
	counts := make(map[string]int)
	allActive := true
	cleanupPending := false
	identityInherited := false
	unsafeTopology := !lifecycleRelationshipsExact(assets)
	privateRules := 0
	seenIDs := make(map[string]struct{}, len(assets))
	for _, asset := range assets {
		if err := validateLifecycleAsset(asset, baseTags, first.Tags); err != nil {
			return cloudlease.Receipt{}, err
		}
		if _, exists := seenIDs[asset.ID]; exists {
			return cloudlease.Receipt{}, ErrAmbiguousInventory
		}
		seenIDs[asset.ID] = struct{}{}
		counts[asset.Kind]++
		if asset.Kind == ResourceKindSecurityRule {
			switch asset.Attributes["rule_kind"] {
			case string(AccessRulePrivate):
				if asset.Grant != nil {
					return cloudlease.Receipt{}, ErrAmbiguousInventory
				}
				privateRules++
			case string(AccessRuleGrant):
				if asset.Grant == nil {
					return cloudlease.Receipt{}, ErrAmbiguousInventory
				}
			default:
				unsafeTopology = true
			}
		}
		state := asset.Tags[lifecycleStateTag]
		allActive = allActive && state == lifecycleStateActive
		cleanupPending = cleanupPending || state == lifecycleStateCleanup
		identityInherited = identityInherited || asset.IdentityInherited
		resource := cloudlease.Resource{
			ID: asset.ID, Kind: asset.Kind, Role: asset.Role, ParentID: asset.ParentID,
			Billable: asset.Billable, SizeBytes: asset.SizeBytes,
			PrivateAddress: asset.PrivateAddress, PublicAddress: asset.PublicAddress,
			Tags: maps.Clone(asset.Tags), Attributes: maps.Clone(asset.Attributes),
		}
		receipt.Resources = append(receipt.Resources, resource)
		if asset.Grant != nil {
			receipt.AccessGrants = append(receipt.AccessGrants, *asset.Grant)
		}
	}
	exactCounts := true
	extraCounts := false
	for _, kind := range []string{
		ResourceKindInstance, ResourceKindDisk, ResourceKindDiskAttachment, ResourceKindENI,
		ResourceKindEIP, ResourceKindEIPAssociation,
		ResourceKindSecurityGroup, ResourceKindVSwitch, ResourceKindVPC,
	} {
		expected, _ := manifest.countFor(kind)
		exactCounts = exactCounts && counts[kind] == expected
		extraCounts = extraCounts || counts[kind] > expected
	}
	for kind := range counts {
		if kind == ResourceKindSecurityRule {
			continue
		}
		if _, known := manifest.countFor(kind); !known {
			extraCounts = true
			exactCounts = false
		}
	}
	switch {
	case cleanupPending || extraCounts || unsafeTopology:
		receipt.State = cloudlease.StateReleasePending
	case exactCounts && privateRules == 1 && allActive && !identityInherited:
		receipt.State = cloudlease.StateActive
	default:
		receipt.State = cloudlease.StateAcquiring
	}
	slices.SortFunc(receipt.Resources, func(left, right cloudlease.Resource) int {
		return strings.Compare(left.Kind+"\x00"+left.ID, right.Kind+"\x00"+right.ID)
	})
	slices.SortFunc(receipt.AccessGrants, func(left, right cloudlease.AccessGrant) int {
		return strings.Compare(left.ID, right.ID)
	})
	return receipt, nil
}

func lifecycleRelationshipsExact(assets []LifecycleAsset) bool {
	byKind := make(map[string]map[string]LifecycleAsset)
	for _, asset := range assets {
		if byKind[asset.Kind] == nil {
			byKind[asset.Kind] = make(map[string]LifecycleAsset)
		}
		if _, exists := byKind[asset.Kind][asset.ID]; exists {
			return false
		}
		byKind[asset.Kind][asset.ID] = asset
	}
	has := func(kind, id string) bool {
		_, exists := byKind[kind][id]
		return id != "" && exists
	}
	for _, asset := range assets {
		switch asset.Kind {
		case ResourceKindVPC:
			if asset.ParentID != "" {
				return false
			}
		case ResourceKindVSwitch, ResourceKindSecurityGroup:
			if !has(ResourceKindVPC, asset.ParentID) {
				return false
			}
		case ResourceKindInstance:
			if !has(ResourceKindVSwitch, asset.ParentID) {
				return false
			}
		case ResourceKindDisk, ResourceKindENI:
			if !has(ResourceKindInstance, asset.ParentID) {
				return false
			}
		case ResourceKindDiskAttachment:
			disk, exists := byKind[ResourceKindDisk][asset.Attributes["disk_id"]]
			if !exists || !has(ResourceKindInstance, asset.ParentID) || disk.ParentID != asset.ParentID || disk.Attributes["disk_type"] != "data" {
				return false
			}
		case ResourceKindEIP:
			if asset.ParentID != "" {
				return false
			}
		case ResourceKindEIPAssociation:
			eip, exists := byKind[ResourceKindEIP][asset.Attributes["eip_id"]]
			instance, instanceExists := byKind[ResourceKindInstance][asset.ParentID]
			if !exists || !instanceExists || eip.Role != asset.Role || instance.Role != asset.Role {
				return false
			}
		case ResourceKindSecurityRule:
			if !has(ResourceKindSecurityGroup, asset.ParentID) {
				return false
			}
		case ResourceKindNATGateway:
			if !has(ResourceKindVPC, asset.ParentID) {
				return false
			}
		case ResourceKindRouteEntry:
			if asset.ParentID == "" || !has(ResourceKindVPC, asset.Attributes["vpc_id"]) {
				return false
			}
		}
	}
	return true
}

func validateLifecycleAsset(asset LifecycleAsset, baseTags, reference map[string]string) error {
	if strings.TrimSpace(asset.ID) == "" || strings.TrimSpace(asset.Kind) == "" || strings.TrimSpace(asset.Role) == "" ||
		asset.Tags[cloudlease.TagResourceRole] != asset.Role || !maps.Equal(lifecycleBaseTags(asset.Tags), baseTags) {
		return ErrAmbiguousInventory
	}
	for _, key := range []string{lifecycleStateTag, manifestTag, accountHashTag, zoneTag, quoteCostTag} {
		if asset.Tags[key] == "" || asset.Tags[key] != reference[key] && key != lifecycleStateTag {
			return ErrAmbiguousInventory
		}
	}
	if state := asset.Tags[lifecycleStateTag]; state != lifecycleStateAcquiring && state != lifecycleStateActive && state != lifecycleStateCleanup {
		return ErrAmbiguousInventory
	}
	return nil
}

func lifecycleBaseTags(tags map[string]string) map[string]string {
	result := maps.Clone(tags)
	delete(result, cloudlease.TagResourceRole)
	for key := range result {
		if strings.HasPrefix(key, adapterTagPrefix) {
			delete(result, key)
		}
	}
	return result
}

// GrantAccess creates one target-address-constrained ingress rule.
func (p *Provider) GrantAccess(ctx context.Context, selector cloudlease.Selector, grant cloudlease.AccessGrant) (cloudlease.Receipt, error) {
	receipt, err := p.Inspect(ctx, selector)
	if err != nil {
		return cloudlease.Receipt{}, err
	}
	securityGroup, target, err := lifecycleAccessTargets(receipt, grant.TargetRole)
	if err != nil {
		return cloudlease.Receipt{}, err
	}
	tags := providerTagsFromReceipt(receipt)
	grantCopy := grant
	targetAddress, _ := netip.ParseAddr(target.PrivateAddress)
	if err := p.lifecycle.SetAccessRule(ctx, AccessRuleRequest{
		Region: selector.Region, Kind: AccessRuleGrant, ID: grant.ID,
		SecurityGroupID: securityGroup.ID, TargetRole: grant.TargetRole,
		Protocol: grant.Protocol, PortFrom: grant.PortFrom, PortTo: grant.PortTo,
		SourcePrefix:      grant.SourcePrefix,
		DestinationPrefix: netip.PrefixFrom(targetAddress, 32),
		Until:             grant.Until, Grant: &grantCopy, Tags: tags,
	}); err != nil {
		return cloudlease.Receipt{}, err
	}
	return p.Inspect(ctx, selector)
}

// RevokeAccess removes one exact owned rule and is idempotent when absent.
func (p *Provider) RevokeAccess(ctx context.Context, selector cloudlease.Selector, grantID string) (cloudlease.Receipt, error) {
	receipt, err := p.Inspect(ctx, selector)
	if err != nil {
		return cloudlease.Receipt{}, err
	}
	var grant *cloudlease.AccessGrant
	for index := range receipt.AccessGrants {
		if receipt.AccessGrants[index].ID == grantID {
			copy := receipt.AccessGrants[index]
			grant = &copy
			break
		}
	}
	if grant == nil {
		return receipt, nil
	}
	securityGroup, target, err := lifecycleAccessTargets(receipt, grant.TargetRole)
	if err != nil {
		return cloudlease.Receipt{}, err
	}
	targetAddress, _ := netip.ParseAddr(target.PrivateAddress)
	if err := p.lifecycle.SetAccessRule(ctx, AccessRuleRequest{
		Region: selector.Region, Kind: AccessRuleGrant, ID: grant.ID,
		SecurityGroupID: securityGroup.ID, TargetRole: grant.TargetRole,
		Protocol: grant.Protocol, PortFrom: grant.PortFrom, PortTo: grant.PortTo,
		SourcePrefix:      grant.SourcePrefix,
		DestinationPrefix: netip.PrefixFrom(targetAddress, 32),
		Until:             grant.Until, Remove: true, Grant: grant, Tags: providerTagsFromReceipt(receipt),
	}); err != nil {
		return cloudlease.Receipt{}, err
	}
	return p.Inspect(ctx, selector)
}

func lifecycleAccessTargets(receipt cloudlease.Receipt, role string) (cloudlease.Resource, cloudlease.Resource, error) {
	var securityGroup cloudlease.Resource
	var target cloudlease.Resource
	securityGroups := 0
	targets := 0
	for _, resource := range receipt.Resources {
		switch {
		case resource.Kind == ResourceKindSecurityGroup:
			securityGroup = resource
			securityGroups++
		case resource.Kind == ResourceKindInstance && resource.Role == role:
			target = resource
			targets++
		}
	}
	address, addressErr := netip.ParseAddr(target.PrivateAddress)
	if securityGroups != 1 || targets != 1 || addressErr != nil || !address.Is4() {
		return cloudlease.Resource{}, cloudlease.Resource{}, ErrAmbiguousInventory
	}
	return securityGroup, target, nil
}

func providerTagsFromReceipt(receipt cloudlease.Receipt) map[string]string {
	for _, resource := range receipt.Resources {
		if resource.Kind == ResourceKindVPC {
			tags := maps.Clone(resource.Tags)
			delete(tags, cloudlease.TagResourceRole)
			return tags
		}
	}
	return nil
}

// Release repeatedly removes the complete dependency graph for up to the
// configured 30-minute production window and succeeds only after empty inventory.
func (p *Provider) Release(ctx context.Context, selector cloudlease.Selector) (cloudlease.ReleaseResult, error) {
	if p == nil || p.api == nil || p.lifecycle == nil || p.releaseTimeout <= 0 || p.releasePollInterval <= 0 || p.wait == nil {
		return cloudlease.ReleaseResult{}, ErrReadOnly
	}
	accountHash, err := p.api.AccountIDHash(ctx)
	if err != nil {
		return cloudlease.ReleaseResult{}, err
	}
	query := InventoryQuery{Region: selector.Region, LeaseID: selector.LeaseID, Repository: selector.Repository}
	maxAttempts := int(p.releaseTimeout/p.releasePollInterval) + 1
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	stateMarked := false
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		assets, listErr := p.lifecycle.ListAssets(ctx, query)
		if listErr != nil {
			lastErr = errors.Join(lastErr, listErr)
		} else if len(assets) == 0 {
			return zeroInventoryResult(selector, accountHash, p.now().UTC()), lastErr
		} else {
			if identityErr := validateAssetsForSelector(assets, selector, accountHash); identityErr != nil {
				return cloudlease.ReleaseResult{}, identityErr
			}
			if !stateMarked {
				if stateErr := p.lifecycle.SetLifecycleState(ctx, query, lifecycleStateCleanup); stateErr != nil {
					lastErr = errors.Join(lastErr, stateErr)
				} else {
					stateMarked = true
				}
			}
			slices.SortFunc(assets, func(left, right LifecycleAsset) int {
				leftRank, rightRank := lifecycleDeleteRank(left.Kind), lifecycleDeleteRank(right.Kind)
				if leftRank != rightRank {
					return leftRank - rightRank
				}
				return strings.Compare(left.ID, right.ID)
			})
			for _, asset := range assets {
				if deleteErr := p.lifecycle.DeleteAsset(ctx, asset); deleteErr != nil {
					lastErr = errors.Join(lastErr, fmt.Errorf("delete %s/%s: %w", asset.Kind, asset.ID, deleteErr))
				}
			}
			remaining, remainingErr := p.lifecycle.ListAssets(ctx, query)
			if remainingErr == nil && len(remaining) == 0 {
				return zeroInventoryResult(selector, accountHash, p.now().UTC()), lastErr
			}
			lastErr = errors.Join(lastErr, remainingErr)
			if attempt+1 == maxAttempts {
				if remainingErr != nil {
					return cloudlease.ReleaseResult{}, lastErr
				}
				receipt, reconcileErr := reconcileLifecycleAssets(remaining, accountHash, &selector)
				if reconcileErr != nil {
					return cloudlease.ReleaseResult{}, errors.Join(lastErr, reconcileErr)
				}
				receipt.State = cloudlease.StateReleasePending
				return cloudlease.ReleaseResult{Receipt: &receipt}, lastErr
			}
		}
		if waitErr := p.wait(ctx, p.releasePollInterval); waitErr != nil {
			return cloudlease.ReleaseResult{}, errors.Join(lastErr, waitErr)
		}
	}
	return cloudlease.ReleaseResult{}, lastErr
}

func validateAssetsForSelector(assets []LifecycleAsset, selector cloudlease.Selector, accountHash string) error {
	for _, asset := range assets {
		if asset.Tags[cloudlease.TagManagedBy] != cloudlease.ManagedByValue ||
			asset.Tags[cloudlease.TagLeaseID] != selector.LeaseID ||
			asset.Tags[cloudlease.TagRequestID] != selector.RequestID ||
			asset.Tags[cloudlease.TagProvider] != selector.Provider ||
			asset.Tags[cloudlease.TagRegion] != selector.Region ||
			asset.Tags[cloudlease.TagRepository] != selector.Repository ||
			asset.Tags[cloudlease.TagPlanDigest] != selector.PlanDigest ||
			asset.Tags[accountHashTag] != accountHash {
			return cloudlease.ErrLeaseConflict
		}
	}
	return nil
}

func lifecycleDeleteRank(kind string) int {
	switch kind {
	case ResourceKindSecurityRule:
		return 0
	case ResourceKindRouteEntry:
		return 1
	case ResourceKindEIPAssociation:
		return 2
	case ResourceKindEIP:
		return 3
	case ResourceKindDiskAttachment:
		return 4
	case ResourceKindInstance:
		return 5
	case ResourceKindDisk:
		return 6
	case ResourceKindENI:
		return 7
	case ResourceKindSecurityGroup:
		return 8
	case ResourceKindNATGateway:
		return 9
	case ResourceKindVSwitch:
		return 10
	case ResourceKindVPC:
		return 11
	default:
		return 12
	}
}

func zeroInventoryResult(selector cloudlease.Selector, accountHash string, observedAt time.Time) cloudlease.ReleaseResult {
	return cloudlease.ReleaseResult{ZeroInventory: &cloudlease.ZeroInventoryProof{
		Selector: selector, AccountIDHash: accountHash, ObservedAt: observedAt,
		Scopes: []string{
			"disk_attachments", "disks", "eip_associations", "eips", "enis",
			"instances", "nat_gateways", "route_entries", "security_group_rules",
			"security_groups", "vpcs", "vswitches",
		},
	}}
}

func cloneLifecycleAsset(asset LifecycleAsset) LifecycleAsset {
	asset.Tags = maps.Clone(asset.Tags)
	asset.Attributes = maps.Clone(asset.Attributes)
	if asset.Grant != nil {
		grant := *asset.Grant
		asset.Grant = &grant
	}
	return asset
}

func cloneLifecycleAssets(assets []LifecycleAsset) []LifecycleAsset {
	result := make([]LifecycleAsset, len(assets))
	for index := range assets {
		result[index] = cloneLifecycleAsset(assets[index])
	}
	return result
}

func cloneLifecycleQuote(quote cloudlease.Quote) cloudlease.Quote {
	quote.LineItems = slices.Clone(quote.LineItems)
	quote.Selection = maps.Clone(quote.Selection)
	return quote
}

func waitContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
