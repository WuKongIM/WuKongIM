// Package alibaba implements the Alibaba Cloud Simulation Run provider adapter.
package alibaba

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudsim"
)

const (
	// ProviderName is the stable provider identifier used in Run Locators.
	ProviderName        = "alibaba"
	assetCount          = 12
	cleanupAttempts     = 90
	cleanupPollInterval = 2 * time.Second
	transitionAttempts  = 45

	tagAccountIDHash = "wukongim-account-hash"
	tagCreatedAt     = "wukongim-created-at"
	tagRunState      = "wukongim-run-state"
	tagActiveUntil   = "wukongim-active-until"
	tagCurrency      = "wukongim-cost-currency"
	tagWorstCost     = "wukongim-cost-micros"
	tagSelectedSKU   = "wukongim-selected-sku"
)

var (
	// ErrInvalidConfig reports unsafe or incomplete Alibaba adapter configuration.
	ErrInvalidConfig = errors.New("internal/infra/cloudsim/alibaba: invalid config")
	// ErrAmbiguousInventory reports conflicting resource identity under one Run Identity.
	ErrAmbiguousInventory = errors.New("internal/infra/cloudsim/alibaba: ambiguous inventory")
	// ErrResidualResources reports cleanup that could not prove empty provider inventory.
	ErrResidualResources = errors.New("internal/infra/cloudsim/alibaba: residual resources")
)

// Preset is one allowlisted set of Alibaba ECS instance types for a capacity class.
type Preset struct {
	// InstanceTypes is ordered only for audit readability; Quote selects by price.
	InstanceTypes []string `json:"instance_types" yaml:"instance_types"`
}

// Config contains non-secret, account-bound Alibaba infrastructure settings.
type Config struct {
	// Region is the Alibaba region used by this adapter instance.
	Region string `json:"region" yaml:"region"`
	// ZoneID fixes one availability zone so baseline latency remains comparable.
	ZoneID string `json:"zone_id" yaml:"zone_id"`
	// ImageID is an allowlisted Linux image used by all four hosts.
	ImageID string `json:"image_id" yaml:"image_id"`
	// AccountIDHash is the non-secret account binding recorded in run identity.
	AccountIDHash string `json:"account_id_hash" yaml:"account_id_hash"`
	// VPCIPv4CIDR is the isolated per-run VPC range.
	VPCIPv4CIDR string `json:"vpc_ipv4_cidr" yaml:"vpc_ipv4_cidr"`
	// VSwitchIPv4CIDR is the isolated per-run subnet range.
	VSwitchIPv4CIDR string `json:"vswitch_ipv4_cidr" yaml:"vswitch_ipv4_cidr"`
	// SystemDiskCategory selects the system disk class.
	SystemDiskCategory string `json:"system_disk_category" yaml:"system_disk_category"`
	// SystemDiskSizeGiB is the system disk capacity per host.
	SystemDiskSizeGiB int32 `json:"system_disk_size_gib" yaml:"system_disk_size_gib"`
	// DataDiskCategory selects the independent data disk class.
	DataDiskCategory string `json:"data_disk_category" yaml:"data_disk_category"`
	// DataDiskSizeGiB is the independent data disk capacity per host.
	DataDiskSizeGiB int32 `json:"data_disk_size_gib" yaml:"data_disk_size_gib"`
	// PublicBandwidthMbps bounds simulator ingress and egress bandwidth.
	PublicBandwidthMbps int32 `json:"public_bandwidth_mbps" yaml:"public_bandwidth_mbps"`
	// PrivateIPv4 fixes one unique in-subnet address per role so deployment
	// configuration is content-addressable before billable resources are created.
	PrivateIPv4 map[string]string `json:"private_ipv4" yaml:"private_ipv4"`
	// SimulatorSourceIPv4 contains the simulator primary address followed by
	// provider-assigned secondary addresses used by the explicit wkbench TCP source pool.
	SimulatorSourceIPv4 []string `json:"simulator_source_ipv4" yaml:"simulator_source_ipv4"`
	// Presets maps provider-neutral capacity classes to audited candidate SKUs.
	Presets map[cloudsim.Preset]Preset `json:"presets" yaml:"presets"`
}

// OfferRequest asks the SDK boundary for current spot capacity and all fixed hourly costs.
type OfferRequest struct {
	// Region is the Alibaba region used for capacity and price checks.
	Region string
	// ZoneID is the fixed availability zone used by the run.
	ZoneID string
	// ImageID is the allowlisted Linux image priced for every host.
	ImageID string
	// InstanceTypes contains the allowlisted candidate SKUs.
	InstanceTypes []string
	// HostCount is the number of spot instances required by the topology.
	HostCount int
	// SystemDiskCategory selects the system disk class included in pricing.
	SystemDiskCategory string
	// SystemDiskSizeGiB is the system disk capacity included in pricing.
	SystemDiskSizeGiB int32
	// DataDiskCategory selects the independent data disk class.
	DataDiskCategory string
	// DataDiskSizeGiB is the independent data disk capacity.
	DataDiskSizeGiB int32
	// PublicBandwidthMbps is the conservative public bandwidth price input.
	PublicBandwidthMbps int32
	// RequirePublicAddress includes public bandwidth in the conservative quote.
	RequirePublicAddress bool
	// SimulatorPrivateIPv4Count is the primary-plus-secondary address count the simulator SKU must support.
	SimulatorPrivateIPv4Count int
}

