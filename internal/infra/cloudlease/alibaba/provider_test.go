package alibaba

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudlease"
)

func TestProviderQuoteSelectsCheapestExactPostPaidOfferWithoutMutation(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 15, 0, 0, time.UTC)
	api := completeReadAPI()
	provider := New(api, Options{Now: func() time.Time { return now }})
	controller := cloudlease.NewController(provider, func() time.Time { return now })

	quote, err := controller.Quote(context.Background(), approvedPlan(now))
	if err != nil {
		t.Fatalf("Quote() error = %v", err)
	}
	if quote.Provider != ProviderName || quote.Region != RegionHangzhou || quote.Zone != "cn-hangzhou-h" {
		t.Fatalf("Quote() placement = %s/%s/%s, want %s/%s/cn-hangzhou-h", quote.Provider, quote.Region, quote.Zone, ProviderName, RegionHangzhou)
	}
	if got := quote.Selection["instance_type"]; got != "ecs.g8.large" {
		t.Fatalf("Quote().Selection[instance_type] = %q, want ecs.g8.large", got)
	}
	if got := quote.Selection["image_id"]; got != "ubuntu_24_04_x64_20G_alibase_20260701.vhd" {
		t.Fatalf("Quote().Selection[image_id] = %q, want latest official Ubuntu 24.04 image", got)
	}
	if got := quote.Selection["billing_model"]; got != "PostPaid" {
		t.Fatalf("Quote().Selection[billing_model] = %q, want PostPaid", got)
	}
	if got := quote.Selection["lease_hours"]; got != "7" {
		t.Fatalf("Quote().Selection[lease_hours] = %q, want ceiling of lease duration", got)
	}
	if got := quote.Selection["conservative_public_egress_gib"]; got != "11" {
		t.Fatalf("Quote().Selection[conservative_public_egress_gib] = %q, want 11", got)
	}
	if quote.Selection["eip_retention_fee"] != "full_lease_plus_cleanup_risk_allowance;direct_ecs_waiver_expected_quota_lte_2000" ||
		quote.Selection["eip_quota_limit"] != "20" || quote.Selection["eip_retention_risk_unit_micros"] != "1000000" ||
		quote.Selection["eip_billing_evidence_version"] != eipBillingEvidenceVersion ||
		quote.Selection["eip_billing_evidence_valid_until"] != eipBillingEvidenceValidUntil.Format(time.RFC3339) {
		t.Fatalf("Quote().Selection EIP waiver evidence = %#v", quote.Selection)
	}
	// g8: service 1 CNY/hour, load 0.8 CNY/hour, EIP 0.8 CNY/GiB.
	// The estimate reserves 1 CNY/hour for the full seven-hour Lease plus
	// four cleanup hours despite the expected direct-ECS waiver:
	// 7*(3*1+0.8)+11*0.8+11*1 = 46.4 CNY.
	if quote.EstimatedCostMicros != 46_400_000 {
		t.Fatalf("Quote().EstimatedCostMicros = %d, want 46400000", quote.EstimatedCostMicros)
	}
	if len(quote.LineItems) != 4 || quote.LineItems[0] != (cloudlease.QuoteLineItem{
		Kind: "postpaid_host_hour", Role: "service", Quantity: 21, CostMicros: 21_000_000,
	}) || quote.LineItems[1] != (cloudlease.QuoteLineItem{
		Kind: "postpaid_host_hour", Role: "load", Quantity: 7, CostMicros: 5_600_000,
	}) || quote.LineItems[2] != (cloudlease.QuoteLineItem{
		Kind: "eip_public_egress_gib", Role: "load", Quantity: 11, CostMicros: 8_800_000,
	}) || quote.LineItems[3] != (cloudlease.QuoteLineItem{
		Kind: "eip_retention_policy_risk_hour", Role: "load", Quantity: 11, CostMicros: 11_000_000,
	}) {
		t.Fatalf("Quote().LineItems = %#v, want complete host/disk/EIP and waived retention estimate", quote.LineItems)
	}
	if api.writeCalls != 0 {
		t.Fatalf("read-only API write calls = %d, want 0", api.writeCalls)
	}
	if len(api.priceRequests) != 7 {
		t.Fatalf("price requests = %d, want two host prices per eligible offer plus one EIP price", len(api.priceRequests))
	}
	if len(api.availabilityRequests) == 0 ||
		!slices.Equal(api.availabilityRequests[0].SystemDiskSizesGiB, []int{40}) ||
		!slices.Equal(api.availabilityRequests[0].DataDiskSizesGiB, []int{500, 200}) {
		t.Fatalf("availability disk sizes = %#v, want every requested system and data disk size", api.availabilityRequests)
	}
}

