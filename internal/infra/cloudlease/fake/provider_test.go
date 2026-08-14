package fake_test

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/infra/cloudlease/fake"
	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudlease"
)

func TestProviderRunsCompleteIdempotentLeaseLifecycle(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	provider := fake.New(fake.Options{Now: func() time.Time { return now }, EstimatedCostMicros: 4_000_000})
	controller := cloudlease.NewController(provider, func() time.Time { return now })
	plan := lifecyclePlan(now)
	ctx := context.Background()

	if _, err := controller.Inspect(ctx, selectorForPlan(plan, "not-yet-quoted")); !errors.Is(err, cloudlease.ErrLeaseNotFound) {
		t.Fatalf("Inspect(before acquire) error = %v, want ErrLeaseNotFound", err)
	}
	quote, err := controller.Quote(ctx, plan)
	if err != nil {
		t.Fatalf("Quote() error = %v", err)
	}
	first, err := controller.Acquire(ctx, plan, quote)
	if err != nil {
		t.Fatalf("Acquire(first) error = %v", err)
	}
	second, err := controller.Acquire(ctx, plan, quote)
	if err != nil {
		t.Fatalf("Acquire(retry) error = %v", err)
	}
	if first.CreatedAt != second.CreatedAt || len(first.Resources) != len(second.Resources) {
		t.Fatalf("Acquire retry changed inventory: first=%#v second=%#v", first, second)
	}
	if len(first.Resources) != 5 {
		t.Fatalf("Acquire() resources = %d, want network+compute+2 disks+address", len(first.Resources))
	}
	for _, key := range cloudlease.MandatoryBaseTagKeys() {
		if first.Tags[key] == "" {
			t.Fatalf("Acquire() receipt missing mandatory tag %q", key)
		}
		for _, resource := range first.Resources {
			if resource.Tags[key] != first.Tags[key] {
				t.Fatalf("resource %q tag %q = %q, want %q", resource.ID, key, resource.Tags[key], first.Tags[key])
			}
		}
	}
	for _, resource := range first.Resources {
		if resource.Tags[cloudlease.TagResourceRole] != resource.Role {
			t.Fatalf("resource %q role tag = %q, want %q", resource.ID, resource.Tags[cloudlease.TagResourceRole], resource.Role)
		}
	}
	if first.Tags[cloudlease.TagSourceSHA] != plan.Provenance.SourceSHA ||
		first.Tags[cloudlease.TagBundleDigest] != plan.Provenance.BundleDigest {
		t.Fatalf("Acquire() provenance tags = %#v, want Plan provenance", first.Tags)
	}
	for _, resource := range first.Resources {
		if resource.Tags[cloudlease.TagSourceSHA] != plan.Provenance.SourceSHA ||
			resource.Tags[cloudlease.TagBundleDigest] != plan.Provenance.BundleDigest {
			t.Fatalf("resource %q provenance tags = %#v, want Plan provenance", resource.ID, resource.Tags)
		}
	}

	selector := selectorForPlan(plan, quote.PlanDigest)
	inspected, err := controller.Inspect(ctx, selector)
	if err != nil {
		t.Fatalf("Inspect(active) error = %v", err)
	}
	if inspected.State != cloudlease.StateActive || len(inspected.AccessGrants) != 1 {
		t.Fatalf("Inspect(active) = %#v, want active with initial HTTP grant", inspected)
	}

	ssh := cloudlease.AccessGrant{
		ID: "ssh", TargetRole: "load", Protocol: cloudlease.ProtocolTCP,
		PortFrom: 22, PortTo: 22, SourcePrefix: netip.MustParsePrefix("0.0.0.0/0"),
		Until: plan.ExpiresAt,
	}
	granted, err := controller.GrantAccess(ctx, selector, ssh)
	if err != nil {
		t.Fatalf("GrantAccess() error = %v", err)
	}
	if len(granted.AccessGrants) != 2 {
		t.Fatalf("GrantAccess() grants = %d, want 2", len(granted.AccessGrants))
	}
	grantedAgain, err := controller.GrantAccess(ctx, selector, ssh)
	if err != nil {
		t.Fatalf("GrantAccess(retry) error = %v", err)
	}
	if len(grantedAgain.AccessGrants) != 2 {
		t.Fatalf("GrantAccess(retry) grants = %d, want 2", len(grantedAgain.AccessGrants))
	}

	revoked, err := controller.RevokeAccess(ctx, selector, ssh.ID)
	if err != nil {
		t.Fatalf("RevokeAccess() error = %v", err)
	}
	if len(revoked.AccessGrants) != 1 {
		t.Fatalf("RevokeAccess() grants = %d, want initial grant only", len(revoked.AccessGrants))
	}
	released, err := controller.Release(ctx, selector)
	if err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if released.ZeroInventory == nil || released.Receipt != nil {
		t.Fatalf("Release() = %#v, want zero-inventory proof", released)
	}
	releasedAgain, err := controller.Release(ctx, selector)
	if err != nil {
		t.Fatalf("Release(retry) error = %v", err)
	}
	if releasedAgain.ZeroInventory == nil || releasedAgain.Receipt != nil {
		t.Fatalf("Release(retry) = %#v, want repeated zero-inventory proof", releasedAgain)
	}
	if _, err := controller.Inspect(ctx, selector); !errors.Is(err, cloudlease.ErrLeaseNotFound) {
		t.Fatalf("Inspect(after release) error = %v, want ErrLeaseNotFound", err)
	}
	if _, err := controller.Acquire(ctx, plan, quote); !errors.Is(err, cloudlease.ErrLeaseReleased) {
		t.Fatalf("Acquire(after release) error = %v, want ErrLeaseReleased", err)
	}

	sweep, err := controller.Sweep(ctx, cloudlease.SweepRequest{Repository: plan.Repository})
	if err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}
	if sweep.Examined != 0 || len(sweep.Released) != 0 || len(sweep.Pending) != 0 || len(sweep.Failed) != 0 {
		t.Fatalf("Sweep() = %#v, want no retained tombstone", sweep)
	}
}