// Offer is one current allowlisted spot-capacity and price decision.
type Offer struct {
	// InstanceType is one concrete allowlisted ECS SKU.
	InstanceType string
	// ZoneID is the zone in which this offer is available.
	ZoneID string
	// HourlyCostMicros is a conservative per-host fixed hourly upper bound. The
	// SDK quote includes public bandwidth on every host even though only the
	// simulator receives an EIP, intentionally biasing the hard cost gate upward.
	HourlyCostMicros int64
	// Available reports current spot capacity for the SKU.
	Available bool
	// QuotaAvailable reports sufficient account quota for four hosts.
	QuotaAvailable bool
}

// ListAssetsRequest selects tagged assets without relying on display names.
type ListAssetsRequest struct {
	// Region is the exact Alibaba inventory region.
	Region string
	// RunID optionally restricts inventory to one exact tagged run.
	RunID string
}

// Asset is one normalized Alibaba resource returned by the SDK boundary.
type Asset struct {
	// ID is the provider resource identity.
	ID string
	// Kind is the normalized resource kind.
	Kind string
	// Role is the normalized run role read from tags.
	Role string
	// Billable reports whether residual inventory continues to incur cost.
	Billable bool
	// PrivateAddress is present only for compute inventory.
	PrivateAddress string
	// PublicAddress is present only for the simulator EIP.
	PublicAddress string
	// AttachedTo identifies the current provider attachment target.
	AttachedTo string
	// Tags contains the normalized provider tag map.
	Tags map[string]string
}

// NetworkRequest creates one run-owned VPC, vSwitch, and security group.
type NetworkRequest struct {
	// Region is the Alibaba region in which the network is created.
	Region string
	// ZoneID is the fixed availability zone for the vSwitch.
	ZoneID string
	// VPCIPv4CIDR is the isolated run VPC range.
	VPCIPv4CIDR string
	// VSwitchIPv4CIDR is the isolated run subnet range.
	VSwitchIPv4CIDR string
	// Tags is the immutable run identity applied at creation.
	Tags map[string]string
}

// HostRequest creates one spot host and one independent data disk.
type HostRequest struct {
	// Region is the Alibaba region in which the host is created.
	Region string
	// ZoneID is the fixed availability zone for the host and data disk.
	ZoneID string
	// Role is one of node-1, node-2, node-3, or sim.
	Role string
	// ImageID is the allowlisted Linux image.
	ImageID string
	// InstanceType is the priced and allowlisted spot SKU.
	InstanceType string
	// VSwitchID is the exact run-owned subnet.
	VSwitchID string
	// SecurityGroupID is the exact run-owned security group.
	SecurityGroupID string
	// PrivateIPv4 is the fixed primary address for this role.
	PrivateIPv4 string
	// SecondaryPrivateIPv4 contains provider-assigned simulator source addresses.
	SecondaryPrivateIPv4 []string
	// PrivateIPv4PrefixBits is the subnet prefix used to configure secondary addresses.
	PrivateIPv4PrefixBits int
	// SystemDiskCategory selects the host system disk class.
	SystemDiskCategory string
	// SystemDiskSizeGiB is the host system disk capacity.
	SystemDiskSizeGiB int32
	// DataDiskCategory selects the independent data disk class.
	DataDiskCategory string
	// DataDiskSizeGiB is the independent data disk capacity.
	DataDiskSizeGiB int32
	// AutoReleaseAt is the provider-native immutable instance release deadline.
	AutoReleaseAt time.Time
	// SSHPublicKey is the ephemeral provisioning key installed by cloud-init.
	SSHPublicKey string
	// Tags is the immutable run identity applied to the host and data disk.
	Tags map[string]string
}

// PublicAddressRequest allocates one tagged public address for the simulator.
type PublicAddressRequest struct {
	// Region is the Alibaba region in which the EIP is allocated.
	Region string
	// BandwidthMbps is the bounded pay-by-bandwidth allocation.
	BandwidthMbps int32
	// Tags is the immutable run identity applied to the EIP.
	Tags map[string]string
}

// IngressRequest mutates one exact run-owned security-group rule.
type IngressRequest struct {
	// RunID is the exact tagged run whose ingress is changed.
	RunID string
	// SecurityGroupID is the exact run-owned security group.
	SecurityGroupID string
	// Source is the single GitHub runner IPv4 /32 when opening ingress.
	Source netip.Prefix
	// Port is the single SSH or MCP port being changed.
	Port uint16
	// Until is encoded into the owned rule description for expiry reconciliation.
	Until time.Time
	// Open selects rule creation; false removes only matching run-owned rules.
	Open bool
}

// StateUpdateRequest applies one control-plane lifecycle state to every run asset.
type StateUpdateRequest struct {
	// Region is the exact Alibaba inventory region.
	Region string
	// Assets is the complete, identity-reconciled run inventory.
	Assets []Asset
	// State is the lifecycle state written to all resource tags.
	State cloudsim.State
	// ActiveUntil is written only when the run enters running.
	ActiveUntil time.Time
}

