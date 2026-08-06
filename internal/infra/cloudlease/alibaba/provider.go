// Package alibaba implements Alibaba Cloud Lease discovery and lifecycle
// operations. The initial adapter is deliberately Quote-only: its API seam
// contains read methods and every lifecycle mutation fails closed.
package alibaba

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudlease"
)

const (
	// ProviderName is the stable Cloud Lease provider identity.
	ProviderName = "alibaba"
	// RegionHangzhou is the only region authorized by the chat-lifecycle design.
	RegionHangzhou = "cn-hangzhou"

	quoteTTL                   = 10 * time.Minute
	bytesPerGiB                = int64(1 << 30)
	providerArchitectureX86    = "x86_64"
	providerBillingPostPaid    = "PostPaid"
	providerSpotNoSpot         = "NoSpot"
	providerDiskESSD           = "cloud_essd"
	providerDiskLevelPL0       = "PL0"
	providerInternetPayTraffic = "PayByTraffic"
	planDiskESSD               = "essd"
	eipRetentionWaiverMaxQuota = int64(2_000)
	// The risk allowance is 50 times the reviewed cn-hangzhou published rate
	// and applies for the full Lease plus bounded cleanup, even when waiver
	// evidence is present. It expires so stale billing policy fails closed.
	eipRetentionRiskUnitCostMicros = int64(1_000_000)
	eipCleanupBillingHours         = 4
	eipBillingEvidenceVersion      = "alibaba-eip-payg-cn-mainland-2026-08-07"
	maxSystemDiskGiB               = 2_048
	maxDataDiskGiB                 = 32_768
)

var (
	// ErrInvalidConfig reports a missing adapter dependency or invalid setting.
	ErrInvalidConfig = errors.New("internal/infra/cloudlease/alibaba: invalid config")
	// ErrUnsupportedPlan reports a valid generic Plan outside implemented Alibaba capabilities.
	ErrUnsupportedPlan = errors.New("internal/infra/cloudlease/alibaba: unsupported plan")
	// ErrDiscoveryUnavailable reports missing or unparseable read-only provider evidence.
	ErrDiscoveryUnavailable = errors.New("internal/infra/cloudlease/alibaba: discovery unavailable")
	// ErrReadOnly reports a lifecycle operation intentionally unavailable before #800.
	ErrReadOnly = errors.New("internal/infra/cloudlease/alibaba: quote-only provider")

	ubuntu2404ImagePattern = regexp.MustCompile(`^ubuntu_24_04_x64_20G_alibase_[0-9]{8}\.vhd$`)
	// eipBillingEvidenceValidUntil forces periodic price and waiver review.
	eipBillingEvidenceValidUntil = time.Date(2026, time.November, 6, 0, 0, 0, 0, time.UTC)
)

// Zone contains the provider fields used to choose an ESSD-capable placement.
type Zone struct {
	// ID is the exact availability-zone identifier.
	ID string
	// SupportsESSD reports zone-level cloud_essd capability.
	SupportsESSD bool
}

// InstanceType contains the fields required to reject wrong, GPU, and
// burst-credit-backed compute candidates.
type InstanceType struct {
	// ID is the concrete ECS instance-type identifier.
	ID string
	// Architecture is the ECS CPU architecture label.
	Architecture string
	// VCPUs and MemoryBytes are the exact advertised compute shape.
	VCPUs       int
	MemoryBytes int64
	// GPUCount must be zero for the current adapter capability.
	GPUCount int
	// FamilyLevel distinguishes credit-backed burstable families.
	FamilyLevel string
}

// Image is one provider-official operating-system image candidate.
type Image struct {
	// ID is the concrete provider image identifier.
	ID string
	// CreationTime is provider-reported and selects the newest allowlisted image.
	CreationTime time.Time
	// Official and CloudInit are independently required provenance signals.
	Official  bool
	CloudInit bool
	// Architecture must match the requested compute architecture.
	Architecture string
}

