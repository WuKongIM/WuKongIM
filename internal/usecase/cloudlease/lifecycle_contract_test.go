package cloudlease_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudlease"
	"golang.org/x/crypto/ssh"
)

func TestControllerAcquireWithBootstrapBindsNormalizedKeySet(t *testing.T) {
	now := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	plan := validPlan(now)
	plan.Provenance.SourceSHA = strings.Repeat("a", 40)
	plan.Provenance.BundleDigest = "sha256:" + strings.Repeat("b", 64)
	keys := contractBootstrapKeys(t)
	access := cloudlease.BootstrapAccess{AuthorizedKeys: []string{
		"  " + strings.TrimSpace(keys[1]) + " second-operator  ",
		strings.TrimSpace(keys[0]) + " first-operator",
	}}
	originalAccess := slices.Clone(access.AuthorizedKeys)

	var acquiredRequest cloudlease.AcquireRequest
	var providerReceipt cloudlease.Receipt
	provider := &leaseContractProvider{
		quote: validContractQuote(plan, now),
		inspectFn: func(context.Context, cloudlease.Selector) (cloudlease.Receipt, error) {
			return cloudlease.Receipt{}, cloudlease.ErrLeaseNotFound
		},
		acquireFn: func(_ context.Context, request cloudlease.AcquireRequest) (cloudlease.Receipt, error) {
			acquiredRequest = request
			providerReceipt = receiptFromAcquireRequest(request)
			return providerReceipt, nil
		},
	}
	controller := cloudlease.NewController(provider, func() time.Time { return now })
	quote, err := controller.Quote(context.Background(), plan)
	if err != nil {
		t.Fatalf("Quote() error = %v", err)
	}

	receipt, err := controller.AcquireWithBootstrap(context.Background(), plan, quote, access)
	if err != nil {
		t.Fatalf("AcquireWithBootstrap() error = %v", err)
	}
	wantKeys := []string{strings.TrimSpace(keys[0]), strings.TrimSpace(keys[1])}
	slices.Sort(wantKeys)
	if !slices.Equal(acquiredRequest.BootstrapAuthorizedKeys, wantKeys) {
		t.Fatalf("provider bootstrap keys = %q, want normalized sorted keys %q", acquiredRequest.BootstrapAuthorizedKeys, wantKeys)
	}
	if !slices.Equal(access.AuthorizedKeys, originalAccess) {
		t.Fatalf("caller bootstrap access was mutated: got %q, want %q", access.AuthorizedKeys, originalAccess)
	}
	digest := receipt.Tags[cloudlease.TagBootstrapAccessDigest]
	if !strings.HasPrefix(digest, "sha256:") || len(digest) != len("sha256:")+64 {
		t.Fatalf("bootstrap digest = %q, want sha256 digest", digest)
	}
	for _, raw := range acquiredRequest.BootstrapAuthorizedKeys {
		if strings.Contains(strings.Join(mapValues(receipt.Tags), "\n"), raw) {
			t.Fatalf("receipt tags contain raw bootstrap key %q", raw)
		}
	}
	if err := cloudlease.ValidateReceiptBootstrapAccess(receipt, access); err != nil {
		t.Fatalf("ValidateReceiptBootstrapAccess() error = %v", err)
	}
	if err := cloudlease.ValidateReceiptBootstrapAccess(receipt, cloudlease.BootstrapAccess{AuthorizedKeys: keys[:1]}); !errors.Is(err, cloudlease.ErrInvalidAccess) {
		t.Fatalf("ValidateReceiptBootstrapAccess(different set) error = %v, want ErrInvalidAccess", err)
	}

	// The controller returns detached inventory: a caller cannot mutate the
	// provider's retained receipt through the result.
	receipt.Tags[cloudlease.TagBootstrapAccessDigest] = "sha256:" + strings.Repeat("0", 64)
	if providerReceipt.Tags[cloudlease.TagBootstrapAccessDigest] != digest {
		t.Fatal("mutating returned receipt changed provider-owned tags")
	}
	if want := []string{"quote", "inspect", "acquire"}; !slices.Equal(provider.calls, want) {
		t.Fatalf("provider calls = %v, want %v", provider.calls, want)
	}
}