// API is the system boundary implemented with Alibaba's official Go SDK.
type API interface {
	// AccountIDHash returns the stable hash of the authenticated Alibaba account.
	AccountIDHash(context.Context) (string, error)
	// Offers lists candidate spot instance offers for the requested capacity.
	Offers(context.Context, OfferRequest) ([]Offer, error)
	// ListAssets returns the provider inventory selected by the exact run tags.
	ListAssets(context.Context, ListAssetsRequest) ([]Asset, error)
	// CreateNetwork creates the run-owned VPC, vSwitch, and security group assets.
	CreateNetwork(context.Context, NetworkRequest) ([]Asset, error)
	// CreateHost creates one tagged spot instance and its attached data disk.
	CreateHost(context.Context, HostRequest) ([]Asset, error)
	// CreatePublicAddress allocates one run-owned public address.
	CreatePublicAddress(context.Context, PublicAddressRequest) (Asset, error)
	// AssociatePublicAddress binds a public address to its intended instance.
	AssociatePublicAddress(context.Context, string, string) error
	// SetIngress creates or removes only the run-owned temporary ingress rule.
	SetIngress(context.Context, IngressRequest) error
	// UpdateRunState writes one lifecycle transition to every run asset.
	UpdateRunState(context.Context, StateUpdateRequest) error
	// DeleteAsset removes one identity-reconciled run asset.
	DeleteAsset(context.Context, Asset) error
}

// Provider reconciles Alibaba resources exclusively through tags and provider inventory.
type Provider struct {
	config Config
	api    API
	now    func() time.Time
}

// New validates immutable adapter configuration and returns an Alibaba provider.
func New(config Config, api API, now func() time.Time) (*Provider, error) {
	if now == nil {
		now = time.Now
	}
	if api == nil || !validConfig(config) {
		return nil, ErrInvalidConfig
	}
	return &Provider{config: config, api: api, now: now}, nil
}

// Name returns the stable Alibaba provider identity.
func (*Provider) Name() string { return ProviderName }

// Authority returns the exact account and region bound to this adapter.
func (p *Provider) Authority(ctx context.Context) (cloudsim.ProviderAuthority, error) {
	accountIDHash, err := p.api.AccountIDHash(ctx)
	if err != nil {
		return cloudsim.ProviderAuthority{}, err
	}
	if accountIDHash != p.config.AccountIDHash {
		return cloudsim.ProviderAuthority{}, ErrInvalidConfig
	}
	return cloudsim.ProviderAuthority{Provider: ProviderName, Region: p.config.Region, AccountIDHash: accountIDHash}, nil
}

// Quote selects the lowest-cost available allowlisted spot type and prices the immutable lease.
func (p *Provider) Quote(ctx context.Context, req cloudsim.CreateRequest) (cloudsim.Quote, error) {
	preset, ok := p.config.Presets[req.Preset]
	if !ok || len(preset.InstanceTypes) == 0 || req.Region != p.config.Region || req.AccountIDHash != p.config.AccountIDHash {
		return cloudsim.Quote{}, ErrInvalidConfig
	}
	offers, err := p.api.Offers(ctx, OfferRequest{
		Region: p.config.Region, ZoneID: p.config.ZoneID, ImageID: p.config.ImageID,
		InstanceTypes: slices.Clone(preset.InstanceTypes), HostCount: 4,
		SystemDiskCategory: p.config.SystemDiskCategory, SystemDiskSizeGiB: p.config.SystemDiskSizeGiB,
		DataDiskCategory: p.config.DataDiskCategory, DataDiskSizeGiB: p.config.DataDiskSizeGiB,
		PublicBandwidthMbps: p.config.PublicBandwidthMbps, RequirePublicAddress: true,
		SimulatorPrivateIPv4Count: len(p.config.SimulatorSourceIPv4),
	})
	if err != nil {
		return cloudsim.Quote{}, err
	}
	allowlisted := make(map[string]struct{}, len(preset.InstanceTypes))
	for _, instanceType := range preset.InstanceTypes {
		allowlisted[instanceType] = struct{}{}
	}
	var selected Offer
	capacitySeen := false
	quotaSeen := false
	for _, offer := range offers {
		if _, allowed := allowlisted[offer.InstanceType]; !allowed || offer.ZoneID != p.config.ZoneID || offer.HourlyCostMicros <= 0 {
			continue
		}
		capacitySeen = capacitySeen || offer.Available
		quotaSeen = quotaSeen || offer.QuotaAvailable
		if !offer.Available || !offer.QuotaAvailable {
			continue
		}
		if selected.InstanceType == "" || offer.HourlyCostMicros < selected.HourlyCostMicros {
			selected = offer
		}
	}
	if selected.InstanceType == "" {
		return cloudsim.Quote{Currency: "CNY", CapacityAvailable: capacitySeen, QuotaAvailable: quotaSeen}, nil
	}
	leaseSeconds := int64(req.ExpiresAt.Sub(p.now().UTC()) / time.Second)
	if leaseSeconds <= 0 || selected.HourlyCostMicros > (1<<63-1)/4 {
		return cloudsim.Quote{}, ErrInvalidConfig
	}
	hourlyTotal := selected.HourlyCostMicros * 4
	if hourlyTotal > (1<<63-1)/leaseSeconds {
		return cloudsim.Quote{}, ErrInvalidConfig
	}
	product := hourlyTotal * leaseSeconds
	if product > (1<<63-1)-3599 {
		return cloudsim.Quote{}, ErrInvalidConfig
	}
	worstCost := (product + 3599) / 3600
	return cloudsim.Quote{
		Currency: "CNY", WorstCaseCostMicros: worstCost, SelectedSKU: selected.InstanceType,
		SpotPriceMicrosPerHour: selected.HourlyCostMicros, CapacityAvailable: true, QuotaAvailable: true,
	}, nil
}