// Availability is the exact capacity compatibility decision for an offer.
type Availability struct {
	// Instance reports current regular PostPaid stock for the exact type.
	Instance bool
	// SystemESSDPL0 and DataESSDPL0 include WithStock status and requested-size range checks.
	SystemESSDPL0 bool
	DataESSDPL0   bool
}

// AvailabilityRequest describes the provider capabilities checked in one zone.
type AvailabilityRequest struct {
	// Region and Zone bind the inventory boundary.
	Region string
	Zone   string
	// InstanceType is the exact candidate shared by all requested hosts.
	InstanceType string
	// HostCount is used for admission evidence and future capacity APIs.
	HostCount int
	// SystemDiskSizesGiB and DataDiskSizesGiB must all fit one returned ESSD range.
	SystemDiskSizesGiB []int
	DataDiskSizesGiB   []int
}

// VCPUQuota is the provider-reported PostPaid vCPU ceiling and current use.
type VCPUQuota struct {
	// Limit is the complete PostPaid vCPU account ceiling.
	Limit int64
	// Used is the provider-reported currently consumed vCPU count.
	Used int64
}

// EIPQuota proves both one-address headroom and the direct-ECS retention-fee waiver.
type EIPQuota struct {
	// Limit is the account's pay-as-you-go EIP ceiling.
	Limit int64
	// Used is the current pay-as-you-go EIP allocation count.
	Used int64
}

// PriceKind distinguishes hourly host-plus-disk quotes from EIP traffic quotes.
type PriceKind string

const (
	// PriceKindHost requests one hour of one PostPaid ECS host and its exact disks.
	PriceKindHost PriceKind = "host"
	// PriceKindEIPTraffic requests one GiB of pay-by-traffic EIP egress.
	PriceKindEIPTraffic PriceKind = "eip_traffic"
)

// PriceRequest contains every reviewed input needed for an auditable unit price.
type PriceRequest struct {
	// Kind selects the SDK price resource type.
	Kind PriceKind
	// Region and Zone bind zonal prices; EIP traffic is regional.
	Region string
	Zone   string
	// InstanceType and ImageID identify a host price request.
	InstanceType string
	ImageID      string
	// BillingModel must be regular PostPaid.
	BillingModel string
	// SystemDiskGiB/Class/Level describe the boot disk included in a host unit.
	SystemDiskGiB   int
	SystemDiskClass string
	SystemDiskLevel string
	// DataDiskGiB/Class/Level describe the single supported data disk per host.
	DataDiskGiB   int
	DataDiskClass string
	DataDiskLevel string
	// InternetCharge and PeakBandwidthMbps describe an EIP traffic unit.
	InternetCharge    string
	PeakBandwidthMbps int
}

// Price is one provider-returned unit price in millionths of Currency.
type Price struct {
	// Currency is the provider billing currency.
	Currency string
	// UnitCostMicros is one host-hour or one egress-GiB cost, rounded upward.
	UnitCostMicros int64
}

// ReadAPI is the complete provider authority reachable from Quote. Keeping
// mutation methods out of this interface makes the no-write property structural.
type ReadAPI interface {
	// AccountIDHash verifies the caller and returns a non-secret stable account binding.
	AccountIDHash(context.Context) (string, error)
	// Zones lists current ESSD-capable PostPaid zones.
	Zones(context.Context, string) ([]Zone, error)
	// InstanceTypes lists all paginated exact compute candidates.
	InstanceTypes(context.Context, string, int, int64) ([]InstanceType, error)
	// Images lists all paginated official image candidates compatible with one type.
	Images(context.Context, string, string) ([]Image, error)
	// Availability checks exact instance and disk stock/ranges in one zone.
	Availability(context.Context, AvailabilityRequest) (Availability, error)
	// PostPaidVCPUQuota returns the current compute ceiling and use.
	PostPaidVCPUQuota(context.Context, string, string) (VCPUQuota, error)
	// EIPQuota returns the current pay-as-you-go EIP ceiling and use.
	EIPQuota(context.Context, string) (EIPQuota, error)
	// Price returns a current provider unit price without mutation.
	Price(context.Context, PriceRequest) (Price, error)
}