func TestProviderQuoteFiltersIneligibleTypesAndUsesOneTypeForAllHosts(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	api := completeReadAPI()
	api.instanceTypes = append(api.instanceTypes,
		InstanceType{ID: "ecs.g8.xlarge", Architecture: "x86_64", VCPUs: 4, MemoryBytes: 16 << 30, FamilyLevel: "EnterpriseLevel"},
		InstanceType{ID: "ecs.c8.large", Architecture: "x86_64", VCPUs: 2, MemoryBytes: 8 << 30, FamilyLevel: "EnterpriseLevel"},
		InstanceType{ID: "ecs.g8.arm", Architecture: "arm64", VCPUs: 4, MemoryBytes: 8 << 30, FamilyLevel: "EnterpriseLevel"},
		InstanceType{ID: "ecs.gn8.large", Architecture: "x86_64", VCPUs: 4, MemoryBytes: 8 << 30, GPUCount: 1, FamilyLevel: "EnterpriseLevel"},
		InstanceType{ID: "ecs.t6-c1m2.large", Architecture: "x86_64", VCPUs: 4, MemoryBytes: 8 << 30, FamilyLevel: "CreditEntryLevel"},
		InstanceType{ID: "ecs.unknown.large", Architecture: "x86_64", VCPUs: 4, MemoryBytes: 8 << 30},
	)
	for _, instanceType := range []string{"ecs.g8.xlarge", "ecs.c8.large", "ecs.g8.arm", "ecs.gn8.large", "ecs.t6-c1m2.large", "ecs.unknown.large"} {
		api.availability[offerKey("cn-hangzhou-h", instanceType)] = Availability{Instance: true, SystemESSDPL0: true, DataESSDPL0: true}
		api.images[instanceType] = []Image{{ID: "ubuntu_24_04_x64_20G_alibase_20260701.vhd", CreationTime: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), Official: true, CloudInit: true, Architecture: "x86_64"}}
		api.hostPrices[priceKey("cn-hangzhou-h", instanceType, 500)] = 1
		api.hostPrices[priceKey("cn-hangzhou-h", instanceType, 200)] = 1
	}
	provider := New(api, Options{Now: func() time.Time { return now }})
	controller := cloudlease.NewController(provider, func() time.Time { return now })

	quote, err := controller.Quote(context.Background(), approvedPlan(now))
	if err != nil {
		t.Fatalf("Quote() error = %v", err)
	}
	for _, request := range api.priceRequests {
		if request.Kind == PriceKindHost && request.InstanceType != quote.Selection["instance_type"] &&
			request.InstanceType != "ecs.c8.large-cheaper" && request.InstanceType != "ecs.g8.large" {
			t.Fatalf("priced ineligible instance type %q", request.InstanceType)
		}
	}
	if quote.Selection["service_instance_type"] != quote.Selection["load_instance_type"] {
		t.Fatalf("service/load instance selections differ: %#v", quote.Selection)
	}
}