func TestControllerAcquireRecoversProviderTruthAfterAmbiguousMutation(t *testing.T) {
	now := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	providerFailure := errors.New("provider response lost")
	for _, test := range []struct {
		name      string
		state     cloudlease.State
		wantError error
	}{
		{name: "complete inventory is an idempotent success", state: cloudlease.StateActive},
		{name: "partial inventory remains cleanup-required", state: cloudlease.StateAcquiring, wantError: cloudlease.ErrAcquireIncomplete},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := validPlan(now)
			var recovered cloudlease.Receipt
			inspectCalls := 0
			provider := &leaseContractProvider{quote: validContractQuote(plan, now)}
			provider.inspectFn = func(context.Context, cloudlease.Selector) (cloudlease.Receipt, error) {
				inspectCalls++
				if inspectCalls == 1 {
					return cloudlease.Receipt{}, cloudlease.ErrLeaseNotFound
				}
				return recovered, nil
			}
			provider.acquireFn = func(_ context.Context, request cloudlease.AcquireRequest) (cloudlease.Receipt, error) {
				recovered = receiptFromAcquireRequest(request)
				recovered.State = test.state
				return cloudlease.Receipt{}, providerFailure
			}
			controller := cloudlease.NewController(provider, func() time.Time { return now })
			quote, err := controller.Quote(context.Background(), plan)
			if err != nil {
				t.Fatal(err)
			}

			receipt, err := controller.Acquire(context.Background(), plan, quote)
			if test.wantError == nil {
				if err != nil || receipt.State != cloudlease.StateActive {
					t.Fatalf("Acquire() = (%#v, %v), want recovered active receipt", receipt, err)
				}
			} else if !errors.Is(err, test.wantError) || !strings.Contains(err.Error(), providerFailure.Error()) {
				t.Fatalf("Acquire() error = %v, want %v retaining provider failure", err, test.wantError)
			}
			if inspectCalls != 2 {
				t.Fatalf("Inspect calls = %d, want initial check plus recovery", inspectCalls)
			}
		})
	}
}