// Inventory returns all active runs visible to this account-scoped provider.
func (p *Provider) Inventory(ctx context.Context) ([]cloudsim.Run, error) {
	assets, err := p.api.ListAssets(ctx, ListAssetsRequest{Region: p.config.Region})
	if err != nil {
		return nil, err
	}
	grouped := make(map[string][]Asset)
	for _, asset := range assets {
		if asset.Tags[cloudsim.TagManagedBy] != cloudsim.ManagedByValue || strings.TrimSpace(asset.Tags[cloudsim.TagRunID]) == "" {
			continue
		}
		runID := asset.Tags[cloudsim.TagRunID]
		grouped[runID] = append(grouped[runID], asset)
	}
	runIDs := make([]string, 0, len(grouped))
	for runID := range grouped {
		runIDs = append(runIDs, runID)
	}
	sort.Strings(runIDs)
	runs := make([]cloudsim.Run, 0, len(runIDs))
	for _, runID := range runIDs {
		run, reconcileErr := p.reconcile(runID, grouped[runID])
		if reconcileErr != nil {
			run, reconcileErr = p.cleanupFallbackRun(runID, grouped[runID])
			if reconcileErr != nil {
				return nil, reconcileErr
			}
		}
		runs = append(runs, run)
	}
	return runs, nil
}

// Create creates one isolated network, four scheduled-release spot hosts, four data disks, and one simulator address.
func (p *Provider) Create(ctx context.Context, req cloudsim.CreateRequest, quote cloudsim.Quote) (run cloudsim.Run, err error) {
	if err := validateCreate(req, quote, p.config); err != nil {
		return cloudsim.Run{}, err
	}
	baseTags := maps.Clone(req.Tags)
	baseTags[tagAccountIDHash] = req.AccountIDHash
	baseTags[tagCreatedAt] = p.now().UTC().Format(time.RFC3339)
	baseTags[tagRunState] = string(cloudsim.StateProvisioning)
	baseTags[tagCurrency] = quote.Currency
	baseTags[tagWorstCost] = strconv.FormatInt(quote.WorstCaseCostMicros, 10)
	baseTags[tagSelectedSKU] = quote.SelectedSKU

	created := make([]Asset, 0, assetCount)
	defer func() {
		if err == nil {
			return
		}
		rollbackAssets, listErr := p.api.ListAssets(ctx, ListAssetsRequest{Region: p.config.Region, RunID: req.RunID})
		if listErr != nil {
			rollbackAssets = created
		}
		_, deleteErr := p.deleteRunAssets(ctx, req.RunID, rollbackAssets)
		rollbackErr := errors.Join(listErr, deleteErr)
		if rollbackErr != nil {
			err = errors.Join(err, fmt.Errorf("rollback: %w", rollbackErr))
		}
	}()

	network, err := p.api.CreateNetwork(ctx, NetworkRequest{
		Region: p.config.Region, ZoneID: p.config.ZoneID, VPCIPv4CIDR: p.config.VPCIPv4CIDR,
		VSwitchIPv4CIDR: p.config.VSwitchIPv4CIDR, Tags: roleTags(baseTags, "run-network"),
	})
	if err != nil {
		return cloudsim.Run{}, fmt.Errorf("create network: %w", err)
	}
	created = append(created, network...)
	vSwitchID := assetID(network, "subnet")
	securityGroupID := assetID(network, "security-group")
	if vSwitchID == "" || securityGroupID == "" || assetID(network, "vpc") == "" {
		return cloudsim.Run{}, ErrAmbiguousInventory
	}
	var simulatorID string
	for _, role := range []string{"node-1", "node-2", "node-3", "sim"} {
		secondaryAddresses := []string(nil)
		if role == "sim" && len(p.config.SimulatorSourceIPv4) > 1 {
			secondaryAddresses = slices.Clone(p.config.SimulatorSourceIPv4[1:])
		}
		hostAssets, createErr := p.api.CreateHost(ctx, HostRequest{
			Region: p.config.Region, ZoneID: p.config.ZoneID, Role: role, ImageID: p.config.ImageID,
			InstanceType: quote.SelectedSKU, VSwitchID: vSwitchID, SecurityGroupID: securityGroupID,
			PrivateIPv4: p.config.PrivateIPv4[role], SecondaryPrivateIPv4: secondaryAddresses,
			PrivateIPv4PrefixBits: netip.MustParsePrefix(p.config.VSwitchIPv4CIDR).Bits(),
			SystemDiskCategory:    p.config.SystemDiskCategory, SystemDiskSizeGiB: p.config.SystemDiskSizeGiB,
			DataDiskCategory: p.config.DataDiskCategory, DataDiskSizeGiB: p.config.DataDiskSizeGiB,
			AutoReleaseAt: req.ExpiresAt.UTC(), Tags: roleTags(baseTags, role),
			SSHPublicKey: req.BootstrapSSHPublicKey,
		})
		if createErr != nil {
			return cloudsim.Run{}, fmt.Errorf("create host %s: %w", role, createErr)
		}
		created = append(created, hostAssets...)
		if role == "sim" {
			simulatorID = assetID(hostAssets, "compute")
		}
	}
	if simulatorID == "" {
		return cloudsim.Run{}, ErrAmbiguousInventory
	}
	address, err := p.api.CreatePublicAddress(ctx, PublicAddressRequest{
		Region: p.config.Region, BandwidthMbps: p.config.PublicBandwidthMbps, Tags: roleTags(baseTags, "sim"),
	})
	if err != nil {
		return cloudsim.Run{}, fmt.Errorf("create simulator address: %w", err)
	}
	created = append(created, address)
	if err := p.api.AssociatePublicAddress(ctx, address.ID, simulatorID); err != nil {
		return cloudsim.Run{}, fmt.Errorf("associate simulator address: %w", err)
	}
	if len(created) != assetCount {
		return cloudsim.Run{}, fmt.Errorf("%w: created %d assets, want %d", ErrAmbiguousInventory, len(created), assetCount)
	}
	return p.reconcile(req.RunID, created)
}

