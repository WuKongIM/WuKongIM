package cloudlease_test

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudlease"
)

func TestControllerQuoteAdmitsPlanWithinRemainingBudgetWithoutMutation(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	provider := &recordingProvider{
		quote: cloudlease.Quote{
			LeaseID:             "lease-1",
			RequestID:           "request-1",
			Provider:            "fake",
			Region:              "test-region",
			Zone:                "test-zone-a",
			Currency:            "CNY",
			EstimatedCostMicros: 7_000_000,
			CapacityAvailable:   true,
			QuotaAvailable:      true,
			QuotedAt:            now,
			ValidUntil:          now.Add(10 * time.Minute),
		},
	}
	controller := cloudlease.NewController(provider, func() time.Time { return now })

	quote, err := controller.Quote(context.Background(), validPlan(now))
	if err != nil {
		t.Fatalf("Quote() error = %v", err)
	}
	if quote.PlanDigest == "" {
		t.Fatal("Quote().PlanDigest is empty")
	}
	if quote.EstimatedCostMicros != 7_000_000 {
		t.Fatalf("Quote().EstimatedCostMicros = %d, want 7000000", quote.EstimatedCostMicros)
	}
	if provider.quoteCalls != 1 {
		t.Fatalf("provider Quote calls = %d, want 1", provider.quoteCalls)
	}
	if provider.mutationCalls != 0 {
		t.Fatalf("provider mutation calls = %d, want 0", provider.mutationCalls)
	}
}

func TestControllerQuoteRejectsNonCanonicalTagsBeforeProviderCall(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	provider := &recordingProvider{}
	controller := cloudlease.NewController(provider, func() time.Time { return now })
	plan := validPlan(now)
	plan.Tags = map[string]string{" purpose ": "unit-test"}

	_, err := controller.Quote(context.Background(), plan)
	if !errors.Is(err, cloudlease.ErrInvalidPlan) {
		t.Fatalf("Quote() error = %v, want ErrInvalidPlan", err)
	}
	if provider.quoteCalls != 0 {
		t.Fatalf("provider Quote calls = %d, want 0", provider.quoteCalls)
	}
}

func TestControllerQuoteRejectsNonCanonicalCurrencyBeforeProviderCall(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	provider := &recordingProvider{}
	controller := cloudlease.NewController(provider, func() time.Time { return now })
	plan := validPlan(now)
	plan.Budget.Currency = "cny"

	_, err := controller.Quote(context.Background(), plan)
	if !errors.Is(err, cloudlease.ErrInvalidPlan) {
		t.Fatalf("Quote() error = %v, want ErrInvalidPlan", err)
	}
	if provider.quoteCalls != 0 {
		t.Fatalf("provider Quote calls = %d, want 0", provider.quoteCalls)
	}
}

func TestControllerQuoteRejectsInvalidProvenanceBeforeProviderCall(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	provider := &recordingProvider{}
	controller := cloudlease.NewController(provider, func() time.Time { return now })
	plan := validPlan(now)
	plan.Provenance.SourceSHA = "moving-main"

	_, err := controller.Quote(context.Background(), plan)
	if !errors.Is(err, cloudlease.ErrInvalidPlan) {
		t.Fatalf("Quote() error = %v, want ErrInvalidPlan", err)
	}
	if provider.quoteCalls != 0 {
		t.Fatalf("provider Quote calls = %d, want 0", provider.quoteCalls)
	}
}

func TestControllerQuoteRejectsPublicEgressWithoutPublicAddress(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	provider := &recordingProvider{}
	controller := cloudlease.NewController(provider, func() time.Time { return now })
	plan := validPlan(now)
	plan.Network.ConservativePublicEgressBytes = 10 << 30

	_, err := controller.Quote(context.Background(), plan)
	if !errors.Is(err, cloudlease.ErrInvalidPlan) {
		t.Fatalf("Quote() error = %v, want ErrInvalidPlan", err)
	}
	if provider.quoteCalls != 0 {
		t.Fatalf("provider Quote calls = %d, want 0", provider.quoteCalls)
	}
}