func TestControllerAcquirePreservesCancellationWhenRecoveryFindsNothing(t *testing.T) {
	now := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	plan := validPlan(now)
	provider := &leaseContractProvider{
		quote: validContractQuote(plan, now),
		inspectFn: func(context.Context, cloudlease.Selector) (cloudlease.Receipt, error) {
			return cloudlease.Receipt{}, cloudlease.ErrLeaseNotFound
		},
		acquireFn: func(ctx context.Context, _ cloudlease.AcquireRequest) (cloudlease.Receipt, error) {
			return cloudlease.Receipt{}, ctx.Err()
		},
	}
	controller := cloudlease.NewController(provider, func() time.Time { return now })
	quote, err := controller.Quote(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := controller.Acquire(ctx, plan, quote); !errors.Is(err, context.Canceled) {
		t.Fatalf("Acquire(canceled) error = %v, want context.Canceled", err)
	}
}

func TestControllerAccessMutationRequiresExactReconciledInventory(t *testing.T) {
	now := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	plan := validPlan(now)
	quote := validContractQuote(plan, now)
	quote.PlanDigest = "digest-1"
	selector := contractSelector(plan, quote)
	active := activeReceipt(plan, quote, now)
	provider := &leaseContractProvider{
		inspectFn: func(context.Context, cloudlease.Selector) (cloudlease.Receipt, error) {
			return active, nil
		},
	}
	provider.grantFn = func(_ context.Context, gotSelector cloudlease.Selector, grant cloudlease.AccessGrant) (cloudlease.Receipt, error) {
		if gotSelector != selector {
			t.Fatalf("GrantAccess selector = %#v, want %#v", gotSelector, selector)
		}
		if grant.ID != "operator-ssh" || grant.TargetRole != "worker" || grant.Until.Location() != time.UTC {
			t.Fatalf("provider grant was not normalized: %#v", grant)
		}
		updated := active
		updated.AccessGrants = []cloudlease.AccessGrant{grant}
		return updated, nil
	}
	controller := cloudlease.NewController(provider, func() time.Time { return now })
	grant := cloudlease.AccessGrant{
		ID: " operator-ssh ", TargetRole: " worker ", Protocol: cloudlease.ProtocolTCP,
		PortFrom: 22, PortTo: 22, SourcePrefix: netip.MustParsePrefix("203.0.113.9/32"),
		Until: plan.ExpiresAt.In(time.FixedZone("operator", 8*60*60)),
	}
	granted, err := controller.GrantAccess(context.Background(), selector, grant)
	if err != nil {
		t.Fatalf("GrantAccess() error = %v", err)
	}
	if len(granted.AccessGrants) != 1 || granted.AccessGrants[0].ID != "operator-ssh" {
		t.Fatalf("GrantAccess() grants = %#v", granted.AccessGrants)
	}

	active.AccessGrants = slices.Clone(granted.AccessGrants)
	provider.revokeFn = func(_ context.Context, gotSelector cloudlease.Selector, grantID string) (cloudlease.Receipt, error) {
		if gotSelector != selector || grantID != "operator-ssh" {
			t.Fatalf("RevokeAccess() = (%#v, %q), want exact grant", gotSelector, grantID)
		}
		updated := active
		updated.AccessGrants = nil
		return updated, nil
	}
	revoked, err := controller.RevokeAccess(context.Background(), selector, " operator-ssh ")
	if err != nil {
		t.Fatalf("RevokeAccess() error = %v", err)
	}
	if len(revoked.AccessGrants) != 0 {
		t.Fatalf("RevokeAccess() grants = %#v, want empty", revoked.AccessGrants)
	}
}

func TestControllerReleaseUsesValidatedZeroInventoryProofForIdempotency(t *testing.T) {
	now := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	plan := validPlan(now)
	quote := validContractQuote(plan, now)
	quote.PlanDigest = "digest-1"
	selector := contractSelector(plan, quote)
	proof := &cloudlease.ZeroInventoryProof{
		Selector: selector, AccountIDHash: "sha256:account", ObservedAt: now,
		Scopes: []string{"compute", "network", "storage"},
	}

	t.Run("proof wins over an ambiguous provider response", func(t *testing.T) {
		provider := &leaseContractProvider{releaseFn: func(context.Context, cloudlease.Selector) (cloudlease.ReleaseResult, error) {
			return cloudlease.ReleaseResult{ZeroInventory: proof}, errors.New("transport closed after response")
		}}
		result, err := cloudlease.NewController(provider, nil).Release(context.Background(), selector)
		if err != nil || result.ZeroInventory == nil {
			t.Fatalf("Release() = (%#v, %v), want authoritative zero-inventory success", result, err)
		}
		result.ZeroInventory.Scopes[0] = "changed"
		if proof.Scopes[0] != "compute" {
			t.Fatal("Release() returned provider-owned proof memory")
		}
		if provider.releaseCalls != 1 {
			t.Fatalf("Release calls = %d, want 1", provider.releaseCalls)
		}
	})

	t.Run("invalid ambiguous response is retried once", func(t *testing.T) {
		transient := errors.New("temporary transport error")
		provider := &leaseContractProvider{}
		provider.releaseFn = func(context.Context, cloudlease.Selector) (cloudlease.ReleaseResult, error) {
			if provider.releaseCalls == 1 {
				return cloudlease.ReleaseResult{}, transient
			}
			return cloudlease.ReleaseResult{ZeroInventory: proof}, nil
		}
		result, err := cloudlease.NewController(provider, nil).Release(context.Background(), selector)
		if err != nil || result.ZeroInventory == nil {
			t.Fatalf("Release() = (%#v, %v), want successful reconciliation retry", result, err)
		}
		if provider.releaseCalls != 2 {
			t.Fatalf("Release calls = %d, want exactly 2", provider.releaseCalls)
		}
	})

	t.Run("invalid successful response fails closed without retry", func(t *testing.T) {
		provider := &leaseContractProvider{releaseFn: func(context.Context, cloudlease.Selector) (cloudlease.ReleaseResult, error) {
			return cloudlease.ReleaseResult{}, nil
		}}
		if _, err := cloudlease.NewController(provider, nil).Release(context.Background(), selector); !errors.Is(err, cloudlease.ErrProviderInvariant) {
			t.Fatalf("Release() error = %v, want ErrProviderInvariant", err)
		}
		if provider.releaseCalls != 1 {
			t.Fatalf("Release calls = %d, want 1", provider.releaseCalls)
		}
	})
}

func TestControllerSweepClassifiesProviderBackedCleanupOutcomes(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	released := expiredContractReceipt("lease-a-released", now, cloudlease.StateActive)
	pending := expiredContractReceipt("lease-b-pending", now, cloudlease.StateReleasePending)
	failed := expiredContractReceipt("lease-c-failed", now, cloudlease.StateActive)
	alreadyReleased := expiredContractReceipt("lease-d-already-released", now, cloudlease.StateReleased)
	alreadyReleased.Resources = nil
	invalid := expiredContractReceipt("lease-e-invalid", now, cloudlease.StateActive)
	delete(invalid.Resources[0].Tags, cloudlease.TagResourceRole)
	releaseFailure := errors.New("cleanup API unavailable")

	provider := &leaseContractProvider{}
	provider.listFn = func(_ context.Context, filter cloudlease.InventoryFilter) ([]cloudlease.Receipt, error) {
		if filter.Repository != "WuKongIM/WuKongIM" {
			t.Fatalf("List() repository = %q", filter.Repository)
		}
		// Deliberately unsorted: Sweep promises deterministic Lease ordering.
		return []cloudlease.Receipt{invalid, failed, pending, alreadyReleased, released}, nil
	}
	provider.releaseFn = func(_ context.Context, selector cloudlease.Selector) (cloudlease.ReleaseResult, error) {
		switch selector.LeaseID {
		case released.LeaseID:
			return cloudlease.ReleaseResult{ZeroInventory: &cloudlease.ZeroInventoryProof{
				Selector: selector, AccountIDHash: "sha256:account", ObservedAt: now,
				Scopes: []string{"compute", "network"},
			}}, nil
		case pending.LeaseID:
			residual := pending
			return cloudlease.ReleaseResult{Receipt: &residual}, nil
		case failed.LeaseID:
			return cloudlease.ReleaseResult{}, releaseFailure
		default:
			t.Fatalf("unexpected Release() for %q", selector.LeaseID)
			return cloudlease.ReleaseResult{}, nil
		}
	}

	result, err := cloudlease.NewController(provider, func() time.Time { return now }).Sweep(
		context.Background(), cloudlease.SweepRequest{Repository: " WuKongIM/WuKongIM "},
	)
	if err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}
	if result.Examined != 5 {
		t.Fatalf("Sweep().Examined = %d, want 5", result.Examined)
	}
	if want := []string{released.LeaseID}; !slices.Equal(result.Released, want) {
		t.Fatalf("Sweep().Released = %v, want %v", result.Released, want)
	}
	if want := []string{pending.LeaseID}; !slices.Equal(result.Pending, want) {
		t.Fatalf("Sweep().Pending = %v, want %v", result.Pending, want)
	}
	wantFailures := []cloudlease.SweepFailure{
		{LeaseID: failed.LeaseID, Reason: "release"},
		{LeaseID: invalid.LeaseID, Reason: "invalid_receipt"},
	}
	if !slices.Equal(result.Failed, wantFailures) {
		t.Fatalf("Sweep().Failed = %#v, want %#v", result.Failed, wantFailures)
	}
	// Failed Release is reconciled exactly once, hence two calls for that Lease.
	if provider.releaseCalls != 4 {
		t.Fatalf("total Release calls = %d, want 4", provider.releaseCalls)
	}
}