// Options configures deterministic Quote time.
type Options struct {
	// Now supplies deterministic UTC Quote timestamps and billing-duration rounding.
	Now func() time.Time
}

// Provider selects and quotes Alibaba capacity through a read-only API seam.
type Provider struct {
	// api is deliberately read-only so Quote cannot reach a mutation method.
	api ReadAPI
	// now supplies one injectable logical Quote clock.
	now func() time.Time
}

var _ cloudlease.Provider = (*Provider)(nil)

// New creates a Quote-only Alibaba Provider.
func New(api ReadAPI, options Options) *Provider {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Provider{api: api, now: now}
}

// Name returns the stable adapter identity.
func (*Provider) Name() string { return ProviderName }

// Quote discovers all exact offers and chooses the lowest complete full-Lease
// estimate. Any unknown input that could change the cheapest decision fails closed.
func (p *Provider) Quote(ctx context.Context, request cloudlease.QuoteRequest) (cloudlease.Quote, error) {
	shape, err := providerShapeFor(request.Plan)
	if err != nil {
		return cloudlease.Quote{}, err
	}
	if p == nil || p.api == nil || p.now == nil {
		return cloudlease.Quote{}, ErrInvalidConfig
	}
	now := p.now().UTC()
	if !request.Plan.ExpiresAt.After(now) {
		return cloudlease.Quote{}, ErrUnsupportedPlan
	}
	if !now.Before(eipBillingEvidenceValidUntil) {
		return cloudlease.Quote{}, discoveryError("expired EIP billing evidence", nil)
	}
	hours, ok := ceilingUnits(request.Plan.ExpiresAt.Sub(now), time.Hour)
	if !ok {
		return cloudlease.Quote{}, ErrUnsupportedPlan
	}
	trafficGiB, ok := ceilingBytes(request.Plan.Network.ConservativePublicEgressBytes, bytesPerGiB)
	if !ok {
		return cloudlease.Quote{}, ErrUnsupportedPlan
	}

	accountHash, err := p.api.AccountIDHash(ctx)
	if err != nil || strings.TrimSpace(accountHash) == "" {
		return cloudlease.Quote{}, discoveryError("account", err)
	}
	zones, err := p.api.Zones(ctx, RegionHangzhou)
	if err != nil {
		return cloudlease.Quote{}, discoveryError("zones", err)
	}
	types, err := p.api.InstanceTypes(ctx, RegionHangzhou, shape.vcpus, shape.memoryBytes)
	if err != nil {
		return cloudlease.Quote{}, discoveryError("instance types", err)
	}
	zones = eligibleZones(zones)
	types = eligibleInstanceTypes(types, shape.vcpus, shape.memoryBytes)
	if len(zones) == 0 || len(types) == 0 {
		return cloudlease.Quote{}, cloudlease.ErrCapacityUnavailable
	}

	eipQuota, err := p.api.EIPQuota(ctx, RegionHangzhou)
	if err != nil || eipQuota.Limit <= 0 || eipQuota.Used < 0 || eipQuota.Used > eipQuota.Limit {
		return cloudlease.Quote{}, discoveryError("EIP quota", err)
	}
	if eipQuota.Limit > eipRetentionWaiverMaxQuota {
		return cloudlease.Quote{}, discoveryError("EIP retention-fee waiver", nil)
	}
	if eipQuota.Limit-eipQuota.Used < int64(shape.publicHosts) {
		return cloudlease.Quote{}, cloudlease.ErrQuotaUnavailable
	}
	eipPrice, err := p.api.Price(ctx, PriceRequest{
		Kind: PriceKindEIPTraffic, Region: RegionHangzhou,
		InternetCharge: providerInternetPayTraffic, PeakBandwidthMbps: shape.peakBandwidthMbps,
	})
	if err := validatePrice(eipPrice, request.Plan.Budget.Currency, err); err != nil {
		return cloudlease.Quote{}, discoveryError("EIP price", err)
	}
	eipCost, ok := checkedMultiply(eipPrice.UnitCostMicros, int64(trafficGiB))
	if !ok {
		return cloudlease.Quote{}, discoveryError("EIP price overflow", nil)
	}
	if hours > math.MaxInt-eipCleanupBillingHours {
		return cloudlease.Quote{}, discoveryError("EIP retention quantity overflow", nil)
	}
	eipRetentionQuantity, ok := checkedIntMultiply(hours+eipCleanupBillingHours, shape.publicHosts)
	if !ok {
		return cloudlease.Quote{}, discoveryError("EIP retention quantity overflow", nil)
	}
	eipRetentionCost, ok := checkedMultiply(eipRetentionRiskUnitCostMicros, int64(eipRetentionQuantity))
	if !ok {
		return cloudlease.Quote{}, discoveryError("EIP retention price overflow", nil)
	}

	var best *offer
	sawCapacity := false
	sawQuota := false
	for _, zone := range zones {
		quota, quotaErr := p.api.PostPaidVCPUQuota(ctx, RegionHangzhou, zone.ID)
		if quotaErr != nil || quota.Limit < 0 || quota.Used < 0 || quota.Used > quota.Limit {
			return cloudlease.Quote{}, discoveryError("PostPaid quota", quotaErr)
		}
		if quota.Limit-quota.Used < int64(shape.vcpus*shape.totalHosts) {
			continue
		}
		sawQuota = true
		for _, instanceType := range types {
			availability, availabilityErr := p.api.Availability(ctx, AvailabilityRequest{
				Region: RegionHangzhou, Zone: zone.ID, InstanceType: instanceType.ID,
				HostCount: shape.totalHosts, SystemDiskSizesGiB: shape.systemDiskSizesGiB,
				DataDiskSizesGiB: shape.dataDiskSizesGiB,
			})
			if availabilityErr != nil {
				return cloudlease.Quote{}, discoveryError("availability", availabilityErr)
			}
			if !availability.Instance || !availability.SystemESSDPL0 || !availability.DataESSDPL0 {
				continue
			}
			sawCapacity = true
			images, imageErr := p.api.Images(ctx, RegionHangzhou, instanceType.ID)
			if imageErr != nil {
				return cloudlease.Quote{}, discoveryError("images", imageErr)
			}
			image, found := latestUbuntu2404(images)
			if !found {
				continue
			}
			candidate, quoteErr := p.quoteOffer(ctx, request, shape, zone.ID, instanceType.ID, image.ID, hours, trafficGiB, eipCost, eipRetentionQuantity, eipRetentionCost)
			if quoteErr != nil {
				return cloudlease.Quote{}, quoteErr
			}
			if best == nil || candidate.cost < best.cost ||
				(candidate.cost == best.cost && offerIdentity(candidate) < offerIdentity(*best)) {
				candidateCopy := candidate
				best = &candidateCopy
			}
		}
	}
	if best == nil {
		switch {
		case !sawQuota:
			return cloudlease.Quote{}, cloudlease.ErrQuotaUnavailable
		case !sawCapacity:
			return cloudlease.Quote{}, cloudlease.ErrCapacityUnavailable
		default:
			return cloudlease.Quote{}, cloudlease.ErrCapacityUnavailable
		}
	}

	selection := map[string]string{
		"zone": best.zone, "instance_type": best.instanceType,
		"image_id": best.imageID, "billing_model": providerBillingPostPaid,
		"architecture":         providerArchitectureX86,
		"internet_charge_type": providerInternetPayTraffic, "peak_bandwidth_mbps": strconv.Itoa(shape.peakBandwidthMbps),
		"eip_retention_fee":                "full_lease_plus_cleanup_risk_allowance;direct_ecs_waiver_expected_quota_lte_2000",
		"eip_quota_limit":                  strconv.FormatInt(eipQuota.Limit, 10),
		"eip_retention_risk_unit_micros":   strconv.FormatInt(eipRetentionRiskUnitCostMicros, 10),
		"eip_billing_evidence_version":     eipBillingEvidenceVersion,
		"eip_billing_evidence_valid_until": eipBillingEvidenceValidUntil.Format(time.RFC3339),
		"lease_hours":                      strconv.Itoa(hours), "conservative_public_egress_gib": strconv.Itoa(trafficGiB),
	}
	commonSystemDisk := shape.groups[0].systemDiskGiB
	for _, group := range shape.groups {
		selection[group.role+"_instance_type"] = best.instanceType
		selection[group.role+"_system_disk"] = diskSelection(group.systemDiskGiB)
		selection[group.role+"_data_disk"] = diskSelection(group.dataDiskGiB)
		if group.systemDiskGiB != commonSystemDisk {
			commonSystemDisk = 0
		}
	}
	if commonSystemDisk > 0 {
		selection["system_disk"] = diskSelection(commonSystemDisk)
	}
	validUntil := now.Add(quoteTTL)
	if eipBillingEvidenceValidUntil.Before(validUntil) {
		validUntil = eipBillingEvidenceValidUntil
	}

	return cloudlease.Quote{
		LeaseID: request.Plan.LeaseID, RequestID: request.Plan.RequestID,
		Provider: ProviderName, Region: RegionHangzhou, Zone: best.zone,
		AccountIDHash: accountHash, PlanDigest: request.PlanDigest,
		Currency: request.Plan.Budget.Currency, EstimatedCostMicros: best.cost,
		CapacityAvailable: true, QuotaAvailable: true,
		QuotedAt: now, ValidUntil: validUntil,
		LineItems: best.lineItems,
		Selection: selection,
	}, nil
}