func TestProviderQuoteFailsClosedOnMissingAdmissionInput(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		change func(*fakeReadAPI)
		want   error
	}{
		{name: "account", change: func(api *fakeReadAPI) { api.accountHash = "" }, want: ErrDiscoveryUnavailable},
		{name: "zone", change: func(api *fakeReadAPI) { api.zones = nil }, want: cloudlease.ErrCapacityUnavailable},
		{name: "candidate", change: func(api *fakeReadAPI) { api.instanceTypes = nil }, want: cloudlease.ErrCapacityUnavailable},
		{name: "image", change: func(api *fakeReadAPI) { api.images = nil }, want: cloudlease.ErrCapacityUnavailable},
		{name: "disk compatibility", change: func(api *fakeReadAPI) {
			api.availability[offerKey("cn-hangzhou-h", "ecs.g8.large")] = Availability{}
			api.availability[offerKey("cn-hangzhou-i", "ecs.g8.large")] = Availability{}
			api.availability[offerKey("cn-hangzhou-h", "ecs.c8.large-cheaper")] = Availability{}
		}, want: cloudlease.ErrCapacityUnavailable},
		{name: "quota", change: func(api *fakeReadAPI) {
			api.quotas["cn-hangzhou-h"] = VCPUQuota{Limit: 16, Used: 1}
			api.quotas["cn-hangzhou-i"] = VCPUQuota{Limit: 16, Used: 1}
		}, want: cloudlease.ErrQuotaUnavailable},
		{name: "EIP quota missing", change: func(api *fakeReadAPI) {
			api.eipQuota = EIPQuota{}
		}, want: ErrDiscoveryUnavailable},
		{name: "EIP quota exhausted", change: func(api *fakeReadAPI) {
			api.eipQuota = EIPQuota{Limit: 20, Used: 20}
		}, want: cloudlease.ErrQuotaUnavailable},
		{name: "EIP waiver unproven", change: func(api *fakeReadAPI) {
			api.eipQuota = EIPQuota{Limit: 2_001, Used: 0}
		}, want: ErrDiscoveryUnavailable},
		{name: "candidate price", change: func(api *fakeReadAPI) {
			delete(api.hostPrices, priceKey("cn-hangzhou-h", "ecs.c8.large-cheaper", 500))
		}, want: ErrDiscoveryUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			api := completeReadAPI()
			test.change(api)
			provider := New(api, Options{Now: func() time.Time { return now }})
			controller := cloudlease.NewController(provider, func() time.Time { return now })

			_, err := controller.Quote(context.Background(), approvedPlan(now))
			if !errors.Is(err, test.want) {
				t.Fatalf("Quote() error = %v, want %v", err, test.want)
			}
			if api.writeCalls != 0 {
				t.Fatalf("read-only API write calls = %d, want 0", api.writeCalls)
			}
		})
	}
}

func TestProviderQuoteRejectsUnsupportedProviderCapabilityBeforeDiscovery(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		change func(*cloudlease.Plan)
	}{
		{name: "region", change: func(plan *cloudlease.Plan) { plan.Region = "cn-shanghai" }},
		{name: "billing", change: func(plan *cloudlease.Plan) { plan.HostGroups[0].Compute.BillingModel = "spot" }},
		{name: "burstable", change: func(plan *cloudlease.Plan) { plan.HostGroups[0].Compute.AllowBurstable = true }},
		{name: "architecture", change: func(plan *cloudlease.Plan) { plan.HostGroups[0].Compute.Architecture = "arm64" }},
		{name: "mixed compute", change: func(plan *cloudlease.Plan) { plan.HostGroups[1].Compute.VCPUs = 8 }},
		{name: "disk class", change: func(plan *cloudlease.Plan) { plan.HostGroups[0].DataDisks[0].Class = "cloud" }},
		{name: "fractional disk", change: func(plan *cloudlease.Plan) { plan.HostGroups[0].DataDisks[0].SizeBytes++ }},
		{name: "multiple data disks", change: func(plan *cloudlease.Plan) {
			plan.HostGroups[0].DataDisks = append(plan.HostGroups[0].DataDisks, plan.HostGroups[0].DataDisks[0])
		}},
		{name: "multiple public EIPs", change: func(plan *cloudlease.Plan) { plan.HostGroups[1].Count = 2 }},
		{name: "private egress", change: func(plan *cloudlease.Plan) { plan.HostGroups[0].InternetEgress = true }},
		{name: "traffic assumption", change: func(plan *cloudlease.Plan) { plan.Network.ConservativePublicEgressBytes = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			api := completeReadAPI()
			provider := New(api, Options{Now: func() time.Time { return now }})
			plan := approvedPlan(now)
			test.change(&plan)

			_, err := provider.Quote(context.Background(), cloudlease.QuoteRequest{Plan: plan})
			if !errors.Is(err, ErrUnsupportedPlan) {
				t.Fatalf("Quote() error = %v, want ErrUnsupportedPlan", err)
			}
			if api.readCalls != 0 || api.writeCalls != 0 {
				t.Fatalf("API calls = read:%d write:%d, want zero", api.readCalls, api.writeCalls)
			}
		})
	}
}