func TestControllerQuoteEnforcesAdmissionSignalsAndRemainingBudget(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	base := cloudlease.Quote{
		LeaseID: "lease-1", RequestID: "request-1", Provider: "fake",
		Region: "test-region", Zone: "test-zone-a", Currency: "CNY",
		EstimatedCostMicros: 7_000_000, CapacityAvailable: true, QuotaAvailable: true,
		QuotedAt: now, ValidUntil: now.Add(10 * time.Minute),
	}
	tests := []struct {
		name   string
		change func(*cloudlease.Quote)
		want   error
	}{
		{
			name: "cost above remaining aggregate budget",
			change: func(quote *cloudlease.Quote) {
				quote.EstimatedCostMicros = 8_000_001
			},
			want: cloudlease.ErrCostLimitExceeded,
		},
		{
			name: "capacity unavailable",
			change: func(quote *cloudlease.Quote) {
				quote.CapacityAvailable = false
			},
			want: cloudlease.ErrCapacityUnavailable,
		},
		{
			name: "quota unavailable",
			change: func(quote *cloudlease.Quote) {
				quote.QuotaAvailable = false
			},
			want: cloudlease.ErrQuotaUnavailable,
		},
		{
			name: "expired quote",
			change: func(quote *cloudlease.Quote) {
				quote.ValidUntil = now
			},
			want: cloudlease.ErrInvalidQuote,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			quote := base
			test.change(&quote)
			provider := &recordingProvider{quote: quote}
			controller := cloudlease.NewController(provider, func() time.Time { return now })

			_, err := controller.Quote(context.Background(), validPlan(now))
			if !errors.Is(err, test.want) {
				t.Fatalf("Quote() error = %v, want %v", err, test.want)
			}
			if provider.mutationCalls != 0 {
				t.Fatalf("provider mutation calls = %d, want 0", provider.mutationCalls)
			}
		})
	}
}

func TestControllerAcquireReturnsExistingMatchingLeaseWithoutMutation(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	provider := &recordingProvider{
		quote: cloudlease.Quote{
			LeaseID:             "lease-1",
			RequestID:           "request-1",
			Provider:            "fake",
			Region:              "test-region",
			Zone:                "test-zone-a",
			Currency:            "CNY",
			EstimatedCostMicros: 7_000_000,
			CapacityAvailable:   true,
			QuotaAvailable:      true,
			QuotedAt:            now,
			ValidUntil:          now.Add(10 * time.Minute),
		},
		inspectErr: cloudlease.ErrLeaseNotFound,
	}
	controller := cloudlease.NewController(provider, func() time.Time { return now })
	plan := validPlan(now)
	quote, err := controller.Quote(context.Background(), plan)
	if err != nil {
		t.Fatalf("Quote() error = %v", err)
	}
	provider.inspect = activeReceipt(plan, quote, now)
	provider.inspectErr = nil

	receipt, err := controller.Acquire(context.Background(), plan, quote)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if receipt.LeaseID != plan.LeaseID || receipt.PlanDigest != quote.PlanDigest {
		t.Fatalf("Acquire() receipt = %#v, want matching existing Lease", receipt)
	}
	if provider.acquireCalls != 0 {
		t.Fatalf("provider Acquire calls = %d, want 0", provider.acquireCalls)
	}
}

func TestControllerAcquireReconstructsExistingLeaseAfterQuoteExpires(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	current := now.Add(-20 * time.Minute)
	plan := validPlan(now)
	provider := &recordingProvider{quote: cloudlease.Quote{
		LeaseID: plan.LeaseID, RequestID: plan.RequestID, Provider: plan.Provider,
		Region: plan.Region, Zone: "test-zone-a",
		Currency: "CNY", EstimatedCostMicros: 7_000_000,
		CapacityAvailable: true, QuotaAvailable: true,
		QuotedAt: current, ValidUntil: current.Add(10 * time.Minute),
	}}
	controller := cloudlease.NewController(provider, func() time.Time { return current })
	quote, err := controller.Quote(context.Background(), plan)
	if err != nil {
		t.Fatalf("Quote() error = %v", err)
	}
	provider.inspect = activeReceipt(plan, quote, now.Add(-time.Hour))
	current = now

	receipt, err := controller.Acquire(context.Background(), plan, quote)
	if err != nil {
		t.Fatalf("Acquire(existing with expired quote) error = %v", err)
	}
	if receipt.State != cloudlease.StateActive || provider.acquireCalls != 0 {
		t.Fatalf("Acquire(existing with expired quote) = %#v, acquire calls = %d", receipt, provider.acquireCalls)
	}
}

