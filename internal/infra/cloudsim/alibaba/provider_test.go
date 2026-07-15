package alibaba

import (
	"context"
	"errors"
	"maps"
	"net/netip"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudsim"
)

func TestProviderSelectsLowestAvailableAllowlistedOffer(t *testing.T) {
	now := time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC)
	api := &apiStub{offers: []Offer{
		{InstanceType: "ecs.g8i.xlarge", ZoneID: "cn-hangzhou-j", HourlyCostMicros: 2_000_000, Available: true, QuotaAvailable: true},
		{InstanceType: "ecs.c8i.xlarge", ZoneID: "cn-hangzhou-j", HourlyCostMicros: 1_200_000, Available: true, QuotaAvailable: true},
		{InstanceType: "ecs.e-c1m2.large", ZoneID: "cn-hangzhou-j", HourlyCostMicros: 800_000, Available: false, QuotaAvailable: true},
	}}
	provider, err := New(testConfig(), api, func() time.Time { return now })
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	quote, err := provider.Quote(context.Background(), testCreateRequest(now))
	if err != nil {
		t.Fatalf("Quote() error = %v", err)
	}
	if quote.SelectedSKU != "ecs.c8i.xlarge" {
		t.Fatalf("selected SKU = %q, want ecs.c8i.xlarge", quote.SelectedSKU)
	}
	if quote.WorstCaseCostMicros != 21_600_000 {
		t.Fatalf("worst-case cost = %d, want 21600000", quote.WorstCaseCostMicros)
	}
	if len(api.offerRequests) != 1 || api.offerRequests[0].SimulatorPrivateIPv4Count != 3 {
		t.Fatalf("offer requests = %#v, want simulator address capacity preflight", api.offerRequests)
	}
}

func TestProviderAuthorityUsesCurrentCloudCaller(t *testing.T) {
	provider, err := New(testConfig(), &apiStub{}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := provider.Authority(context.Background())
	if err != nil || authority.Region != "cn-hangzhou" || authority.AccountIDHash != "sha256:account" {
		t.Fatalf("authority = %#v, %v", authority, err)
	}
}

func TestProviderAuthorityRejectsDifferentCurrentCloudCaller(t *testing.T) {
	provider, err := New(testConfig(), &apiStub{accountIDHash: "sha256:other-account"}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Authority(context.Background()); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Authority() error = %v, want ErrInvalidConfig", err)
	}
}

func TestProviderCreatesFourScheduledReleaseHostsAndTwelveTaggedAssets(t *testing.T) {
	now := time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC)
	api := newCreatingAPIStub()
	provider, err := New(testConfig(), api, func() time.Time { return now })
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	req := testCreateRequest(now)
	quote, err := provider.Quote(context.Background(), req)
	if err != nil {
		t.Fatalf("Quote() error = %v", err)
	}
	req.Tags = mandatoryTestTags(req)

	run, err := provider.Create(context.Background(), req, quote)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if run.State != cloudsim.StateProvisioning {
		t.Fatalf("state = %q, want provisioning", run.State)
	}
	if len(run.Resources) != 12 {
		t.Fatalf("resources = %d, want 12", len(run.Resources))
	}
	if len(api.hostRequests) != 4 {
		t.Fatalf("host requests = %d, want 4", len(api.hostRequests))
	}
	roles := []string{"node-1", "node-2", "node-3", "sim"}
	for index, request := range api.hostRequests {
		if request.Role != roles[index] {
			t.Fatalf("host %d role = %q, want %q", index, request.Role, roles[index])
		}
		if !request.AutoReleaseAt.Equal(req.ExpiresAt) {
			t.Fatalf("host %s release = %s, want %s", request.Role, request.AutoReleaseAt, req.ExpiresAt)
		}
		if request.Tags[cloudsim.TagRunID] != req.RunID || request.Tags[cloudsim.TagResourceRole] != request.Role {
			t.Fatalf("host %s tags = %#v", request.Role, request.Tags)
		}
	}
	for _, resource := range run.Resources {
		for _, key := range cloudsim.MandatoryTagKeys() {
			if resource.Tags[key] == "" {
				t.Fatalf("resource %s missing mandatory tag %s", resource.ID, key)
			}
		}
	}
}