func TestProviderReturnsDetachedQuoteAndReceiptState(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	provider := fake.New(fake.Options{Now: func() time.Time { return now }, EstimatedCostMicros: 4_000_000})
	controller := cloudlease.NewController(provider, func() time.Time { return now })
	plan := lifecyclePlan(now)
	ctx := context.Background()

	firstQuote, err := controller.Quote(ctx, plan)
	if err != nil {
		t.Fatalf("Quote(first) error = %v", err)
	}
	firstQuote.LineItems[0].CostMicros = -1
	firstQuote.Selection["zone"] = "mutated"
	quote, err := controller.Quote(ctx, plan)
	if err != nil {
		t.Fatalf("Quote(second) error = %v", err)
	}
	if quote.LineItems[0].CostMicros != 4_000_000 || quote.Selection["zone"] != "fake-zone-a" {
		t.Fatalf("Quote(second) shares caller mutation: %#v", quote)
	}

	receipt, err := controller.Acquire(ctx, plan, quote)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	selector := selectorForPlan(plan, quote.PlanDigest)
	wantResourceID := receipt.Resources[0].ID
	wantResourceTag := receipt.Resources[0].Tags[cloudlease.TagLeaseID]
	wantGrantID := receipt.AccessGrants[0].ID

	receipt.Tags[cloudlease.TagLeaseID] = "mutated"
	receipt.Resources[0].ID = "mutated"
	receipt.Resources[0].Tags[cloudlease.TagLeaseID] = "mutated"
	receipt.AccessGrants[0].ID = "mutated"
	receipt.Quote.LineItems[0].CostMicros = -1
	receipt.Quote.Selection["zone"] = "mutated"

	inspected, err := provider.Inspect(ctx, selector)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if inspected.Tags[cloudlease.TagLeaseID] != plan.LeaseID ||
		inspected.Resources[0].ID != wantResourceID ||
		inspected.Resources[0].Tags[cloudlease.TagLeaseID] != wantResourceTag ||
		inspected.AccessGrants[0].ID != wantGrantID ||
		inspected.Quote.LineItems[0].CostMicros != 4_000_000 ||
		inspected.Quote.Selection["zone"] != "fake-zone-a" {
		t.Fatalf("Inspect() observed caller mutation: %#v", inspected)
	}
}