func TestControllerAcquireReportsMatchingPartialInventoryAsIncomplete(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	provider := &recordingProvider{
		quote: cloudlease.Quote{
			LeaseID:             "lease-1",
			RequestID:           "request-1",
			Provider:            "fake",
			Region:              "test-region",
			Zone:                "test-zone-a",
			Currency:            "CNY",
			EstimatedCostMicros: 7_000_000,
			CapacityAvailable:   true,
			QuotaAvailable:      true,
			QuotedAt:            now,
			ValidUntil:          now.Add(10 * time.Minute),
		},
		inspectErr: cloudlease.ErrLeaseNotFound,
	}
	controller := cloudlease.NewController(provider, func() time.Time { return now })
	plan := validPlan(now)
	quote, err := controller.Quote(context.Background(), plan)
	if err != nil {
		t.Fatalf("Quote() error = %v", err)
	}
	provider.acquire = activeReceipt(plan, quote, now)
	provider.acquire.State = cloudlease.StateAcquiring

	receipt, err := controller.Acquire(context.Background(), plan, quote)
	if !errors.Is(err, cloudlease.ErrAcquireIncomplete) {
		t.Fatalf("Acquire() error = %v, want ErrAcquireIncomplete", err)
	}
	if receipt.State != cloudlease.StateAcquiring || len(receipt.Resources) == 0 {
		t.Fatalf("Acquire() receipt = %#v, want retained partial inventory", receipt)
	}
}

func TestControllerInspectRejectsProviderIdentityMismatch(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	plan := validPlan(now)
	quote := cloudlease.Quote{
		LeaseID: plan.LeaseID, RequestID: plan.RequestID, Provider: plan.Provider,
		Region: plan.Region, Zone: "test-zone-a", PlanDigest: "digest-1",
		Currency: "CNY", EstimatedCostMicros: 7_000_000,
		CapacityAvailable: true, QuotaAvailable: true,
		QuotedAt: now, ValidUntil: now.Add(10 * time.Minute),
	}
	provider := &recordingProvider{inspect: activeReceipt(plan, quote, now)}
	provider.inspect.RequestID = "another-request"
	controller := cloudlease.NewController(provider, func() time.Time { return now })

	_, err := controller.Inspect(context.Background(), cloudlease.Selector{
		LeaseID: plan.LeaseID, RequestID: plan.RequestID, Provider: plan.Provider,
		Region: plan.Region, Repository: plan.Repository, PlanDigest: quote.PlanDigest,
	})
	if !errors.Is(err, cloudlease.ErrProviderInvariant) {
		t.Fatalf("Inspect() error = %v, want ErrProviderInvariant", err)
	}
}

func TestControllerInspectRejectsCorruptAccountAndAccessInventory(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	plan := validPlan(now)
	quote := cloudlease.Quote{
		LeaseID: plan.LeaseID, RequestID: plan.RequestID, Provider: plan.Provider,
		Region: plan.Region, Zone: "test-zone-a", AccountIDHash: "account-a", PlanDigest: "digest-1",
		Currency: "CNY", EstimatedCostMicros: 7_000_000,
		CapacityAvailable: true, QuotaAvailable: true,
		QuotedAt: now, ValidUntil: now.Add(10 * time.Minute),
	}
	selector := cloudlease.Selector{
		LeaseID: plan.LeaseID, RequestID: plan.RequestID, Provider: plan.Provider,
		Region: plan.Region, Repository: plan.Repository, PlanDigest: quote.PlanDigest,
	}
	tests := []struct {
		name   string
		change func(*cloudlease.Receipt)
	}{
		{
			name: "account does not match admitted quote",
			change: func(receipt *cloudlease.Receipt) {
				receipt.AccountIDHash = "account-b"
			},
		},
		{
			name: "stored access has non-canonical prefix",
			change: func(receipt *cloudlease.Receipt) {
				receipt.AccessGrants = []cloudlease.AccessGrant{{
					ID: "ssh", TargetRole: "worker", Protocol: cloudlease.ProtocolTCP,
					PortFrom: 22, PortTo: 22, SourcePrefix: netip.MustParsePrefix("203.0.113.8/24"),
					Until: plan.ExpiresAt,
				}}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			receipt := activeReceipt(plan, quote, now)
			test.change(&receipt)
			provider := &recordingProvider{inspect: receipt}
			controller := cloudlease.NewController(provider, func() time.Time { return now })

			_, err := controller.Inspect(context.Background(), selector)
			if !errors.Is(err, cloudlease.ErrProviderInvariant) {
				t.Fatalf("Inspect() error = %v, want ErrProviderInvariant", err)
			}
		})
	}
}