func diskSelection(sizeGiB int) string {
	return providerDiskESSD + ":" + providerDiskLevelPL0 + ":" + strconv.Itoa(sizeGiB) + "GiB"
}

type quoteGroup struct {
	role          string
	count         int
	systemDiskGiB int
	dataDiskGiB   int
}

type providerPlanShape struct {
	vcpus              int
	memoryBytes        int64
	totalHosts         int
	publicHosts        int
	publicRole         string
	peakBandwidthMbps  int
	systemDiskSizesGiB []int
	dataDiskSizesGiB   []int
	groups             []quoteGroup
}

// providerShapeFor validates only capabilities implemented by this adapter:
// regular x86 PostPaid compute, one common type, ESSD PL0 disks, and one
// directly-associated EIP. Workload topology and role policy remain in use cases.
func providerShapeFor(plan cloudlease.Plan) (providerPlanShape, error) {
	if plan.Provider != ProviderName || plan.Region != RegionHangzhou || plan.Budget.Currency != "CNY" ||
		!plan.Network.Isolated || !plan.Network.SingleZone || plan.Network.ConservativePublicEgressBytes <= 0 ||
		len(plan.HostGroups) == 0 {
		return providerPlanShape{}, ErrUnsupportedPlan
	}
	first := plan.HostGroups[0].Compute
	if first.VCPUs <= 0 || first.MemoryBytes <= 0 || !strings.EqualFold(first.Architecture, providerArchitectureX86) ||
		!strings.EqualFold(first.BillingModel, providerBillingPostPaid) || first.AllowBurstable {
		return providerPlanShape{}, ErrUnsupportedPlan
	}
	shape := providerPlanShape{
		vcpus: first.VCPUs, memoryBytes: first.MemoryBytes,
		groups: make([]quoteGroup, 0, len(plan.HostGroups)),
	}
	for _, group := range plan.HostGroups {
		if group.Count <= 0 || group.Compute.VCPUs != first.VCPUs || group.Compute.MemoryBytes != first.MemoryBytes ||
			!strings.EqualFold(group.Compute.Architecture, first.Architecture) ||
			!strings.EqualFold(group.Compute.BillingModel, first.BillingModel) || group.Compute.AllowBurstable ||
			len(group.DataDisks) != 1 {
			return providerPlanShape{}, ErrUnsupportedPlan
		}
		systemGiB, systemOK := providerDiskGiB(group.SystemDisk, maxSystemDiskGiB)
		dataGiB, dataOK := providerDiskGiB(group.DataDisks[0], maxDataDiskGiB)
		if !systemOK || !dataOK || shape.totalHosts > math.MaxInt-group.Count {
			return providerPlanShape{}, ErrUnsupportedPlan
		}
		shape.totalHosts += group.Count
		shape.systemDiskSizesGiB = appendUniqueInt(shape.systemDiskSizesGiB, systemGiB)
		shape.dataDiskSizesGiB = appendUniqueInt(shape.dataDiskSizesGiB, dataGiB)
		shape.groups = append(shape.groups, quoteGroup{
			role: group.Role, count: group.Count, systemDiskGiB: systemGiB, dataDiskGiB: dataGiB,
		})
		switch {
		case group.PublicIPv4:
			if !group.InternetEgress || group.PeakBandwidthMbps <= 0 ||
				(shape.peakBandwidthMbps != 0 && shape.peakBandwidthMbps != group.PeakBandwidthMbps) ||
				shape.publicHosts > math.MaxInt-group.Count {
				return providerPlanShape{}, ErrUnsupportedPlan
			}
			shape.publicHosts += group.Count
			shape.publicRole = group.Role
			shape.peakBandwidthMbps = group.PeakBandwidthMbps
		case group.InternetEgress || group.PeakBandwidthMbps != 0:
			return providerPlanShape{}, ErrUnsupportedPlan
		}
	}
	// Quote v1 has one aggregate public-traffic ceiling and can therefore prove
	// the retention-fee waiver only for one directly-associated EIP.
	if shape.totalHosts <= 0 || shape.publicHosts != 1 || shape.peakBandwidthMbps <= 0 ||
		shape.vcpus > math.MaxInt/shape.totalHosts {
		return providerPlanShape{}, ErrUnsupportedPlan
	}
	return shape, nil
}