// Status reconciles one exact live run from tagged provider inventory.
func (p *Provider) Status(ctx context.Context, runID string) (cloudsim.Run, error) {
	if strings.TrimSpace(runID) == "" {
		return cloudsim.Run{}, cloudsim.ErrRunNotFound
	}
	assets, err := p.api.ListAssets(ctx, ListAssetsRequest{Region: p.config.Region, RunID: runID})
	if err != nil {
		return cloudsim.Run{}, err
	}
	if len(assets) == 0 {
		return cloudsim.Run{}, cloudsim.ErrRunNotFound
	}
	return p.reconcile(runID, assets)
}

// Transition writes one validated lifecycle step to every tagged run asset and
// waits until provider inventory returns one consistent new state.
func (p *Provider) Transition(ctx context.Context, req cloudsim.TransitionRequest) (cloudsim.Run, error) {
	run, err := p.Status(ctx, req.RunID)
	if err != nil {
		return cloudsim.Run{}, err
	}
	assets, err := p.api.ListAssets(ctx, ListAssetsRequest{Region: p.config.Region, RunID: req.RunID})
	if err != nil {
		return cloudsim.Run{}, err
	}
	if len(assets) != len(run.Resources) {
		return cloudsim.Run{}, ErrAmbiguousInventory
	}
	var updateErr error
	for attempt := 0; attempt < 5; attempt++ {
		updateErr = p.api.UpdateRunState(ctx, StateUpdateRequest{
			Region: p.config.Region, Assets: assets, State: req.Next, ActiveUntil: req.ActiveUntil,
		})
		if updateErr == nil {
			break
		}
		if waitErr := waitForCleanup(ctx, cleanupPollInterval); waitErr != nil {
			return cloudsim.Run{}, errors.Join(updateErr, waitErr)
		}
	}
	if updateErr != nil {
		return cloudsim.Run{}, updateErr
	}
	var lastErr error
	for attempt := 0; attempt < transitionAttempts; attempt++ {
		updated, statusErr := p.Status(ctx, req.RunID)
		if statusErr == nil && updated.State == req.Next && (req.Next != cloudsim.StateRunning || updated.ActiveUntil.Equal(req.ActiveUntil.UTC())) {
			return updated, nil
		}
		lastErr = statusErr
		if attempt+1 == transitionAttempts {
			break
		}
		if waitErr := waitForCleanup(ctx, cleanupPollInterval); waitErr != nil {
			return cloudsim.Run{}, errors.Join(lastErr, waitErr)
		}
	}
	if lastErr == nil {
		lastErr = ErrAmbiguousInventory
	}
	return cloudsim.Run{}, fmt.Errorf("persist run state %s: %w", req.Next, lastErr)
}

// OpenDeployment admits the exact runner /32 to simulator SSH only.
func (p *Provider) OpenDeployment(ctx context.Context, req cloudsim.OpenDeploymentRequest) (cloudsim.Run, error) {
	return p.setIngress(ctx, req.RunID, req.SourcePrefix, req.Until, 22, true, true)
}

// CloseDeployment removes simulator SSH ingress for one exact run.
func (p *Provider) CloseDeployment(ctx context.Context, runID string) (cloudsim.Run, error) {
	return p.setIngress(ctx, runID, netip.Prefix{}, time.Time{}, 22, false, true)
}