func TestProviderRetainsPartialAcquireForExactCleanup(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	provider := fake.New(fake.Options{
		Now: func() time.Time { return now },
		Failures: fake.FailurePlan{
			AcquireAfterResources: 2,
		},
	})
	controller := cloudlease.NewController(provider, func() time.Time { return now })
	plan := lifecyclePlan(now)
	ctx := context.Background()

	quote, err := controller.Quote(ctx, plan)
	if err != nil {
		t.Fatalf("Quote() error = %v", err)
	}
	partial, err := controller.Acquire(ctx, plan, quote)
	if !errors.Is(err, cloudlease.ErrAcquireIncomplete) {
		t.Fatalf("Acquire() error = %v, want ErrAcquireIncomplete", err)
	}
	if partial.State != cloudlease.StateReleasePending || len(partial.Resources) != 2 {
		t.Fatalf("Acquire() receipt = %#v, want two retained partial resources", partial)
	}

	released, err := controller.Release(ctx, selectorForPlan(plan, quote.PlanDigest))
	if err != nil {
		t.Fatalf("Release(partial) error = %v", err)
	}
	if released.ZeroInventory == nil || released.Receipt != nil {
		t.Fatalf("Release(partial) = %#v, want zero-inventory proof", released)
	}
}

func TestProviderLetsControllerRecoverAmbiguousAcquireAndRelease(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	provider := fake.New(fake.Options{
		Now: func() time.Time { return now },
		Failures: fake.FailurePlan{
			AcquireAmbiguous: true,
			ReleaseAmbiguous: true,
		},
	})
	controller := cloudlease.NewController(provider, func() time.Time { return now })
	plan := lifecyclePlan(now)
	ctx := context.Background()

	quote, err := controller.Quote(ctx, plan)
	if err != nil {
		t.Fatalf("Quote() error = %v", err)
	}
	acquired, err := controller.Acquire(ctx, plan, quote)
	if err != nil {
		t.Fatalf("Acquire(ambiguous) error = %v", err)
	}
	if acquired.State != cloudlease.StateActive {
		t.Fatalf("Acquire(ambiguous) state = %q, want active", acquired.State)
	}
	released, err := controller.Release(ctx, selectorForPlan(plan, quote.PlanDigest))
	if err != nil {
		t.Fatalf("Release(ambiguous) error = %v", err)
	}
	if released.ZeroInventory == nil || released.Receipt != nil {
		t.Fatalf("Release(ambiguous) = %#v, want zero-inventory proof", released)
	}
}

func TestProviderRejectsExpiryExtensionForExistingLeaseIdentity(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	provider := fake.New(fake.Options{Now: func() time.Time { return now }})
	controller := cloudlease.NewController(provider, func() time.Time { return now })
	plan := lifecyclePlan(now)
	ctx := context.Background()

	quote, err := controller.Quote(ctx, plan)
	if err != nil {
		t.Fatalf("Quote(original) error = %v", err)
	}
	if _, err := controller.Acquire(ctx, plan, quote); err != nil {
		t.Fatalf("Acquire(original) error = %v", err)
	}
	extended := plan
	extended.ExpiresAt = plan.ExpiresAt.Add(time.Hour)
	extended.Network.InitialAccess = append([]cloudlease.AccessGrant(nil), plan.Network.InitialAccess...)
	extended.Network.InitialAccess[0].Until = extended.ExpiresAt
	extendedQuote, err := controller.Quote(ctx, extended)
	if err != nil {
		t.Fatalf("Quote(extended) error = %v", err)
	}

	_, err = controller.Acquire(ctx, extended, extendedQuote)
	if !errors.Is(err, cloudlease.ErrLeaseConflict) {
		t.Fatalf("Acquire(extended) error = %v, want ErrLeaseConflict", err)
	}
}