func TestProviderPersistsAndReconcilesLifecycleState(t *testing.T) {
	now := time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC)
	api := newCreatingAPIStub()
	provider, err := New(testConfig(), api, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	req := testCreateRequest(now)
	quote, _ := provider.Quote(context.Background(), req)
	req.Tags = mandatoryTestTags(req)
	if _, err := provider.Create(context.Background(), req, quote); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Transition(context.Background(), cloudsim.TransitionRequest{RunID: req.RunID, Next: cloudsim.StateReady}); err != nil {
		t.Fatal(err)
	}
	activeUntil := now.Add(2 * time.Hour)
	run, err := provider.Transition(context.Background(), cloudsim.TransitionRequest{RunID: req.RunID, Next: cloudsim.StateRunning, ActiveUntil: activeUntil})
	if err != nil || run.State != cloudsim.StateRunning || !run.ActiveUntil.Equal(activeUntil) {
		t.Fatalf("running transition = %#v, %v", run, err)
	}
	for _, asset := range api.assets {
		if asset.Tags[tagRunState] != string(cloudsim.StateRunning) || asset.Tags[tagActiveUntil] != activeUntil.Format(time.RFC3339) {
			t.Fatalf("asset %s lifecycle tags = %#v", asset.ID, asset.Tags)
		}
	}
}

func TestProviderRollsBackPartialCreate(t *testing.T) {
	now := time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC)
	api := newCreatingAPIStub()
	api.failHostRole = "node-2"
	api.failHostAfterCreate = true
	provider, err := New(testConfig(), api, func() time.Time { return now })
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	req := testCreateRequest(now)
	quote, err := provider.Quote(context.Background(), req)
	if err != nil {
		t.Fatalf("Quote() error = %v", err)
	}
	req.Tags = mandatoryTestTags(req)

	_, err = provider.Create(context.Background(), req, quote)
	if err == nil {
		t.Fatal("Create() error = nil, want injected failure")
	}
	if len(api.assets) != 0 {
		t.Fatalf("remaining assets = %#v, want rollback to empty", api.assets)
	}
}

func TestProviderRejectsDivergentImmutableResourceTags(t *testing.T) {
	now := time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC)
	api := newCreatingAPIStub()
	provider, err := New(testConfig(), api, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	req := testCreateRequest(now)
	quote, err := provider.Quote(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	req.Tags = mandatoryTestTags(req)
	if _, err := provider.Create(context.Background(), req, quote); err != nil {
		t.Fatal(err)
	}
	api.assets[len(api.assets)-1].Tags[cloudsim.TagBundleDigest] = "sha256:other"
	if _, err := provider.Status(context.Background(), req.RunID); !errors.Is(err, ErrAmbiguousInventory) {
		t.Fatalf("Status() error = %v, want ErrAmbiguousInventory", err)
	}
}

func TestProviderCleanupSurvivesPartialLifecycleTagUpdate(t *testing.T) {
	now := time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC)
	api := newCreatingAPIStub()
	provider, err := New(testConfig(), api, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	req := testCreateRequest(now)
	quote, _ := provider.Quote(context.Background(), req)
	req.Tags = mandatoryTestTags(req)
	if _, err := provider.Create(context.Background(), req, quote); err != nil {
		t.Fatal(err)
	}
	api.assets[0].Tags[tagRunState] = string(cloudsim.StateReady)
	runs, err := provider.Inventory(context.Background())
	if err != nil || len(runs) != 1 || runs[0].State != cloudsim.StateReleasePending {
		t.Fatalf("fallback inventory = %#v, %v", runs, err)
	}
	run, err := provider.Destroy(context.Background(), req.RunID)
	if err != nil || run.State != cloudsim.StateReleased || len(api.assets) != 0 {
		t.Fatalf("destroy after partial state = %#v, %v, residual=%#v", run, err, api.assets)
	}
}

func TestProviderOpensOnlyTheRequestedIngressSurface(t *testing.T) {
	now := time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC)
	api := newCreatingAPIStub()
	provider, err := New(testConfig(), api, func() time.Time { return now })
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	req := testCreateRequest(now)
	quote, _ := provider.Quote(context.Background(), req)
	req.Tags = mandatoryTestTags(req)
	_, err = provider.Create(context.Background(), req, quote)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	_, err = provider.OpenDeployment(context.Background(), cloudsim.OpenDeploymentRequest{
		RunID: req.RunID, SourcePrefix: netip.MustParsePrefix("203.0.113.10/32"), Until: now.Add(15 * time.Minute),
	})
	if err != nil {
		t.Fatalf("OpenDeployment() error = %v", err)
	}
	_, err = provider.OpenAnalysis(context.Background(), cloudsim.OpenAnalysisRequest{
		RunID: req.RunID, SourcePrefix: netip.MustParsePrefix("198.51.100.8/32"), Until: now.Add(40 * time.Minute),
	})
	if err != nil {
		t.Fatalf("OpenAnalysis() error = %v", err)
	}
	if len(api.ingress) != 2 || api.ingress[0].Port != 22 || api.ingress[1].Port != 19092 {
		t.Fatalf("ingress = %#v, want only SSH and MCP rules", api.ingress)
	}
	if api.ingress[0].Source.String() != "203.0.113.10/32" || api.ingress[1].Source.String() != "198.51.100.8/32" {
		t.Fatalf("ingress sources = %#v", api.ingress)
	}
	status, err := provider.Status(context.Background(), req.RunID)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.DeploymentWindow == nil || status.DeploymentWindow.SourcePrefix.String() != "203.0.113.10/32" ||
		status.AnalysisWindow == nil || status.AnalysisWindow.SourcePrefix.String() != "198.51.100.8/32" {
		t.Fatalf("reconciled ingress windows = deployment %#v analysis %#v", status.DeploymentWindow, status.AnalysisWindow)
	}
}