func TestControllerGrantAccessReturnsExistingExactGrantWithoutMutation(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	plan := validPlan(now)
	quote := cloudlease.Quote{
		LeaseID: plan.LeaseID, RequestID: plan.RequestID, Provider: plan.Provider,
		Region: plan.Region, Zone: "test-zone-a", PlanDigest: "digest-1",
		Currency: "CNY", EstimatedCostMicros: 7_000_000,
		CapacityAvailable: true, QuotaAvailable: true,
		QuotedAt: now, ValidUntil: now.Add(10 * time.Minute),
	}
	grant := cloudlease.AccessGrant{
		ID: "ssh-public", TargetRole: "worker", Protocol: cloudlease.ProtocolTCP,
		PortFrom: 22, PortTo: 22, SourcePrefix: netip.MustParsePrefix("0.0.0.0/0"),
		Until: plan.ExpiresAt,
	}
	provider := &recordingProvider{inspect: activeReceipt(plan, quote, now)}
	provider.inspect.AccessGrants = []cloudlease.AccessGrant{grant}
	controller := cloudlease.NewController(provider, func() time.Time { return now })
	selector := cloudlease.Selector{
		LeaseID: plan.LeaseID, RequestID: plan.RequestID, Provider: plan.Provider,
		Region: plan.Region, Repository: plan.Repository, PlanDigest: quote.PlanDigest,
	}

	receipt, err := controller.GrantAccess(context.Background(), selector, grant)
	if err != nil {
		t.Fatalf("GrantAccess() error = %v", err)
	}
	if len(receipt.AccessGrants) != 1 || receipt.AccessGrants[0].ID != grant.ID {
		t.Fatalf("GrantAccess() grants = %#v, want existing exact grant", receipt.AccessGrants)
	}
	if provider.mutationCalls != 0 {
		t.Fatalf("provider mutation calls = %d, want 0", provider.mutationCalls)
	}
}

func TestControllerGrantAccessRejectsUnmaskedPrefixBeforeMutation(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	plan := validPlan(now)
	quote := cloudlease.Quote{
		LeaseID: plan.LeaseID, RequestID: plan.RequestID, Provider: plan.Provider,
		Region: plan.Region, Zone: "test-zone-a", PlanDigest: "digest-1",
		Currency: "CNY", EstimatedCostMicros: 7_000_000,
		CapacityAvailable: true, QuotaAvailable: true,
		QuotedAt: now, ValidUntil: now.Add(10 * time.Minute),
	}
	provider := &recordingProvider{inspect: activeReceipt(plan, quote, now)}
	controller := cloudlease.NewController(provider, func() time.Time { return now })
	selector := cloudlease.Selector{
		LeaseID: plan.LeaseID, RequestID: plan.RequestID, Provider: plan.Provider,
		Region: plan.Region, Repository: plan.Repository, PlanDigest: quote.PlanDigest,
	}
	grant := cloudlease.AccessGrant{
		ID: "bad-prefix", TargetRole: "worker", Protocol: cloudlease.ProtocolTCP,
		PortFrom: 22, PortTo: 22, SourcePrefix: netip.MustParsePrefix("203.0.113.8/24"),
		Until: plan.ExpiresAt,
	}

	_, err := controller.GrantAccess(context.Background(), selector, grant)
	if !errors.Is(err, cloudlease.ErrInvalidAccess) {
		t.Fatalf("GrantAccess() error = %v, want ErrInvalidAccess", err)
	}
	if provider.mutationCalls != 0 {
		t.Fatalf("provider mutation calls = %d, want 0", provider.mutationCalls)
	}
}

func TestControllerRevokeAccessIsIdempotentWhenGrantIsAbsent(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	plan := validPlan(now)
	quote := cloudlease.Quote{
		LeaseID: plan.LeaseID, RequestID: plan.RequestID, Provider: plan.Provider,
		Region: plan.Region, Zone: "test-zone-a", PlanDigest: "digest-1",
		Currency: "CNY", EstimatedCostMicros: 7_000_000,
		CapacityAvailable: true, QuotaAvailable: true,
		QuotedAt: now, ValidUntil: now.Add(10 * time.Minute),
	}
	provider := &recordingProvider{inspect: activeReceipt(plan, quote, now)}
	controller := cloudlease.NewController(provider, func() time.Time { return now })
	selector := cloudlease.Selector{
		LeaseID: plan.LeaseID, RequestID: plan.RequestID, Provider: plan.Provider,
		Region: plan.Region, Repository: plan.Repository, PlanDigest: quote.PlanDigest,
	}

	receipt, err := controller.RevokeAccess(context.Background(), selector, "missing-grant")
	if err != nil {
		t.Fatalf("RevokeAccess() error = %v", err)
	}
	if len(receipt.AccessGrants) != 0 {
		t.Fatalf("RevokeAccess() grants = %#v, want empty", receipt.AccessGrants)
	}
	if provider.mutationCalls != 0 {
		t.Fatalf("provider mutation calls = %d, want 0", provider.mutationCalls)
	}
}