func TestProviderAllowsDistinctStageLeasesWithinOneRequest(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	provider := fake.New(fake.Options{Now: func() time.Time { return now }})
	controller := cloudlease.NewController(provider, func() time.Time { return now })
	ctx := context.Background()
	firstPlan := lifecyclePlan(now)
	secondPlan := lifecyclePlan(now)
	secondPlan.LeaseID = "lease-2"

	for _, plan := range []cloudlease.Plan{firstPlan, secondPlan} {
		quote, err := controller.Quote(ctx, plan)
		if err != nil {
			t.Fatalf("Quote(%s) error = %v", plan.LeaseID, err)
		}
		if _, err := controller.Acquire(ctx, plan, quote); err != nil {
			t.Fatalf("Acquire(%s) error = %v", plan.LeaseID, err)
		}
	}
	receipts, err := provider.List(ctx, cloudlease.InventoryFilter{Repository: firstPlan.Repository})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(receipts) != 2 || receipts[0].RequestID != receipts[1].RequestID || receipts[0].LeaseID == receipts[1].LeaseID {
		t.Fatalf("List() = %#v, want two stage Leases under one Request", receipts)
	}
}

func TestProviderSweepPrioritizesExpiredLeaseCleanupAfterAccessFailure(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	current := now
	provider := fake.New(fake.Options{
		Now: func() time.Time { return current },
		Failures: fake.FailurePlan{
			Access: true,
		},
	})
	controller := cloudlease.NewController(provider, func() time.Time { return current })
	plan := lifecyclePlan(now)
	ctx := context.Background()
	quote, err := controller.Quote(ctx, plan)
	if err != nil {
		t.Fatalf("Quote() error = %v", err)
	}
	if _, err := controller.Acquire(ctx, plan, quote); err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	current = plan.ExpiresAt

	result, err := controller.Sweep(ctx, cloudlease.SweepRequest{Repository: plan.Repository})
	if err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}
	if len(result.Failed) != 1 || result.Failed[0].Reason != "revoke_access" {
		t.Fatalf("Sweep() failures = %#v, want access failure", result.Failed)
	}
	if len(result.Released) != 1 || result.Released[0] != plan.LeaseID {
		t.Fatalf("Sweep() released = %#v, want cleanup despite access failure", result.Released)
	}
}

func TestProviderSweepRetriesResidualReleaseUntilZero(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	current := now
	provider := fake.New(fake.Options{
		Now: func() time.Time { return current },
		Failures: fake.FailurePlan{
			ReleaseResidualAttempts: map[string]int{"lease-1": 1},
		},
	})
	controller := cloudlease.NewController(provider, func() time.Time { return current })
	plan := lifecyclePlan(now)
	ctx := context.Background()
	quote, err := controller.Quote(ctx, plan)
	if err != nil {
		t.Fatalf("Quote() error = %v", err)
	}
	if _, err := controller.Acquire(ctx, plan, quote); err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	current = plan.ExpiresAt

	first, err := controller.Sweep(ctx, cloudlease.SweepRequest{Repository: plan.Repository})
	if err != nil {
		t.Fatalf("Sweep(first) error = %v", err)
	}
	if len(first.Pending) != 1 || first.Pending[0] != plan.LeaseID {
		t.Fatalf("Sweep(first) = %#v, want cleanup pending", first)
	}
	second, err := controller.Sweep(ctx, cloudlease.SweepRequest{Repository: plan.Repository})
	if err != nil {
		t.Fatalf("Sweep(second) error = %v", err)
	}
	if len(second.Released) != 1 || second.Released[0] != plan.LeaseID || len(second.Pending) != 0 {
		t.Fatalf("Sweep(second) = %#v, want released", second)
	}
	_, err = controller.Inspect(ctx, selectorForPlan(plan, quote.PlanDigest))
	if !errors.Is(err, cloudlease.ErrLeaseNotFound) {
		t.Fatalf("Inspect(released) error = %v, want ErrLeaseNotFound", err)
	}
}