// OpenAnalysis admits the exact runner /32 to simulator MCP HTTPS only.
func (p *Provider) OpenAnalysis(ctx context.Context, req cloudsim.OpenAnalysisRequest) (cloudsim.Run, error) {
	return p.setIngress(ctx, req.RunID, req.SourcePrefix, req.Until, 19092, true, false)
}

// CloseAnalysis removes simulator MCP ingress for one exact run.
func (p *Provider) CloseAnalysis(ctx context.Context, runID string) (cloudsim.Run, error) {
	return p.setIngress(ctx, runID, netip.Prefix{}, time.Time{}, 19092, false, false)
}

// Destroy removes all run-tagged resource types and proves inventory absence.
func (p *Provider) Destroy(ctx context.Context, runID string) (cloudsim.Run, error) {
	assets, err := p.api.ListAssets(ctx, ListAssetsRequest{Region: p.config.Region, RunID: runID})
	if err != nil {
		return cloudsim.Run{}, err
	}
	if len(assets) == 0 {
		return cloudsim.Run{ID: runID, Provider: ProviderName, Region: p.config.Region, AccountIDHash: p.config.AccountIDHash, State: cloudsim.StateReleased, Resources: []cloudsim.Resource{}}, nil
	}
	run, reconcileErr := p.reconcile(runID, assets)
	if reconcileErr != nil {
		run, reconcileErr = p.cleanupFallbackRun(runID, assets)
		if reconcileErr != nil {
			return cloudsim.Run{}, reconcileErr
		}
	}
	var deploymentErr error
	var analysisErr error
	if securityGroupID := assetID(assets, "security-group"); securityGroupID != "" {
		deploymentErr = p.api.SetIngress(ctx, IngressRequest{RunID: runID, SecurityGroupID: securityGroupID, Port: 22})
		analysisErr = p.api.SetIngress(ctx, IngressRequest{RunID: runID, SecurityGroupID: securityGroupID, Port: 19092})
	}
	remaining, deleteErr := p.deleteRunAssets(ctx, runID, assets)
	if len(remaining) != 0 {
		run.State = cloudsim.StateReleasePending
		run.Resources = resourcesFromAssets(remaining)
		return run, errors.Join(deploymentErr, analysisErr, deleteErr, fmt.Errorf("%w: %d", ErrResidualResources, len(remaining)))
	}
	run.State = cloudsim.StateReleased
	run.Resources = []cloudsim.Resource{}
	run.AnalysisWindow = nil
	run.DeploymentWindow = nil
	// Provider inventory is the cleanup authority. Transient delete or ingress
	// errors are no longer actionable after a second listing proves zero resources.
	return run, nil
}

func (p *Provider) cleanupFallbackRun(runID string, assets []Asset) (cloudsim.Run, error) {
	if len(assets) == 0 {
		return cloudsim.Run{}, cloudsim.ErrRunNotFound
	}
	var expiresAt time.Time
	var createdAt time.Time
	for _, asset := range assets {
		if asset.Tags[cloudsim.TagManagedBy] != cloudsim.ManagedByValue || asset.Tags[cloudsim.TagRunID] != runID {
			return cloudsim.Run{}, ErrAmbiguousInventory
		}
		candidate, err := time.Parse(time.RFC3339, asset.Tags[cloudsim.TagExpiresAt])
		if err != nil {
			return cloudsim.Run{}, ErrAmbiguousInventory
		}
		if expiresAt.IsZero() || candidate.Before(expiresAt) {
			expiresAt = candidate
		}
		if value, parseErr := time.Parse(time.RFC3339, asset.Tags[tagCreatedAt]); parseErr == nil && (createdAt.IsZero() || value.Before(createdAt)) {
			createdAt = value
		}
	}
	return cloudsim.Run{
		ID: runID, Provider: ProviderName, Region: p.config.Region, AccountIDHash: p.config.AccountIDHash,
		Repository: assets[0].Tags[cloudsim.TagRepository], State: cloudsim.StateReleasePending,
		CreatedAt: createdAt, ExpiresAt: expiresAt, Tags: maps.Clone(assets[0].Tags), Resources: resourcesFromAssets(assets),
	}, nil
}

func (p *Provider) deleteRunAssets(ctx context.Context, runID string, assets []Asset) ([]Asset, error) {
	remaining := append([]Asset(nil), assets...)
	var lastErr error
	for attempt := 0; attempt < cleanupAttempts; attempt++ {
		deleteErr := p.deleteAssets(ctx, remaining)
		listed, listErr := p.api.ListAssets(ctx, ListAssetsRequest{Region: p.config.Region, RunID: runID})
		if listErr == nil {
			remaining = listed
			if len(remaining) == 0 {
				return nil, nil
			}
		}
		lastErr = errors.Join(deleteErr, listErr)
		if attempt+1 == cleanupAttempts {
			break
		}
		if err := waitForCleanup(ctx, cleanupPollInterval); err != nil {
			return remaining, errors.Join(lastErr, err)
		}
	}
	return remaining, errors.Join(lastErr, fmt.Errorf("%w: %d", ErrResidualResources, len(remaining)))
}