func TestControllerReleaseReportsResidualInventoryUntilZero(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	plan := validPlan(now)
	quote := cloudlease.Quote{
		LeaseID: plan.LeaseID, RequestID: plan.RequestID, Provider: plan.Provider,
		Region: plan.Region, Zone: "test-zone-a", PlanDigest: "digest-1",
		Currency: "CNY", EstimatedCostMicros: 7_000_000,
		CapacityAvailable: true, QuotaAvailable: true,
		QuotedAt: now, ValidUntil: now.Add(10 * time.Minute),
	}
	active := activeReceipt(plan, quote, now)
	pending := active
	pending.State = cloudlease.StateReleasePending
	provider := &recordingProvider{inspect: active, release: cloudlease.ReleaseResult{Receipt: &pending}}
	controller := cloudlease.NewController(provider, func() time.Time { return now })
	selector := cloudlease.Selector{
		LeaseID: plan.LeaseID, RequestID: plan.RequestID, Provider: plan.Provider,
		Region: plan.Region, Repository: plan.Repository, PlanDigest: quote.PlanDigest,
	}

	result, err := controller.Release(context.Background(), selector)
	if !errors.Is(err, cloudlease.ErrResidualResources) {
		t.Fatalf("Release() error = %v, want ErrResidualResources", err)
	}
	if result.Receipt == nil || result.Receipt.State != cloudlease.StateReleasePending || len(result.Receipt.Resources) == 0 {
		t.Fatalf("Release() result = %#v, want residual inventory", result)
	}
}

func TestControllerSweepRevokesExpiredAccessOnActiveLease(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	plan := validPlan(now)
	quote := cloudlease.Quote{
		LeaseID: plan.LeaseID, RequestID: plan.RequestID, Provider: plan.Provider,
		Region: plan.Region, Zone: "test-zone-a", PlanDigest: "digest-1",
		Currency: "CNY", EstimatedCostMicros: 7_000_000,
		CapacityAvailable: true, QuotaAvailable: true,
		QuotedAt: now, ValidUntil: now.Add(10 * time.Minute),
	}
	expiredGrant := cloudlease.AccessGrant{
		ID: "expired-ssh", TargetRole: "worker", Protocol: cloudlease.ProtocolTCP,
		PortFrom: 22, PortTo: 22, SourcePrefix: netip.MustParsePrefix("0.0.0.0/0"),
		Until: now,
	}
	active := activeReceipt(plan, quote, now)
	active.AccessGrants = []cloudlease.AccessGrant{expiredGrant}
	revoked := active
	revoked.AccessGrants = nil
	provider := &recordingProvider{inspect: active, list: []cloudlease.Receipt{active}, revoke: revoked}
	controller := cloudlease.NewController(provider, func() time.Time { return now })

	result, err := controller.Sweep(context.Background(), cloudlease.SweepRequest{Repository: plan.Repository})
	if err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}
	if result.Examined != 1 || len(result.RevokedAccess) != 1 ||
		result.RevokedAccess[0] != "lease-1/expired-ssh" {
		t.Fatalf("Sweep() result = %#v, want one revoked access", result)
	}
	if len(result.Released) != 0 || len(result.Pending) != 0 || len(result.Failed) != 0 {
		t.Fatalf("Sweep() terminal result = %#v, want no release outcome", result)
	}
}

func validPlan(now time.Time) cloudlease.Plan {
	return cloudlease.Plan{
		Schema:     cloudlease.PlanSchemaV1,
		LeaseID:    "lease-1",
		RequestID:  "request-1",
		Provider:   "fake",
		Region:     "test-region",
		Repository: "WuKongIM/WuKongIM",
		Operator:   "tester",
		ExpiresAt:  now.Add(2 * time.Hour),
		Budget: cloudlease.Budget{
			Currency:        "CNY",
			LimitMicros:     10_000_000,
			CommittedMicros: 2_000_000,
		},
		Network: cloudlease.NetworkPlan{
			Isolated:   true,
			SingleZone: true,
		},
		HostGroups: []cloudlease.HostGroupPlan{{
			Role:  "worker",
			Count: 1,
			Compute: cloudlease.ComputePlan{
				VCPUs:          4,
				MemoryBytes:    8 << 30,
				Architecture:   "x86_64",
				BillingModel:   "postpaid",
				AllowBurstable: false,
			},
			SystemDisk: cloudlease.DiskPlan{
				Role:      "system",
				SizeBytes: 40 << 30,
				Class:     "ssd",
			},
		}},
		Tags: map[string]string{"purpose": "unit-test"},
	}
}