func TestProviderReleasesAccessAndDependentResourcesBeforeNetwork(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	provider := fake.New(fake.Options{
		Now: func() time.Time { return now },
		Failures: fake.FailurePlan{
			ReleaseResidualAttempts: map[string]int{"lease-1": 5},
		},
	})
	controller := cloudlease.NewController(provider, func() time.Time { return now })
	plan := lifecyclePlan(now)
	ctx := context.Background()
	quote, err := controller.Quote(ctx, plan)
	if err != nil {
		t.Fatalf("Quote() error = %v", err)
	}
	if _, err := controller.Acquire(ctx, plan, quote); err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	selector := selectorForPlan(plan, quote.PlanDigest)

	wantResourceCounts := []int{5, 4, 3, 2, 1}
	for index, want := range wantResourceCounts {
		result, releaseErr := controller.Release(ctx, selector)
		if !errors.Is(releaseErr, cloudlease.ErrResidualResources) {
			t.Fatalf("Release(%d) error = %v, want ErrResidualResources", index+1, releaseErr)
		}
		if result.Receipt == nil || len(result.Receipt.Resources) != want || len(result.Receipt.AccessGrants) != 0 {
			t.Fatalf("Release(%d) = %#v, want %d resources and no access", index+1, result, want)
		}
		if want == 1 && result.Receipt.Resources[0].Kind != "network" {
			t.Fatalf("Release(%d) final residual = %#v, want network last", index+1, result.Receipt.Resources)
		}
	}
	result, err := controller.Release(ctx, selector)
	if err != nil || result.ZeroInventory == nil {
		t.Fatalf("Release(final) = %#v, %v, want zero-inventory proof", result, err)
	}
}

func lifecyclePlan(now time.Time) cloudlease.Plan {
	return cloudlease.Plan{
		Schema: cloudlease.PlanSchemaV1, LeaseID: "lease-1", RequestID: "request-1",
		Provider: fake.ProviderName, Region: "fake-region", Repository: "WuKongIM/WuKongIM",
		Operator: "tester", ExpiresAt: now.Add(2 * time.Hour),
		Budget: cloudlease.Budget{Currency: "CNY", LimitMicros: 10_000_000, CommittedMicros: 1_000_000},
		Provenance: cloudlease.Provenance{
			SourceSHA:    strings.Repeat("a", 40),
			BundleDigest: strings.Repeat("b", 64),
		},
		Network: cloudlease.NetworkPlan{
			Isolated: true, SingleZone: true,
			InitialAccess: []cloudlease.AccessGrant{{
				ID: "http", TargetRole: "load", Protocol: cloudlease.ProtocolTCP,
				PortFrom: 80, PortTo: 80, SourcePrefix: netip.MustParsePrefix("0.0.0.0/0"),
				Until: now.Add(2 * time.Hour),
			}},
		},
		HostGroups: []cloudlease.HostGroupPlan{{
			Role: "load", Count: 1,
			Compute: cloudlease.ComputePlan{
				VCPUs: 4, MemoryBytes: 8 << 30, Architecture: "x86_64", BillingModel: "postpaid",
			},
			SystemDisk: cloudlease.DiskPlan{Role: "system", SizeBytes: 40 << 30, Class: "ssd"},
			DataDisks:  []cloudlease.DiskPlan{{Role: "data", SizeBytes: 200 << 30, Class: "ssd"}},
			PublicIPv4: true, InternetEgress: true, PeakBandwidthMbps: 20,
		}},
		Tags: map[string]string{"purpose": "lifecycle-test"},
	}
}

func selectorForPlan(plan cloudlease.Plan, digest string) cloudlease.Selector {
	return cloudlease.Selector{
		LeaseID: plan.LeaseID, RequestID: plan.RequestID, Provider: plan.Provider,
		Region: plan.Region, Repository: plan.Repository, PlanDigest: digest,
	}
}