func appendUniqueInt(values []int, value int) []int {
	if slices.Contains(values, value) {
		return values
	}
	return append(values, value)
}

func providerDiskGiB(disk cloudlease.DiskPlan, maximumGiB int) (int, bool) {
	if disk.CountPerHost != 1 || disk.SizeBytes <= 0 || disk.SizeBytes%bytesPerGiB != 0 ||
		!strings.EqualFold(disk.Class, planDiskESSD) || !strings.EqualFold(disk.PerformanceLevel, providerDiskLevelPL0) {
		return 0, false
	}
	sizeGiB := disk.SizeBytes / bytesPerGiB
	if sizeGiB <= 0 || sizeGiB > int64(maximumGiB) {
		return 0, false
	}
	return int(sizeGiB), true
}

func eligibleZones(zones []Zone) []Zone {
	result := make([]Zone, 0, len(zones))
	seen := make(map[string]struct{}, len(zones))
	for _, zone := range zones {
		zone.ID = strings.TrimSpace(zone.ID)
		if zone.ID == "" || !strings.HasPrefix(zone.ID, RegionHangzhou+"-") || !zone.SupportsESSD {
			continue
		}
		if _, exists := seen[zone.ID]; exists {
			continue
		}
		seen[zone.ID] = struct{}{}
		result = append(result, zone)
	}
	slices.SortFunc(result, func(left, right Zone) int { return strings.Compare(left.ID, right.ID) })
	return result
}