func TestProviderShapeAcceptsWorkloadTopologyChosenByUseCase(t *testing.T) {
	plan := approvedPlan(time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC))
	plan.HostGroups[0].Role = "replica"
	plan.HostGroups[0].Count = 2
	plan.HostGroups[0].DataDisks[0].SizeBytes = 400 << 30
	plan.HostGroups[1].Role = "coordinator"
	plan.HostGroups[1].DataDisks[0].SizeBytes = 300 << 30
	plan.HostGroups[1].PeakBandwidthMbps = 19

	shape, err := providerShapeFor(plan)
	if err != nil {
		t.Fatalf("providerShapeFor() error = %v", err)
	}
	if shape.totalHosts != 3 || shape.publicRole != "coordinator" || shape.peakBandwidthMbps != 19 ||
		len(shape.groups) != 2 || shape.groups[0].role != "replica" || shape.groups[0].dataDiskGiB != 400 ||
		shape.groups[1].role != "coordinator" || shape.groups[1].dataDiskGiB != 300 {
		t.Fatalf("providerShapeFor() = %#v, want generic provider capability mapping", shape)
	}
}

func TestProviderQuoteFailsClosedAfterBillingEvidenceExpires(t *testing.T) {
	now := eipBillingEvidenceValidUntil
	api := completeReadAPI()
	provider := New(api, Options{Now: func() time.Time { return now }})

	_, err := provider.Quote(context.Background(), cloudlease.QuoteRequest{Plan: approvedPlan(now)})
	if !errors.Is(err, ErrDiscoveryUnavailable) {
		t.Fatalf("Quote() error = %v, want ErrDiscoveryUnavailable", err)
	}
	if api.readCalls != 0 || api.writeCalls != 0 {
		t.Fatalf("API calls = read:%d write:%d, want zero with stale billing evidence", api.readCalls, api.writeCalls)
	}
}

func TestProviderQuoteValidityCannotOutliveBillingEvidence(t *testing.T) {
	now := eipBillingEvidenceValidUntil.Add(-5 * time.Minute)
	api := completeReadAPI()
	provider := New(api, Options{Now: func() time.Time { return now }})
	controller := cloudlease.NewController(provider, func() time.Time { return now })

	quote, err := controller.Quote(context.Background(), approvedPlan(now))
	if err != nil {
		t.Fatalf("Quote() error = %v", err)
	}
	if !quote.ValidUntil.Equal(eipBillingEvidenceValidUntil) {
		t.Fatalf("Quote().ValidUntil = %s, want billing evidence expiry %s", quote.ValidUntil, eipBillingEvidenceValidUntil)
	}
}