func validContractQuote(plan cloudlease.Plan, now time.Time) cloudlease.Quote {
	return cloudlease.Quote{
		LeaseID: plan.LeaseID, RequestID: plan.RequestID, Provider: plan.Provider,
		Region: plan.Region, Zone: "test-zone-a", AccountIDHash: "sha256:account",
		Currency: plan.Budget.Currency, EstimatedCostMicros: 7_000_000,
		CapacityAvailable: true, QuotaAvailable: true,
		QuotedAt: now, ValidUntil: now.Add(10 * time.Minute),
	}
}

func contractSelector(plan cloudlease.Plan, quote cloudlease.Quote) cloudlease.Selector {
	return cloudlease.Selector{
		LeaseID: plan.LeaseID, RequestID: plan.RequestID, Provider: plan.Provider,
		Region: plan.Region, Repository: plan.Repository, PlanDigest: quote.PlanDigest,
	}
}

func receiptFromAcquireRequest(request cloudlease.AcquireRequest) cloudlease.Receipt {
	receipt := activeReceipt(request.Plan, request.Quote, request.RequestedAt)
	for key, value := range request.BaseTags {
		receipt.Tags[key] = value
		receipt.Resources[0].Tags[key] = value
	}
	return receipt
}

func expiredContractReceipt(leaseID string, now time.Time, state cloudlease.State) cloudlease.Receipt {
	createdAt := now.Add(-2 * time.Hour)
	plan := validPlan(createdAt)
	plan.LeaseID = leaseID
	plan.ExpiresAt = now.Add(-time.Hour)
	quote := validContractQuote(plan, createdAt)
	quote.PlanDigest = "digest-" + leaseID
	receipt := activeReceipt(plan, quote, createdAt)
	receipt.State = state
	return receipt
}