func TestProviderReconcilesMissingSpotHostAsInfrastructureInterruption(t *testing.T) {
	now := time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC)
	api := newCreatingAPIStub()
	provider, err := New(testConfig(), api, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	req := testCreateRequest(now)
	quote, err := provider.Quote(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	req.Tags = mandatoryTestTags(req)
	if _, err := provider.Create(context.Background(), req, quote); err != nil {
		t.Fatal(err)
	}
	remaining := api.assets[:0]
	for _, asset := range api.assets {
		if asset.Role == "node-3" && (asset.Kind == "compute" || asset.Kind == "disk") {
			continue
		}
		remaining = append(remaining, asset)
	}
	api.assets = remaining
	run, err := provider.Status(context.Background(), req.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != cloudsim.StateInfrastructureInterrupted {
		t.Fatalf("state = %s, want infrastructure_interrupted", run.State)
	}
}

func testConfig() Config {
	return Config{
		Region: "cn-hangzhou", ZoneID: "cn-hangzhou-j", ImageID: "aliyun_3_x64_20G_alibase_20250629.vhd",
		AccountIDHash: "sha256:account", VPCIPv4CIDR: "10.42.0.0/16", VSwitchIPv4CIDR: "10.42.0.0/24",
		SystemDiskCategory: "cloud_essd", SystemDiskSizeGiB: 40, DataDiskCategory: "cloud_essd", DataDiskSizeGiB: 100,
		PublicBandwidthMbps: 10,
		PrivateIPv4: map[string]string{
			"node-1": "10.42.0.11", "node-2": "10.42.0.12", "node-3": "10.42.0.13", "sim": "10.42.0.20",
		},
		SimulatorSourceIPv4: []string{"10.42.0.20", "10.42.0.21", "10.42.0.22"},
		Presets: map[cloudsim.Preset]Preset{
			cloudsim.PresetSmall: {InstanceTypes: []string{"ecs.g8i.xlarge", "ecs.c8i.xlarge", "ecs.e-c1m2.large"}},
		},
	}
}

func testCreateRequest(now time.Time) cloudsim.CreateRequest {
	return cloudsim.CreateRequest{
		RunID: "run-20260714-001", Provider: ProviderName, Region: "cn-hangzhou", AccountIDHash: "sha256:account",
		Repository: "WuKongIM/WuKongIM", SourceSHA: "0123456789012345678901234567890123456789",
		ScenarioDigest: "sha256:scenario", DeploymentBundleDigest: "sha256:bundle",
		MCPCertificateFingerprint: "sha256:certificate", Preset: cloudsim.PresetSmall,
		BootstrapSSHPublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKey cloud-sim",
		ExpiresAt:             now.Add(4*time.Hour + 30*time.Minute), MaxTotalCostMicros: 30_000_000, Currency: "CNY",
	}
}

func mandatoryTestTags(req cloudsim.CreateRequest) map[string]string {
	return map[string]string{
		cloudsim.TagManagedBy: cloudsim.ManagedByValue, cloudsim.TagRunID: req.RunID,
		cloudsim.TagRepository: req.Repository, cloudsim.TagResourceRole: "run",
		cloudsim.TagSourceSHA: req.SourceSHA, cloudsim.TagScenarioDigest: req.ScenarioDigest,
		cloudsim.TagBundleDigest: req.DeploymentBundleDigest, cloudsim.TagExpiresAt: req.ExpiresAt.Format(time.RFC3339),
		cloudsim.TagMCPCertificateFingerprint: req.MCPCertificateFingerprint,
	}
}

type apiStub struct {
	accountIDHash       string
	offers              []Offer
	offerRequests       []OfferRequest
	assets              []Asset
	hostRequests        []HostRequest
	ingress             []IngressRequest
	failHostRole        string
	failHostAfterCreate bool
	nextID              int
}

func (a *apiStub) AccountIDHash(context.Context) (string, error) {
	if a.accountIDHash == "" {
		return "sha256:account", nil
	}
	return a.accountIDHash, nil
}

func newCreatingAPIStub() *apiStub {
	return &apiStub{offers: []Offer{{
		InstanceType: "ecs.c8i.xlarge", ZoneID: "cn-hangzhou-j", HourlyCostMicros: 1_200_000,
		Available: true, QuotaAvailable: true,
	}}}
}

func (a *apiStub) Offers(_ context.Context, request OfferRequest) ([]Offer, error) {
	a.offerRequests = append(a.offerRequests, request)
	return append([]Offer(nil), a.offers...), nil
}

func (a *apiStub) ListAssets(_ context.Context, request ListAssetsRequest) ([]Asset, error) {
	result := make([]Asset, 0, len(a.assets))
	for _, asset := range a.assets {
		if request.RunID == "" || asset.Tags[cloudsim.TagRunID] == request.RunID {
			result = append(result, cloneTestAsset(asset))
		}
	}
	return result, nil
}

func (a *apiStub) CreateNetwork(_ context.Context, request NetworkRequest) ([]Asset, error) {
	assets := []Asset{
		a.asset("vpc", "run-network", request.Tags),
		a.asset("subnet", "run-network", request.Tags),
		a.asset("security-group", "run-network", request.Tags),
	}
	a.assets = append(a.assets, assets...)
	return assets, nil
}

func (a *apiStub) CreateHost(_ context.Context, request HostRequest) ([]Asset, error) {
	a.hostRequests = append(a.hostRequests, request)
	if request.Role == a.failHostRole && !a.failHostAfterCreate {
		return nil, errors.New("injected host failure")
	}
	assets := []Asset{a.asset("compute", request.Role, request.Tags), a.asset("disk", request.Role, request.Tags)}
	a.assets = append(a.assets, assets...)
	if request.Role == a.failHostRole {
		return nil, errors.New("injected host failure after provider allocation")
	}
	return assets, nil
}

func (a *apiStub) CreatePublicAddress(_ context.Context, request PublicAddressRequest) (Asset, error) {
	asset := a.asset("public-address", "sim", request.Tags)
	asset.PublicAddress = "203.0.113.20"
	a.assets = append(a.assets, asset)
	return asset, nil
}

func (*apiStub) AssociatePublicAddress(context.Context, string, string) error { return nil }

func (a *apiStub) SetIngress(_ context.Context, request IngressRequest) error {
	if request.Open {
		a.ingress = append(a.ingress, request)
		return nil
	}
	remaining := a.ingress[:0]
	for _, rule := range a.ingress {
		if rule.RunID != request.RunID || rule.Port != request.Port {
			remaining = append(remaining, rule)
		}
	}
	a.ingress = remaining
	return nil
}

func (a *apiStub) ListIngress(_ context.Context, request IngressListRequest) ([]IngressWindow, error) {
	result := make([]IngressWindow, 0, len(a.ingress))
	for _, ingress := range a.ingress {
		if ingress.RunID == request.RunID && ingress.SecurityGroupID == request.SecurityGroupID && ingress.Open {
			result = append(result, IngressWindow{Source: ingress.Source, Port: ingress.Port, Until: ingress.Until})
		}
	}
	return result, nil
}

func (a *apiStub) UpdateRunState(_ context.Context, request StateUpdateRequest) error {
	for index := range a.assets {
		if request.State == cloudsim.StateRunning {
			a.assets[index].Tags[tagActiveUntil] = request.ActiveUntil.UTC().Format(time.RFC3339)
		}
		a.assets[index].Tags[tagRunState] = string(request.State)
	}
	return nil
}

func (a *apiStub) DeleteAsset(_ context.Context, target Asset) error {
	remaining := a.assets[:0]
	for _, asset := range a.assets {
		if asset.ID != target.ID {
			remaining = append(remaining, asset)
		}
	}
	a.assets = remaining
	return nil
}

func (a *apiStub) asset(kind, role string, tags map[string]string) Asset {
	a.nextID++
	cloned := maps.Clone(tags)
	cloned[cloudsim.TagResourceRole] = role
	return Asset{ID: kind + "-" + time.Unix(int64(a.nextID), 0).UTC().Format("150405"), Kind: kind, Role: role, Tags: cloned}
}

func cloneTestAsset(asset Asset) Asset {
	asset.Tags = maps.Clone(asset.Tags)
	return asset
}