func TestProviderQuoteFailsBeforeMutationWhenCheapestOfferExceedsRemainingEnvelope(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	api := completeReadAPI()
	provider := New(api, Options{Now: func() time.Time { return now }})
	controller := cloudlease.NewController(provider, func() time.Time { return now })
	plan := approvedPlan(now)
	plan.Budget.CommittedMicros = plan.Budget.LimitMicros - 1

	_, err := controller.Quote(context.Background(), plan)
	if !errors.Is(err, cloudlease.ErrCostLimitExceeded) {
		t.Fatalf("Quote() error = %v, want ErrCostLimitExceeded", err)
	}
	if api.writeCalls != 0 {
		t.Fatalf("read-only API write calls = %d, want 0", api.writeCalls)
	}
}

func approvedPlan(now time.Time) cloudlease.Plan {
	compute := cloudlease.ComputePlan{
		VCPUs: 4, MemoryBytes: 8 << 30, Architecture: "x86_64",
		BillingModel: "postpaid", AllowBurstable: false,
	}
	system := cloudlease.DiskPlan{Role: "system", CountPerHost: 1, SizeBytes: 40 << 30, Class: "essd", PerformanceLevel: "PL0"}
	return cloudlease.Plan{
		Schema: cloudlease.PlanSchemaV1, LeaseID: "lease-quote-1", RequestID: "request-1",
		Provider: ProviderName, Region: RegionHangzhou, Repository: "WuKongIM/WuKongIM", Operator: "tester",
		ExpiresAt: now.Add(6*time.Hour + time.Minute),
		Budget:    cloudlease.Budget{Currency: "CNY", LimitMicros: 1_500_000_000, CommittedMicros: 100_000_000},
		Network:   cloudlease.NetworkPlan{Isolated: true, SingleZone: true, ConservativePublicEgressBytes: 10<<30 + 1},
		HostGroups: []cloudlease.HostGroupPlan{
			{
				Role: "service", Count: 3, Compute: compute, SystemDisk: system,
				DataDisks: []cloudlease.DiskPlan{{Role: "data", CountPerHost: 1, SizeBytes: 500 << 30, Class: "essd", PerformanceLevel: "PL0"}},
			},
			{
				Role: "load", Count: 1, Compute: compute, SystemDisk: system,
				DataDisks:  []cloudlease.DiskPlan{{Role: "data", CountPerHost: 1, SizeBytes: 200 << 30, Class: "essd", PerformanceLevel: "PL0"}},
				PublicIPv4: true, InternetEgress: true, PeakBandwidthMbps: 20,
			},
		},
	}
}

type fakeReadAPI struct {
	accountHash          string
	zones                []Zone
	instanceTypes        []InstanceType
	images               map[string][]Image
	availability         map[string]Availability
	quotas               map[string]VCPUQuota
	eipQuota             EIPQuota
	hostPrices           map[string]int64
	eipPrice             Price
	priceErr             error
	priceRequests        []PriceRequest
	availabilityRequests []AvailabilityRequest
	readCalls            int
	writeCalls           int
}

