package alibaba

import (
	"context"
	"errors"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudsim"
)

func TestProviderRejectsUnsafeNetworkAndHostConfiguration(t *testing.T) {
	t.Parallel()

	if !validConfig(cloneProviderConfig()) {
		t.Fatal("valid fixture was rejected")
	}
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "missing required field", mutate: func(config *Config) { config.Region = "" }},
		{name: "noncanonical VPC", mutate: func(config *Config) { config.VPCIPv4CIDR = "10.42.1.0/16" }},
		{name: "subnet outside VPC", mutate: func(config *Config) { config.VSwitchIPv4CIDR = "10.43.0.0/24" }},
		{name: "missing host address", mutate: func(config *Config) { delete(config.PrivateIPv4, "node-2") }},
		{name: "duplicate host address", mutate: func(config *Config) { config.PrivateIPv4["node-2"] = config.PrivateIPv4["node-1"] }},
		{name: "missing simulator source pool", mutate: func(config *Config) { config.SimulatorSourceIPv4 = nil }},
		{name: "source pool does not start at simulator", mutate: func(config *Config) {
			config.SimulatorSourceIPv4[0] = "10.42.0.30"
		}},
		{name: "secondary source collides with host", mutate: func(config *Config) {
			config.SimulatorSourceIPv4[1] = config.PrivateIPv4["node-1"]
		}},
		{name: "secondary source outside subnet", mutate: func(config *Config) {
			config.SimulatorSourceIPv4[1] = "10.42.1.21"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := cloneProviderConfig()
			test.mutate(&config)
			if validConfig(config) {
				t.Fatalf("validConfig() accepted unsafe config: %#v", config)
			}
		})
	}

	if _, err := New(Config{}, &apiStub{}, nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("New() error = %v, want ErrInvalidConfig", err)
	}
}

func TestProviderCreateValidationRequiresExactQuoteTagsAndBootstrapKey(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 2, 8, 0, 0, 0, time.UTC)
	request := testCreateRequest(now)
	request.Tags = mandatoryTestTags(request)
	quote := cloudsim.Quote{
		Currency: "CNY", WorstCaseCostMicros: 1_000_000,
		SelectedSKU: "ecs.c8i.xlarge", CapacityAvailable: true, QuotaAvailable: true,
	}
	if err := validateCreate(request, quote, cloneProviderConfig()); err != nil {
		t.Fatalf("validateCreate(valid) error = %v", err)
	}

	withoutKey := request
	withoutKey.BootstrapSSHPublicKey = ""
	if err := validateCreate(withoutKey, quote, cloneProviderConfig()); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("validateCreate(missing key) error = %v", err)
	}

	withoutTag := request
	withoutTag.Tags = maps.Clone(request.Tags)
	delete(withoutTag.Tags, cloudsim.TagBundleDigest)
	if err := validateCreate(withoutTag, quote, cloneProviderConfig()); !errors.Is(err, ErrInvalidConfig) ||
		!strings.Contains(err.Error(), cloudsim.TagBundleDigest) {
		t.Fatalf("validateCreate(missing tag) error = %v", err)
	}
}