func waitForCleanup(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (p *Provider) setIngress(ctx context.Context, runID string, source netip.Prefix, until time.Time, port uint16, open, deployment bool) (cloudsim.Run, error) {
	run, err := p.Status(ctx, runID)
	if err != nil {
		return cloudsim.Run{}, err
	}
	securityGroupID := ""
	for _, resource := range run.Resources {
		if resource.Kind == "security-group" {
			securityGroupID = resource.ID
			break
		}
	}
	if securityGroupID == "" {
		return cloudsim.Run{}, ErrAmbiguousInventory
	}
	if err := p.api.SetIngress(ctx, IngressRequest{RunID: runID, SecurityGroupID: securityGroupID, Source: source, Port: port, Until: until.UTC(), Open: open}); err != nil {
		return cloudsim.Run{}, err
	}
	if deployment {
		if open {
			run.DeploymentWindow = &cloudsim.DeploymentWindow{SourcePrefix: source, Until: until.UTC()}
		} else {
			run.DeploymentWindow = nil
		}
	} else if open {
		run.AnalysisWindow = &cloudsim.AnalysisWindow{SourcePrefix: source, Until: until.UTC()}
	} else {
		run.AnalysisWindow = nil
	}
	return run, nil
}

func (p *Provider) deleteAssets(ctx context.Context, assets []Asset) error {
	priority := map[string]int{"public-address": 0, "compute": 1, "disk": 2, "security-group": 3, "subnet": 4, "vpc": 5}
	ordered := append([]Asset(nil), assets...)
	sort.SliceStable(ordered, func(i, j int) bool { return priority[ordered[i].Kind] < priority[ordered[j].Kind] })
	var errs []error
	for _, asset := range ordered {
		if err := p.api.DeleteAsset(ctx, asset); err != nil {
			errs = append(errs, fmt.Errorf("delete %s %s: %w", asset.Kind, asset.ID, err))
		}
	}
	return errors.Join(errs...)
}

func (p *Provider) reconcile(runID string, assets []Asset) (cloudsim.Run, error) {
	if len(assets) == 0 {
		return cloudsim.Run{}, cloudsim.ErrRunNotFound
	}
	first := assets[0]
	identityTags := append(cloudsim.MandatoryTagKeys(), tagAccountIDHash, tagCreatedAt, tagRunState, tagCurrency, tagWorstCost, tagSelectedSKU)
	seenAssets := make(map[string]struct{}, len(assets))
	for _, asset := range assets {
		for _, key := range identityTags {
			if key == cloudsim.TagResourceRole {
				if strings.TrimSpace(asset.Tags[key]) == "" {
					return cloudsim.Run{}, ErrAmbiguousInventory
				}
				continue
			}
			if strings.TrimSpace(first.Tags[key]) == "" || asset.Tags[key] != first.Tags[key] {
				return cloudsim.Run{}, ErrAmbiguousInventory
			}
		}
		if asset.Tags[tagActiveUntil] != first.Tags[tagActiveUntil] {
			return cloudsim.Run{}, ErrAmbiguousInventory
		}
		if asset.Tags[cloudsim.TagRunID] != runID || asset.Tags[cloudsim.TagManagedBy] != cloudsim.ManagedByValue {
			return cloudsim.Run{}, ErrAmbiguousInventory
		}
		key := asset.Kind + "\x00" + asset.Role
		if _, duplicate := seenAssets[key]; duplicate {
			return cloudsim.Run{}, ErrAmbiguousInventory
		}
		seenAssets[key] = struct{}{}
	}
	createdAt, err := time.Parse(time.RFC3339, first.Tags[tagCreatedAt])
	if err != nil {
		return cloudsim.Run{}, ErrAmbiguousInventory
	}
	expiresAt, err := time.Parse(time.RFC3339, first.Tags[cloudsim.TagExpiresAt])
	if err != nil {
		return cloudsim.Run{}, ErrAmbiguousInventory
	}
	worstCost, err := strconv.ParseInt(first.Tags[tagWorstCost], 10, 64)
	if err != nil || worstCost <= 0 || first.Tags[tagCurrency] != "CNY" {
		return cloudsim.Run{}, ErrAmbiguousInventory
	}
	state := cloudsim.State(first.Tags[tagRunState])
	if !validReconciledState(state) || (state == cloudsim.StateReleased && len(assets) != 0) {
		return cloudsim.Run{}, ErrAmbiguousInventory
	}
	var activeUntil time.Time
	if raw := first.Tags[tagActiveUntil]; raw != "" {
		activeUntil, err = time.Parse(time.RFC3339, raw)
		if err != nil || activeUntil.After(expiresAt) {
			return cloudsim.Run{}, ErrAmbiguousInventory
		}
	}
	if expiresAt.After(p.now().UTC()) && missingComputeRole(assets) {
		state = cloudsim.StateInfrastructureInterrupted
	} else if !expiresAt.After(p.now().UTC()) {
		state = cloudsim.StateReleasePending
	} else if state == cloudsim.StateRunning && !activeUntil.IsZero() && !p.now().UTC().Before(activeUntil) {
		state = cloudsim.StateAnalysisGrace
	}
	return cloudsim.Run{
		ID: runID, Provider: ProviderName, Region: p.config.Region, AccountIDHash: first.Tags[tagAccountIDHash],
		Repository: first.Tags[cloudsim.TagRepository], State: state, CreatedAt: createdAt, ExpiresAt: expiresAt, ActiveUntil: activeUntil,
		Tags: maps.Clone(first.Tags), Resources: resourcesFromAssets(assets), Quote: cloudsim.Quote{
			Currency: first.Tags[tagCurrency], WorstCaseCostMicros: worstCost, SelectedSKU: first.Tags[tagSelectedSKU],
			CapacityAvailable: true, QuotaAvailable: true,
		},
	}, nil
}

func validReconciledState(state cloudsim.State) bool {
	switch state {
	case cloudsim.StateProvisioning, cloudsim.StateReady, cloudsim.StateRunning, cloudsim.StateAnalysisGrace,
		cloudsim.StateInfrastructureInterrupted, cloudsim.StateReleasePending, cloudsim.StateReleased:
		return true
	default:
		return false
	}
}

func missingComputeRole(assets []Asset) bool {
	present := make(map[string]bool, 4)
	for _, asset := range assets {
		if asset.Kind == "compute" {
			present[asset.Role] = true
		}
	}
	for _, role := range []string{"node-1", "node-2", "node-3", "sim"} {
		if !present[role] {
			return true
		}
	}
	return false
}

func resourcesFromAssets(assets []Asset) []cloudsim.Resource {
	resources := make([]cloudsim.Resource, 0, len(assets))
	for _, asset := range assets {
		resources = append(resources, cloudsim.Resource{
			ID: asset.ID, Kind: asset.Kind, Role: asset.Role, Billable: asset.Billable,
			PrivateAddress: asset.PrivateAddress, PublicAddress: asset.PublicAddress, Tags: maps.Clone(asset.Tags),
		})
	}
	return resources
}

func roleTags(base map[string]string, role string) map[string]string {
	tags := maps.Clone(base)
	tags[cloudsim.TagResourceRole] = role
	return tags
}

func assetID(assets []Asset, kind string) string {
	for _, asset := range assets {
		if asset.Kind == kind {
			return asset.ID
		}
	}
	return ""
}

func validateCreate(req cloudsim.CreateRequest, quote cloudsim.Quote, config Config) error {
	if req.Provider != ProviderName || req.Region != config.Region || req.AccountIDHash != config.AccountIDHash ||
		quote.Currency != "CNY" || quote.WorstCaseCostMicros <= 0 || strings.TrimSpace(quote.SelectedSKU) == "" ||
		!validSSHPublicKey(req.BootstrapSSHPublicKey) {
		return ErrInvalidConfig
	}
	for _, key := range cloudsim.MandatoryTagKeys() {
		if strings.TrimSpace(req.Tags[key]) == "" {
			return fmt.Errorf("%w: missing tag %s", ErrInvalidConfig, key)
		}
	}
	return nil
}

func validSSHPublicKey(value string) bool {
	value = strings.TrimSpace(value)
	return len(value) <= 2048 && (strings.HasPrefix(value, "ssh-ed25519 ") || strings.HasPrefix(value, "ssh-rsa "))
}

func validConfig(config Config) bool {
	if strings.TrimSpace(config.Region) == "" || strings.TrimSpace(config.ZoneID) == "" || strings.TrimSpace(config.ImageID) == "" ||
		strings.TrimSpace(config.AccountIDHash) == "" || strings.TrimSpace(config.SystemDiskCategory) == "" ||
		strings.TrimSpace(config.DataDiskCategory) == "" || config.SystemDiskSizeGiB <= 0 || config.DataDiskSizeGiB <= 0 ||
		config.PublicBandwidthMbps <= 0 || len(config.Presets) == 0 {
		return false
	}
	vpc, err := netip.ParsePrefix(config.VPCIPv4CIDR)
	if err != nil || !vpc.Addr().Is4() || vpc != vpc.Masked() {
		return false
	}
	subnet, err := netip.ParsePrefix(config.VSwitchIPv4CIDR)
	if err != nil || !subnet.Addr().Is4() || subnet != subnet.Masked() || !vpc.Contains(subnet.Addr()) {
		return false
	}
	seen := make(map[netip.Addr]struct{}, 4)
	for _, role := range []string{"node-1", "node-2", "node-3", "sim"} {
		address, parseErr := netip.ParseAddr(config.PrivateIPv4[role])
		if parseErr != nil || !address.Is4() || !subnet.Contains(address) || address == subnet.Addr() {
			return false
		}
		if _, duplicate := seen[address]; duplicate {
			return false
		}
		seen[address] = struct{}{}
	}
	if len(config.SimulatorSourceIPv4) == 0 || len(config.SimulatorSourceIPv4) > 3 || config.SimulatorSourceIPv4[0] != config.PrivateIPv4["sim"] {
		return false
	}
	for _, raw := range config.SimulatorSourceIPv4 {
		address, parseErr := netip.ParseAddr(raw)
		if parseErr != nil || !address.Is4() || !subnet.Contains(address) {
			return false
		}
		if raw == config.PrivateIPv4["sim"] {
			continue
		}
		if _, duplicate := seen[address]; duplicate {
			return false
		}
		seen[address] = struct{}{}
	}
	return true
}