func eligibleInstanceTypes(instanceTypes []InstanceType, vcpus int, memoryBytes int64) []InstanceType {
	result := make([]InstanceType, 0, len(instanceTypes))
	seen := make(map[string]struct{}, len(instanceTypes))
	for _, instanceType := range instanceTypes {
		instanceType.ID = strings.TrimSpace(instanceType.ID)
		architecture := strings.ToLower(strings.TrimSpace(instanceType.Architecture))
		familyLevel := strings.TrimSpace(instanceType.FamilyLevel)
		if instanceType.ID == "" || (architecture != "x86_64" && architecture != "x86" && architecture != "amd64") ||
			instanceType.VCPUs != vcpus || instanceType.MemoryBytes != memoryBytes || instanceType.GPUCount != 0 ||
			(familyLevel != "EnterpriseLevel" && familyLevel != "EntryLevel") || strings.HasPrefix(instanceType.ID, "ecs.t") {
			continue
		}
		if _, exists := seen[instanceType.ID]; exists {
			continue
		}
		seen[instanceType.ID] = struct{}{}
		result = append(result, instanceType)
	}
	slices.SortFunc(result, func(left, right InstanceType) int { return strings.Compare(left.ID, right.ID) })
	return result
}

func latestUbuntu2404(images []Image) (Image, bool) {
	var latest Image
	found := false
	for _, image := range images {
		image.ID = strings.TrimSpace(image.ID)
		architecture := strings.ToLower(strings.TrimSpace(image.Architecture))
		if !image.Official || !image.CloudInit || architecture != "x86_64" ||
			!ubuntu2404ImagePattern.MatchString(image.ID) || image.CreationTime.IsZero() {
			continue
		}
		if !found || image.CreationTime.After(latest.CreationTime) ||
			(image.CreationTime.Equal(latest.CreationTime) && image.ID > latest.ID) {
			latest = image
			found = true
		}
	}
	return latest, found
}