func TestProviderQuoteFailsClosedOnUnavailableOrOverflowingOffers(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 2, 8, 0, 0, 0, time.UTC)
	request := testCreateRequest(now)
	providerErr := errors.New("pricing unavailable")
	provider, err := New(cloneProviderConfig(), &offerFailureAPI{
		apiStub: newCreatingAPIStub(),
		err:     providerErr,
	}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Quote(context.Background(), request); !errors.Is(err, providerErr) {
		t.Fatalf("Quote(provider error) = %v", err)
	}

	noQuota := newCreatingAPIStub()
	noQuota.offers = []Offer{{
		InstanceType: "ecs.c8i.xlarge", ZoneID: "cn-hangzhou-j",
		HourlyCostMicros: 1_000_000, Available: true, QuotaAvailable: false,
	}}
	provider, err = New(cloneProviderConfig(), noQuota, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	quote, err := provider.Quote(context.Background(), request)
	if err != nil || quote.SelectedSKU != "" || !quote.CapacityAvailable || quote.QuotaAvailable {
		t.Fatalf("Quote(no quota) = %#v, %v", quote, err)
	}

	invalidPreset := request
	invalidPreset.Preset = cloudsim.PresetStandard
	if _, err := provider.Quote(context.Background(), invalidPreset); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Quote(invalid preset) error = %v", err)
	}

	maxInt64 := int64(^uint64(0) >> 1)
	tests := []struct {
		name       string
		hourlyCost int64
		lease      time.Duration
	}{
		{name: "per-host multiplication", hourlyCost: maxInt64/4 + 1, lease: time.Second},
		{name: "lease multiplication", hourlyCost: maxInt64 / 8, lease: 3 * time.Second},
		{name: "ceil rounding", hourlyCost: maxInt64 / 4, lease: time.Second},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			api := newCreatingAPIStub()
			api.offers = []Offer{{
				InstanceType: "ecs.c8i.xlarge", ZoneID: "cn-hangzhou-j",
				HourlyCostMicros: test.hourlyCost, Available: true, QuotaAvailable: true,
			}}
			provider, err := New(cloneProviderConfig(), api, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			request := testCreateRequest(now)
			request.ExpiresAt = now.Add(test.lease)
			if _, err := provider.Quote(context.Background(), request); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("Quote() error = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestProviderStatusPreservesInventoryAndIngressFailures(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 2, 8, 0, 0, 0, time.UTC)
	providerErr := errors.New("inventory unavailable")
	provider, err := New(cloneProviderConfig(), &listFailureAPI{
		apiStub: &apiStub{},
		err:     providerErr,
	}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Status(context.Background(), "run-1"); !errors.Is(err, providerErr) {
		t.Fatalf("Status() error = %v", err)
	}
	if _, err := provider.Inventory(context.Background()); !errors.Is(err, providerErr) {
		t.Fatalf("Inventory() error = %v", err)
	}

	provider, err = New(cloneProviderConfig(), &apiStub{}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Status(context.Background(), "run-1"); !errors.Is(err, cloudsim.ErrRunNotFound) {
		t.Fatalf("Status(empty) error = %v", err)
	}

	base := newCreatingAPIStub()
	createdProvider, request := createBoundaryRun(t, now, base)
	_ = createdProvider
	ingressErr := errors.New("ingress inventory unavailable")
	provider, err = New(cloneProviderConfig(), &ingressFailureAPI{
		apiStub: base,
		err:     ingressErr,
	}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Status(context.Background(), request.RunID); !errors.Is(err, ingressErr) {
		t.Fatalf("Status(ingress failure) error = %v", err)
	}

	unchanged, err := provider.attachIngressWindows(context.Background(), cloudsim.Run{ID: request.RunID})
	if err != nil || unchanged.ID != request.RunID {
		t.Fatalf("attachIngressWindows(no group) = %#v, %v", unchanged, err)
	}
}

func TestProviderReconcilesProviderTimeWithoutTrustingStoredState(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 2, 8, 0, 0, 0, time.UTC)
	api := newCreatingAPIStub()
	provider, request := createBoundaryRun(t, now, api)

	for index := range api.assets {
		api.assets[index].Tags[tagRunState] = string(cloudsim.StateRunning)
		api.assets[index].Tags[tagActiveUntil] = now.Add(-time.Minute).Format(time.RFC3339)
	}
	run, err := provider.reconcile(request.RunID, api.assets)
	if err != nil || run.State != cloudsim.StateAnalysisGrace {
		t.Fatalf("reconcile(elapsed workload) = %#v, %v", run, err)
	}

	provider.now = func() time.Time { return request.ExpiresAt }
	run, err = provider.reconcile(request.RunID, api.assets)
	if err != nil || run.State != cloudsim.StateReleasePending {
		t.Fatalf("reconcile(expired lease) = %#v, %v", run, err)
	}
	if validReconciledState(cloudsim.State("unknown")) {
		t.Fatal("unknown provider state was accepted")
	}
}

func TestProviderTransitionStopsRetryingWhenContextIsCanceled(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 2, 8, 0, 0, 0, time.UTC)
	base := newCreatingAPIStub()
	_, request := createBoundaryRun(t, now, base)
	ctx, cancel := context.WithCancel(context.Background())
	updateErr := errors.New("state tag update unavailable")
	api := &updateFailureAPI{apiStub: base, err: updateErr, cancel: cancel}
	provider, err := New(cloneProviderConfig(), api, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}

	_, err = provider.Transition(ctx, cloudsim.TransitionRequest{
		RunID: request.RunID,
		Next:  cloudsim.StateReady,
	})
	if !errors.Is(err, updateErr) || !errors.Is(err, context.Canceled) || api.calls != 1 {
		t.Fatalf("Transition() = calls %d, error %v", api.calls, err)
	}
}

func TestCleanupFallbackRequiresExactRunIdentityAndLease(t *testing.T) {
	t.Parallel()

	provider, err := New(cloneProviderConfig(), &apiStub{}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.cleanupFallbackRun("run-1", nil); !errors.Is(err, cloudsim.ErrRunNotFound) {
		t.Fatalf("cleanupFallbackRun(empty) error = %v", err)
	}
	if _, err := provider.cleanupFallbackRun("run-1", []Asset{{
		ID: "i-1", Tags: map[string]string{
			cloudsim.TagManagedBy: cloudsim.ManagedByValue,
			cloudsim.TagRunID:     "different-run",
		},
	}}); !errors.Is(err, ErrAmbiguousInventory) {
		t.Fatalf("cleanupFallbackRun(identity mismatch) error = %v", err)
	}
	if _, err := provider.cleanupFallbackRun("run-1", []Asset{{
		ID: "i-1", Tags: map[string]string{
			cloudsim.TagManagedBy: cloudsim.ManagedByValue,
			cloudsim.TagRunID:     "run-1",
			cloudsim.TagExpiresAt: "not-a-time",
		},
	}}); !errors.Is(err, ErrAmbiguousInventory) {
		t.Fatalf("cleanupFallbackRun(invalid lease) error = %v", err)
	}
}

type offerFailureAPI struct {
	*apiStub
	err error
}

func (api *offerFailureAPI) Offers(context.Context, OfferRequest) ([]Offer, error) {
	return nil, api.err
}

type listFailureAPI struct {
	*apiStub
	err error
}

func (api *listFailureAPI) ListAssets(context.Context, ListAssetsRequest) ([]Asset, error) {
	return nil, api.err
}

type ingressFailureAPI struct {
	*apiStub
	err error
}

func (api *ingressFailureAPI) ListIngress(context.Context, IngressListRequest) ([]IngressWindow, error) {
	return nil, api.err
}

type updateFailureAPI struct {
	*apiStub
	err    error
	cancel context.CancelFunc
	calls  int
}

func (api *updateFailureAPI) UpdateRunState(context.Context, StateUpdateRequest) error {
	api.calls++
	api.cancel()
	return api.err
}

func cloneProviderConfig() Config {
	config := testConfig()
	config.PrivateIPv4 = maps.Clone(config.PrivateIPv4)
	config.SimulatorSourceIPv4 = append([]string(nil), config.SimulatorSourceIPv4...)
	config.Presets = maps.Clone(config.Presets)
	return config
}

func createBoundaryRun(
	t *testing.T,
	now time.Time,
	api *apiStub,
) (*Provider, cloudsim.CreateRequest) {
	t.Helper()
	provider, err := New(cloneProviderConfig(), api, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	request := testCreateRequest(now)
	quote, err := provider.Quote(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	request.Tags = mandatoryTestTags(request)
	if _, err := provider.Create(context.Background(), request, quote); err != nil {
		t.Fatal(err)
	}
	return provider, request
}