func completeReadAPI() *fakeReadAPI {
	return &fakeReadAPI{
		accountHash: "sha256:account",
		zones:       []Zone{{ID: "cn-hangzhou-i", SupportsESSD: true}, {ID: "cn-hangzhou-h", SupportsESSD: true}},
		instanceTypes: []InstanceType{
			{ID: "ecs.g8.large", Architecture: "x86_64", VCPUs: 4, MemoryBytes: 8 << 30, FamilyLevel: "EnterpriseLevel"},
			{ID: "ecs.c8.large-cheaper", Architecture: "x86_64", VCPUs: 4, MemoryBytes: 8 << 30, FamilyLevel: "EnterpriseLevel"},
		},
		images: map[string][]Image{
			"ecs.g8.large": {
				{ID: "marketplace-ubuntu", CreationTime: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), Official: false, CloudInit: true, Architecture: "x86_64"},
				{ID: "ubuntu_24_04_x64_20G_alibase_20260601.vhd", CreationTime: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), Official: true, CloudInit: true, Architecture: "x86_64"},
				{ID: "ubuntu_24_04_x64_20G_alibase_20260701.vhd", CreationTime: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), Official: true, CloudInit: true, Architecture: "x86_64"},
			},
			"ecs.c8.large-cheaper": {{ID: "ubuntu_24_04_x64_20G_alibase_20260701.vhd", CreationTime: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), Official: true, CloudInit: true, Architecture: "x86_64"}},
		},
		availability: map[string]Availability{
			offerKey("cn-hangzhou-h", "ecs.g8.large"):         {Instance: true, SystemESSDPL0: true, DataESSDPL0: true},
			offerKey("cn-hangzhou-i", "ecs.g8.large"):         {Instance: true, SystemESSDPL0: true, DataESSDPL0: true},
			offerKey("cn-hangzhou-h", "ecs.c8.large-cheaper"): {Instance: true, SystemESSDPL0: true, DataESSDPL0: true},
			offerKey("cn-hangzhou-i", "ecs.c8.large-cheaper"): {Instance: false, SystemESSDPL0: true, DataESSDPL0: true},
		},
		quotas: map[string]VCPUQuota{
			"cn-hangzhou-h": {Limit: 64, Used: 16},
			"cn-hangzhou-i": {Limit: 64, Used: 16},
		},
		eipQuota: EIPQuota{Limit: 20, Used: 0},
		hostPrices: map[string]int64{
			priceKey("cn-hangzhou-h", "ecs.g8.large", 500):         1_000_000,
			priceKey("cn-hangzhou-h", "ecs.g8.large", 200):         800_000,
			priceKey("cn-hangzhou-i", "ecs.g8.large", 500):         1_100_000,
			priceKey("cn-hangzhou-i", "ecs.g8.large", 200):         900_000,
			priceKey("cn-hangzhou-h", "ecs.c8.large-cheaper", 500): 1_300_000,
			priceKey("cn-hangzhou-h", "ecs.c8.large-cheaper", 200): 1_000_000,
		},
		eipPrice: Price{Currency: "CNY", UnitCostMicros: 800_000},
	}
}

func (f *fakeReadAPI) AccountIDHash(context.Context) (string, error) {
	f.readCalls++
	return f.accountHash, nil
}

func (f *fakeReadAPI) Zones(context.Context, string) ([]Zone, error) {
	f.readCalls++
	return f.zones, nil
}

func (f *fakeReadAPI) InstanceTypes(context.Context, string, int, int64) ([]InstanceType, error) {
	f.readCalls++
	return f.instanceTypes, nil
}

func (f *fakeReadAPI) Images(_ context.Context, _, instanceType string) ([]Image, error) {
	f.readCalls++
	return f.images[instanceType], nil
}

func (f *fakeReadAPI) Availability(_ context.Context, request AvailabilityRequest) (Availability, error) {
	f.readCalls++
	f.availabilityRequests = append(f.availabilityRequests, request)
	return f.availability[offerKey(request.Zone, request.InstanceType)], nil
}

func (f *fakeReadAPI) PostPaidVCPUQuota(_ context.Context, _, zone string) (VCPUQuota, error) {
	f.readCalls++
	return f.quotas[zone], nil
}

func (f *fakeReadAPI) EIPQuota(context.Context, string) (EIPQuota, error) {
	f.readCalls++
	return f.eipQuota, nil
}

func (f *fakeReadAPI) Price(_ context.Context, request PriceRequest) (Price, error) {
	f.readCalls++
	f.priceRequests = append(f.priceRequests, request)
	if f.priceErr != nil {
		return Price{}, f.priceErr
	}
	if request.Kind == PriceKindEIPTraffic {
		return f.eipPrice, nil
	}
	return Price{Currency: "CNY", UnitCostMicros: f.hostPrices[priceKey(request.Zone, request.InstanceType, request.DataDiskGiB)]}, nil
}

func offerKey(zone, instanceType string) string { return zone + "/" + instanceType }

func priceKey(zone, instanceType string, dataDiskGiB int) string {
	return offerKey(zone, instanceType) + "/" + time.Duration(dataDiskGiB).String()
}