func contractBootstrapKeys(t *testing.T) []string {
	t.Helper()
	keys := make([]string, 0, 2)
	for value := byte(1); value <= 2; value++ {
		private := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{value}, ed25519.SeedSize))
		publicKey, err := ssh.NewPublicKey(private.Public())
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, string(ssh.MarshalAuthorizedKey(publicKey)))
	}
	return keys
}

func mapValues(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
}

type leaseContractProvider struct {
	quote        cloudlease.Quote
	quoteErr     error
	inspectFn    func(context.Context, cloudlease.Selector) (cloudlease.Receipt, error)
	acquireFn    func(context.Context, cloudlease.AcquireRequest) (cloudlease.Receipt, error)
	listFn       func(context.Context, cloudlease.InventoryFilter) ([]cloudlease.Receipt, error)
	grantFn      func(context.Context, cloudlease.Selector, cloudlease.AccessGrant) (cloudlease.Receipt, error)
	revokeFn     func(context.Context, cloudlease.Selector, string) (cloudlease.Receipt, error)
	releaseFn    func(context.Context, cloudlease.Selector) (cloudlease.ReleaseResult, error)
	calls        []string
	releaseCalls int
}

func (*leaseContractProvider) Name() string { return "fake" }

func (p *leaseContractProvider) Quote(_ context.Context, _ cloudlease.QuoteRequest) (cloudlease.Quote, error) {
	p.calls = append(p.calls, "quote")
	return p.quote, p.quoteErr
}

func (p *leaseContractProvider) Acquire(ctx context.Context, request cloudlease.AcquireRequest) (cloudlease.Receipt, error) {
	p.calls = append(p.calls, "acquire")
	if p.acquireFn == nil {
		return cloudlease.Receipt{}, errors.New("unexpected Acquire call")
	}
	return p.acquireFn(ctx, request)
}

func (p *leaseContractProvider) Inspect(ctx context.Context, selector cloudlease.Selector) (cloudlease.Receipt, error) {
	p.calls = append(p.calls, "inspect")
	if p.inspectFn == nil {
		return cloudlease.Receipt{}, errors.New("unexpected Inspect call")
	}
	return p.inspectFn(ctx, selector)
}

func (p *leaseContractProvider) List(ctx context.Context, filter cloudlease.InventoryFilter) ([]cloudlease.Receipt, error) {
	p.calls = append(p.calls, "list")
	if p.listFn == nil {
		return nil, errors.New("unexpected List call")
	}
	return p.listFn(ctx, filter)
}

func (p *leaseContractProvider) GrantAccess(ctx context.Context, selector cloudlease.Selector, grant cloudlease.AccessGrant) (cloudlease.Receipt, error) {
	p.calls = append(p.calls, "grant")
	if p.grantFn == nil {
		return cloudlease.Receipt{}, errors.New("unexpected GrantAccess call")
	}
	return p.grantFn(ctx, selector, grant)
}

func (p *leaseContractProvider) RevokeAccess(ctx context.Context, selector cloudlease.Selector, grantID string) (cloudlease.Receipt, error) {
	p.calls = append(p.calls, "revoke")
	if p.revokeFn == nil {
		return cloudlease.Receipt{}, errors.New("unexpected RevokeAccess call")
	}
	return p.revokeFn(ctx, selector, grantID)
}

func (p *leaseContractProvider) Release(ctx context.Context, selector cloudlease.Selector) (cloudlease.ReleaseResult, error) {
	p.calls = append(p.calls, "release")
	p.releaseCalls++
	if p.releaseFn == nil {
		return cloudlease.ReleaseResult{}, errors.New("unexpected Release call")
	}
	return p.releaseFn(ctx, selector)
}