type offer struct {
	zone         string
	instanceType string
	imageID      string
	cost         int64
	lineItems    []cloudlease.QuoteLineItem
}

func (p *Provider) quoteOffer(ctx context.Context, request cloudlease.QuoteRequest, shape providerPlanShape, zone, instanceType, imageID string, hours, trafficGiB int, eipCost int64, eipRetentionQuantity int, eipRetentionCost int64) (offer, error) {
	lineItems := make([]cloudlease.QuoteLineItem, 0, len(shape.groups)+2)
	total := int64(0)
	for _, group := range shape.groups {
		price, err := p.api.Price(ctx, hostPriceRequest(
			zone, instanceType, imageID, group.systemDiskGiB, group.dataDiskGiB,
		))
		if err := validatePrice(price, request.Plan.Budget.Currency, err); err != nil {
			return offer{}, discoveryError(group.role+" host price", err)
		}
		quantity, ok := checkedIntMultiply(group.count, hours)
		if !ok {
			return offer{}, discoveryError(group.role+" quantity overflow", nil)
		}
		cost, ok := checkedMultiply(price.UnitCostMicros, int64(quantity))
		if !ok {
			return offer{}, discoveryError(group.role+" price overflow", nil)
		}
		total, ok = checkedAdd(total, cost)
		if !ok {
			return offer{}, discoveryError("host price overflow", nil)
		}
		lineItems = append(lineItems, cloudlease.QuoteLineItem{
			Kind: "postpaid_host_hour", Role: group.role, Quantity: quantity, CostMicros: cost,
		})
	}
	total, ok := checkedAdd(total, eipCost)
	if !ok {
		return offer{}, discoveryError("lease price overflow", nil)
	}
	total, ok = checkedAdd(total, eipRetentionCost)
	if !ok {
		return offer{}, discoveryError("EIP retention price overflow", nil)
	}
	lineItems = append(lineItems,
		cloudlease.QuoteLineItem{
			Kind: "eip_public_egress_gib", Role: shape.publicRole,
			Quantity: trafficGiB, CostMicros: eipCost,
		},
		cloudlease.QuoteLineItem{
			Kind: "eip_retention_policy_risk_hour", Role: shape.publicRole,
			Quantity: eipRetentionQuantity, CostMicros: eipRetentionCost,
		},
	)
	return offer{
		zone: zone, instanceType: instanceType, imageID: imageID, cost: total,
		lineItems: lineItems,
	}, nil
}