func activeReceipt(plan cloudlease.Plan, quote cloudlease.Quote, now time.Time) cloudlease.Receipt {
	baseTags := map[string]string{
		cloudlease.TagManagedBy:  cloudlease.ManagedByValue,
		cloudlease.TagLeaseID:    plan.LeaseID,
		cloudlease.TagRequestID:  plan.RequestID,
		cloudlease.TagRepository: plan.Repository,
		cloudlease.TagOperator:   plan.Operator,
		cloudlease.TagProvider:   plan.Provider,
		cloudlease.TagRegion:     plan.Region,
		cloudlease.TagPlanDigest: quote.PlanDigest,
		cloudlease.TagCreatedAt:  now.UTC().Format(time.RFC3339Nano),
		cloudlease.TagExpiresAt:  plan.ExpiresAt.UTC().Format(time.RFC3339Nano),
		"purpose":                "unit-test",
	}
	if plan.Provenance.SourceSHA != "" {
		baseTags[cloudlease.TagSourceSHA] = plan.Provenance.SourceSHA
	}
	if plan.Provenance.BundleDigest != "" {
		baseTags[cloudlease.TagBundleDigest] = plan.Provenance.BundleDigest
	}
	resourceTags := make(map[string]string, len(baseTags)+1)
	for key, value := range baseTags {
		resourceTags[key] = value
	}
	resourceTags[cloudlease.TagResourceRole] = "worker"
	return cloudlease.Receipt{
		LeaseID: plan.LeaseID, RequestID: plan.RequestID, Provider: plan.Provider,
		Region: plan.Region, Zone: quote.Zone, AccountIDHash: quote.AccountIDHash, Repository: plan.Repository,
		Operator: plan.Operator, PlanDigest: quote.PlanDigest, Provenance: plan.Provenance, State: cloudlease.StateActive,
		CreatedAt: now, ExpiresAt: plan.ExpiresAt, Quote: quote, Tags: baseTags,
		Resources: []cloudlease.Resource{{
			ID: "compute-1", Kind: "compute", Role: "worker", Billable: true, Tags: resourceTags,
		}},
	}
}

type recordingProvider struct {
	quote         cloudlease.Quote
	quoteCalls    int
	mutationCalls int
	inspect       cloudlease.Receipt
	inspectErr    error
	acquireCalls  int
	acquire       cloudlease.Receipt
	acquireErr    error
	release       cloudlease.ReleaseResult
	releaseErr    error
	list          []cloudlease.Receipt
	listErr       error
	revoke        cloudlease.Receipt
	revokeErr     error
}

func (*recordingProvider) Name() string { return "fake" }

func (p *recordingProvider) Quote(_ context.Context, _ cloudlease.QuoteRequest) (cloudlease.Quote, error) {
	p.quoteCalls++
	return p.quote, nil
}

func (p *recordingProvider) Acquire(context.Context, cloudlease.AcquireRequest) (cloudlease.Receipt, error) {
	p.mutationCalls++
	p.acquireCalls++
	return p.acquire, p.acquireErr
}

func (p *recordingProvider) Inspect(context.Context, cloudlease.Selector) (cloudlease.Receipt, error) {
	return p.inspect, p.inspectErr
}

func (p *recordingProvider) List(context.Context, cloudlease.InventoryFilter) ([]cloudlease.Receipt, error) {
	return p.list, p.listErr
}

func (p *recordingProvider) GrantAccess(context.Context, cloudlease.Selector, cloudlease.AccessGrant) (cloudlease.Receipt, error) {
	p.mutationCalls++
	return cloudlease.Receipt{}, nil
}

func (p *recordingProvider) RevokeAccess(context.Context, cloudlease.Selector, string) (cloudlease.Receipt, error) {
	p.mutationCalls++
	return p.revoke, p.revokeErr
}

func (p *recordingProvider) Release(context.Context, cloudlease.Selector) (cloudlease.ReleaseResult, error) {
	p.mutationCalls++
	return p.release, p.releaseErr
}