func hostPriceRequest(zone, instanceType, imageID string, systemDiskGiB, dataDiskGiB int) PriceRequest {
	return PriceRequest{
		Kind: PriceKindHost, Region: RegionHangzhou, Zone: zone,
		InstanceType: instanceType, ImageID: imageID, BillingModel: providerBillingPostPaid,
		SystemDiskGiB: systemDiskGiB, SystemDiskClass: providerDiskESSD, SystemDiskLevel: providerDiskLevelPL0,
		DataDiskGiB: dataDiskGiB, DataDiskClass: providerDiskESSD, DataDiskLevel: providerDiskLevelPL0,
	}
}

func validatePrice(price Price, currency string, err error) error {
	if err != nil {
		return err
	}
	if strings.TrimSpace(price.Currency) != currency || price.UnitCostMicros <= 0 {
		return ErrDiscoveryUnavailable
	}
	return nil
}

func discoveryError(stage string, err error) error {
	if err == nil {
		return fmt.Errorf("%w: %s", ErrDiscoveryUnavailable, stage)
	}
	return fmt.Errorf("%w: %s: %v", ErrDiscoveryUnavailable, stage, err)
}

func offerIdentity(value offer) string { return value.zone + "\x00" + value.instanceType }

func ceilingUnits(duration, unit time.Duration) (int, bool) {
	if duration <= 0 || unit <= 0 {
		return 0, false
	}
	units := duration / unit
	if duration%unit != 0 {
		units++
	}
	if units <= 0 || int64(units) > int64(math.MaxInt) {
		return 0, false
	}
	return int(units), true
}

func ceilingBytes(value, unit int64) (int, bool) {
	if value <= 0 || unit <= 0 || value > math.MaxInt64-(unit-1) {
		return 0, false
	}
	units := (value + unit - 1) / unit
	if units > int64(math.MaxInt) {
		return 0, false
	}
	return int(units), true
}

func checkedIntMultiply(left, right int) (int, bool) {
	if left <= 0 || right <= 0 || left > math.MaxInt/right {
		return 0, false
	}
	return left * right, true
}

func checkedMultiply(left, right int64) (int64, bool) {
	if left <= 0 || right <= 0 || left > math.MaxInt64/right {
		return 0, false
	}
	return left * right, true
}

func checkedAdd(left, right int64) (int64, bool) {
	if left < 0 || right < 0 || left > math.MaxInt64-right {
		return 0, false
	}
	return left + right, true
}

// Acquire is unavailable until the mutation adapter is implemented in #800.
func (*Provider) Acquire(context.Context, cloudlease.AcquireRequest) (cloudlease.Receipt, error) {
	return cloudlease.Receipt{}, ErrReadOnly
}

// Inspect is unavailable until inventory reconstruction is implemented in #800.
func (*Provider) Inspect(context.Context, cloudlease.Selector) (cloudlease.Receipt, error) {
	return cloudlease.Receipt{}, ErrReadOnly
}

// List is unavailable until inventory reconstruction is implemented in #800.
func (*Provider) List(context.Context, cloudlease.InventoryFilter) ([]cloudlease.Receipt, error) {
	return nil, ErrReadOnly
}

// GrantAccess is unavailable until the mutation adapter is implemented in #800.
func (*Provider) GrantAccess(context.Context, cloudlease.Selector, cloudlease.AccessGrant) (cloudlease.Receipt, error) {
	return cloudlease.Receipt{}, ErrReadOnly
}

// RevokeAccess is unavailable until the mutation adapter is implemented in #800.
func (*Provider) RevokeAccess(context.Context, cloudlease.Selector, string) (cloudlease.Receipt, error) {
	return cloudlease.Receipt{}, ErrReadOnly
}

// Release is unavailable until the mutation adapter is implemented in #800.
func (*Provider) Release(context.Context, cloudlease.Selector) (cloudlease.ReleaseResult, error) {
	return cloudlease.ReleaseResult{}, ErrReadOnly
}
