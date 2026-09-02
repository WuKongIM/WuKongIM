package alibaba

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudlease"
	"golang.org/x/crypto/ssh"
)

func TestLifecycleAcquireCreatesExactTaggedTopology(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	readAPI := completeReadAPI()
	lifecycleAPI := newLifecycleAPIStub()
	provider := NewLifecycle(readAPI, lifecycleAPI, Options{Now: func() time.Time { return now }})
	controller := cloudlease.NewController(provider, func() time.Time { return now })
	plan := approvedLifecyclePlan(now)

	quote, err := controller.Quote(context.Background(), plan)
	if err != nil {
		t.Fatalf("Quote() error = %v", err)
	}
	receipt, err := controller.AcquireWithBootstrap(context.Background(), plan, quote, lifecycleBootstrap(t))
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if receipt.State != cloudlease.StateActive || receipt.Zone != quote.Zone || len(receipt.Resources) == 0 {
		t.Fatalf("Acquire() receipt = %#v, want active provider-reconciled inventory", receipt)
	}
	if lifecycleAPI.networkCalls != 1 || len(lifecycleAPI.hostRequests) != 4 || lifecycleAPI.eipCalls != 1 || lifecycleAPI.associateCalls != 1 {
		t.Fatalf("mutation calls = network:%d hosts:%d eip:%d associate:%d, want 1/4/1/1",
			lifecycleAPI.networkCalls, len(lifecycleAPI.hostRequests), lifecycleAPI.eipCalls, lifecycleAPI.associateCalls)
	}
	roles := []string{"service", "service", "service", "load"}
	dataSizes := []int{500, 500, 500, 200}
	for index, request := range lifecycleAPI.hostRequests {
		if request.Role != roles[index] || request.Ordinal != index%3+1 && request.Role == "service" ||
			request.SystemDiskGiB != 40 || request.DataDiskGiB != dataSizes[index] ||
			request.PublicIPv4 || !request.AutoReleaseAt.Equal(plan.ExpiresAt) || len(request.BootstrapAuthorizedKeys) != 2 {
			t.Fatalf("host request %d = %#v", index, request)
		}
	}
	if request := lifecycleAPI.eipRequest; request.Role != "load" || request.PeakBandwidthMbps != 20 ||
		request.InternetChargeType != providerInternetPayTraffic {
		t.Fatalf("EIP request = %#v, want load 20 Mbps PayByTraffic", request)
	}
	if len(lifecycleAPI.accessRequests) != 3 {
		t.Fatalf("access requests = %#v, want private plus SSH and HTTP", lifecycleAPI.accessRequests)
	}
	if private := lifecycleAPI.accessRequests[0]; private.Kind != AccessRulePrivate || private.SourcePrefix.String() != "10.42.0.0/24" ||
		private.DestinationPrefix.String() != "0.0.0.0/0" {
		t.Fatalf("private access = %#v", private)
	}
	loadAddress := lifecycleAPI.addressForRole("load")
	for _, request := range lifecycleAPI.accessRequests[1:] {
		if request.Kind != AccessRuleGrant || request.TargetRole != "load" || request.SourcePrefix.String() != "0.0.0.0/0" ||
			request.DestinationPrefix.String() != loadAddress+"/32" || (request.PortFrom != 22 && request.PortFrom != 80) {
			t.Fatalf("public access request = %#v, want load-only 22/80", request)
		}
	}
	for _, resource := range receipt.Resources {
		for _, key := range cloudlease.MandatoryResourceTagKeys() {
			if resource.Tags[key] == "" {
				t.Fatalf("resource %s/%s missing tag %s", resource.Kind, resource.ID, key)
			}
		}
		if resource.Tags[cloudlease.TagPlanDigest] != quote.PlanDigest ||
			resource.Tags[cloudlease.TagExpiresAt] != plan.ExpiresAt.Format(time.RFC3339Nano) {
			t.Fatalf("resource %s immutable tags = %#v", resource.ID, resource.Tags)
		}
	}
}

func TestLifecycleAcquireRecoversPartialAmbiguousInventoryWithoutDuplicating(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	readAPI := completeReadAPI()
	lifecycleAPI := newLifecycleAPIStub()
	lifecycleAPI.failHostCall = 2
	provider := NewLifecycle(readAPI, lifecycleAPI, Options{Now: func() time.Time { return now }})
	controller := cloudlease.NewController(provider, func() time.Time { return now })
	plan := approvedLifecyclePlan(now)
	quote, err := controller.Quote(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}

	bootstrap := lifecycleBootstrap(t)
	receipt, err := controller.AcquireWithBootstrap(context.Background(), plan, quote, bootstrap)
	if !errors.Is(err, cloudlease.ErrAcquireIncomplete) || receipt.State != cloudlease.StateAcquiring {
		t.Fatalf("Acquire(partial) = %#v, %v, want acquiring ErrAcquireIncomplete", receipt, err)
	}
	if lifecycleAPI.networkCalls != 1 || len(lifecycleAPI.hostRequests) != 2 {
		t.Fatalf("partial mutation calls = network:%d hosts:%d", lifecycleAPI.networkCalls, len(lifecycleAPI.hostRequests))
	}

	_, err = controller.AcquireWithBootstrap(context.Background(), plan, quote, bootstrap)
	if !errors.Is(err, cloudlease.ErrAcquireIncomplete) {
		t.Fatalf("Acquire(retry) error = %v, want ErrAcquireIncomplete", err)
	}
	if lifecycleAPI.networkCalls != 1 || len(lifecycleAPI.hostRequests) != 2 {
		t.Fatalf("ambiguous retry duplicated resources: network:%d hosts:%d", lifecycleAPI.networkCalls, len(lifecycleAPI.hostRequests))
	}
}

func TestLifecycleAcquireRetryRejectsDifferentBootstrapIdentity(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	provider := NewLifecycle(completeReadAPI(), newLifecycleAPIStub(), Options{Now: func() time.Time { return now }})
	controller := cloudlease.NewController(provider, func() time.Time { return now })
	plan := approvedLifecyclePlan(now)
	quote, err := controller.Quote(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.AcquireWithBootstrap(context.Background(), plan, quote, lifecycleBootstrap(t)); err != nil {
		t.Fatal(err)
	}
	different := lifecycleBootstrap(t)
	private := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{9}, ed25519.SeedSize))
	publicKey, err := ssh.NewPublicKey(private.Public())
	if err != nil {
		t.Fatal(err)
	}
	different.AuthorizedKeys[1] = string(ssh.MarshalAuthorizedKey(publicKey))
	if _, err := controller.AcquireWithBootstrap(context.Background(), plan, quote, different); !errors.Is(err, cloudlease.ErrLeaseConflict) {
		t.Fatalf("AcquireWithBootstrap(changed keys) error = %v, want ErrLeaseConflict", err)
	}
}

func TestLifecycleGrantAndRevokeAccessRemainTargetConstrained(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	controller, lifecycleAPI, plan, quote := acquiredLifecycleLease(t, now, Options{Now: func() time.Time { return now }})
	selector := lifecycleSelector(plan, quote)
	grant := cloudlease.AccessGrant{
		ID: "load-extra", TargetRole: "load", Protocol: cloudlease.ProtocolTCP,
		PortFrom: 443, PortTo: 443, SourcePrefix: netip.MustParsePrefix("203.0.113.8/32"), Until: plan.ExpiresAt,
	}

	receipt, err := controller.GrantAccess(context.Background(), selector, grant)
	if err != nil {
		t.Fatalf("GrantAccess() error = %v", err)
	}
	if receipt.State != cloudlease.StateActive || !slices.ContainsFunc(receipt.AccessGrants, func(value cloudlease.AccessGrant) bool { return value == grant }) {
		t.Fatalf("GrantAccess() receipt = %#v", receipt)
	}
	request := lifecycleAPI.accessRequests[len(lifecycleAPI.accessRequests)-1]
	if request.DestinationPrefix.String() != lifecycleAPI.addressForRole("load")+"/32" || request.PortFrom != 443 {
		t.Fatalf("GrantAccess() request = %#v", request)
	}

	receipt, err = controller.RevokeAccess(context.Background(), selector, grant.ID)
	if err != nil {
		t.Fatalf("RevokeAccess() error = %v", err)
	}
	if slices.ContainsFunc(receipt.AccessGrants, func(value cloudlease.AccessGrant) bool { return value.ID == grant.ID }) {
		t.Fatalf("RevokeAccess() retained grant: %#v", receipt.AccessGrants)
	}
}

func TestLifecycleReleaseDeletesDependenciesInOrderAndProvesZeroInventory(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	options := Options{
		Now: func() time.Time { return now }, ReleaseTimeout: 10 * time.Second,
		ReleasePollInterval: time.Second, Wait: func(context.Context, time.Duration) error { return nil },
	}
	controller, lifecycleAPI, plan, quote := acquiredLifecycleLease(t, now, options)
	base := maps.Clone(lifecycleAPI.assets[0].Tags)
	lifecycleAPI.assets = append(lifecycleAPI.assets,
		stubAsset("nat-1", ResourceKindNATGateway, "network", "vpc-1", base),
		LifecycleAsset{ID: "route-1", Kind: ResourceKindRouteEntry, Role: "network", ParentID: "route-table-1",
			Tags: maps.Clone(base), Attributes: map[string]string{"vpc_id": "vpc-1"}},
	)

	result, err := controller.Release(context.Background(), lifecycleSelector(plan, quote))
	if err != nil || result.ZeroInventory == nil || len(lifecycleAPI.assets) != 0 {
		t.Fatalf("Release() = %#v, %v, residual=%#v", result, err, lifecycleAPI.assets)
	}
	if !slices.IsSorted(result.ZeroInventory.Scopes) || !slices.Contains(result.ZeroInventory.Scopes, "nat_gateways") ||
		!slices.Contains(result.ZeroInventory.Scopes, "security_group_rules") {
		t.Fatalf("zero scopes = %#v", result.ZeroInventory.Scopes)
	}
	for index := 1; index < len(lifecycleAPI.deleted); index++ {
		if lifecycleDeleteRank(lifecycleAPI.deleted[index-1].Kind) > lifecycleDeleteRank(lifecycleAPI.deleted[index].Kind) {
			t.Fatalf("delete order = %#v", lifecycleAPI.deleted)
		}
	}
}

func TestLifecycleInspectClassifiesWrongRelationshipForCleanup(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	controller, lifecycleAPI, plan, quote := acquiredLifecycleLease(t, now, Options{Now: func() time.Time { return now }})
	for index := range lifecycleAPI.assets {
		if lifecycleAPI.assets[index].Kind == ResourceKindEIPAssociation {
			lifecycleAPI.assets[index].ParentID = "instance-1"
		}
	}
	receipt, err := controller.Inspect(context.Background(), lifecycleSelector(plan, quote))
	if err != nil || receipt.State != cloudlease.StateReleasePending {
		t.Fatalf("Inspect(wrong EIP target) = %#v, %v, want release_pending", receipt, err)
	}
}

func TestLifecycleInspectClassifiesChildRoleConflictForCleanup(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	controller, lifecycleAPI, plan, quote := acquiredLifecycleLease(t, now, Options{Now: func() time.Time { return now }})
	for index := range lifecycleAPI.assets {
		if lifecycleAPI.assets[index].Kind == ResourceKindDisk {
			lifecycleAPI.assets[index].Role = "foreign-role"
			lifecycleAPI.assets[index].Tags[cloudlease.TagResourceRole] = "foreign-role"
			break
		}
	}
	receipt, err := controller.Inspect(context.Background(), lifecycleSelector(plan, quote))
	if err != nil || receipt.State != cloudlease.StateReleasePending {
		t.Fatalf("Inspect(child role conflict) = %#v, %v, want release_pending", receipt, err)
	}
}

func TestLifecycleReleaseReturnsResidualThenContinuesToZero(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	options := Options{
		Now: func() time.Time { return now }, ReleaseTimeout: 5 * time.Second,
		ReleasePollInterval: 5 * time.Second, Wait: func(context.Context, time.Duration) error { return nil },
	}
	controller, lifecycleAPI, plan, quote := acquiredLifecycleLease(t, now, options)
	lifecycleAPI.deleteFailures["vpc-1"] = -1
	selector := lifecycleSelector(plan, quote)

	result, err := controller.Release(context.Background(), selector)
	if !errors.Is(err, cloudlease.ErrResidualResources) || result.Receipt == nil || result.Receipt.State != cloudlease.StateReleasePending {
		t.Fatalf("Release(residual) = %#v, %v", result, err)
	}
	if len(result.Receipt.Resources) != 1 || result.Receipt.Resources[0].Kind != ResourceKindVPC {
		t.Fatalf("residual receipt = %#v, want only VPC", result.Receipt)
	}

	delete(lifecycleAPI.deleteFailures, "vpc-1")
	result, err = controller.Release(context.Background(), selector)
	if err != nil || result.ZeroInventory == nil || len(lifecycleAPI.assets) != 0 {
		t.Fatalf("Release(retry) = %#v, %v, residual=%#v", result, err, lifecycleAPI.assets)
	}
}

func acquiredLifecycleLease(t *testing.T, now time.Time, options Options) (*cloudlease.Controller, *lifecycleAPIStub, cloudlease.Plan, cloudlease.Quote) {
	t.Helper()
	readAPI := completeReadAPI()
	lifecycleAPI := newLifecycleAPIStub()
	provider := NewLifecycle(readAPI, lifecycleAPI, options)
	controller := cloudlease.NewController(provider, options.Now)
	plan := approvedLifecyclePlan(now)
	quote, err := controller.Quote(context.Background(), plan)
	if err != nil {
		t.Fatalf("Quote() error = %v", err)
	}
	if _, err := controller.AcquireWithBootstrap(context.Background(), plan, quote, lifecycleBootstrap(t)); err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	return controller, lifecycleAPI, plan, quote
}

func lifecycleBootstrap(t *testing.T) cloudlease.BootstrapAccess {
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
	return cloudlease.BootstrapAccess{AuthorizedKeys: keys}
}

func lifecycleSelector(plan cloudlease.Plan, quote cloudlease.Quote) cloudlease.Selector {
	return cloudlease.Selector{
		LeaseID: plan.LeaseID, RequestID: plan.RequestID, Provider: plan.Provider,
		Region: plan.Region, Repository: plan.Repository, PlanDigest: quote.PlanDigest,
	}
}

func approvedLifecyclePlan(now time.Time) cloudlease.Plan {
	plan := approvedPlan(now)
	plan.Network.InitialAccess = []cloudlease.AccessGrant{
		{
			ID: "load-ssh", TargetRole: "load", Protocol: cloudlease.ProtocolTCP,
			PortFrom: 22, PortTo: 22, SourcePrefix: netip.MustParsePrefix("0.0.0.0/0"), Until: plan.ExpiresAt,
		},
		{
			ID: "load-http", TargetRole: "load", Protocol: cloudlease.ProtocolTCP,
			PortFrom: 80, PortTo: 80, SourcePrefix: netip.MustParsePrefix("0.0.0.0/0"), Until: plan.ExpiresAt,
		},
	}
	return plan
}

type lifecycleAPIStub struct {
	assets           []LifecycleAsset
	networkCalls     int
	hostRequests     []HostCreateRequest
	eipCalls         int
	eipRequest       PublicAddressCreateRequest
	associateCalls   int
	accessRequests   []AccessRuleRequest
	lifecycleUpdates []string
	deleted          []LifecycleAsset
	failHostCall     int
	deleteFailures   map[string]int
}

func newLifecycleAPIStub() *lifecycleAPIStub {
	return &lifecycleAPIStub{deleteFailures: make(map[string]int)}
}

func (s *lifecycleAPIStub) ListAssets(_ context.Context, query InventoryQuery) ([]LifecycleAsset, error) {
	result := make([]LifecycleAsset, 0, len(s.assets))
	for _, asset := range s.assets {
		if query.LeaseID != "" && asset.Tags[cloudlease.TagLeaseID] != query.LeaseID {
			continue
		}
		if query.Repository != "" && asset.Tags[cloudlease.TagRepository] != query.Repository {
			continue
		}
		result = append(result, cloneLifecycleAsset(asset))
	}
	return result, nil
}

func (s *lifecycleAPIStub) CreateNetwork(_ context.Context, request NetworkCreateRequest) ([]LifecycleAsset, error) {
	s.networkCalls++
	created := []LifecycleAsset{
		stubAsset("vpc-1", ResourceKindVPC, "network", "", request.Tags),
		stubAsset("vsw-1", ResourceKindVSwitch, "network", "vpc-1", request.Tags),
		stubAsset("sg-1", ResourceKindSecurityGroup, "network", "vpc-1", request.Tags),
	}
	s.assets = append(s.assets, created...)
	return cloneLifecycleAssets(created), nil
}

func (s *lifecycleAPIStub) CreateHost(_ context.Context, request HostCreateRequest) ([]LifecycleAsset, error) {
	s.hostRequests = append(s.hostRequests, request)
	index := len(s.hostRequests)
	instanceID := fmt.Sprintf("instance-%d", index)
	address := fmt.Sprintf("10.42.0.%d", 9+index)
	created := []LifecycleAsset{
		stubAsset(instanceID, ResourceKindInstance, request.Role, "vsw-1", request.Tags),
		stubAsset(fmt.Sprintf("system-disk-%d", index), ResourceKindDisk, request.Role, instanceID, request.Tags),
		stubAsset(fmt.Sprintf("data-disk-%d", index), ResourceKindDisk, request.Role, instanceID, request.Tags),
		stubAsset(fmt.Sprintf("eni-%d", index), ResourceKindENI, request.Role, instanceID, request.Tags),
		stubAsset(fmt.Sprintf("data-attach-%d", index), ResourceKindDiskAttachment, request.Role, instanceID, request.Tags),
	}
	created[0].PrivateAddress = address
	created[1].SizeBytes = int64(request.SystemDiskGiB) << 30
	created[1].Attributes = map[string]string{"disk_type": "system"}
	created[2].SizeBytes = int64(request.DataDiskGiB) << 30
	created[2].Attributes = map[string]string{"disk_type": "data"}
	created[4].Attributes = map[string]string{"disk_id": created[2].ID}
	s.assets = append(s.assets, created...)
	if s.failHostCall == index {
		return nil, errors.New("ambiguous host create")
	}
	return cloneLifecycleAssets(created), nil
}

func (s *lifecycleAPIStub) CreatePublicAddress(_ context.Context, request PublicAddressCreateRequest) (LifecycleAsset, error) {
	s.eipCalls++
	s.eipRequest = request
	asset := stubAsset("eip-1", ResourceKindEIP, request.Role, "", request.Tags)
	asset.PublicAddress = "198.51.100.10"
	s.assets = append(s.assets, asset)
	return cloneLifecycleAsset(asset), nil
}

func (s *lifecycleAPIStub) AssociatePublicAddress(_ context.Context, request PublicAddressAssociationRequest) error {
	s.associateCalls++
	asset := stubAsset("eip-association-1", ResourceKindEIPAssociation, request.Role, request.InstanceID, request.Tags)
	asset.Attributes = map[string]string{"eip_id": request.AllocationID}
	s.assets = append(s.assets, asset)
	return nil
}

func (s *lifecycleAPIStub) SetAccessRule(_ context.Context, request AccessRuleRequest) error {
	s.accessRequests = append(s.accessRequests, request)
	assetID := "rule-" + request.ID
	if request.Remove {
		for index := range s.assets {
			if s.assets[index].ID == assetID {
				s.assets = slices.Delete(s.assets, index, index+1)
				break
			}
		}
		return nil
	}
	for _, existing := range s.assets {
		if existing.ID == assetID {
			return nil
		}
	}
	asset := stubAsset(assetID, ResourceKindSecurityRule, request.TargetRole, request.SecurityGroupID, request.Tags)
	asset.Grant = request.Grant
	asset.Attributes = map[string]string{"rule_kind": string(request.Kind)}
	s.assets = append(s.assets, asset)
	return nil
}

func (s *lifecycleAPIStub) SetLifecycleState(_ context.Context, query InventoryQuery, state string) error {
	s.lifecycleUpdates = append(s.lifecycleUpdates, state)
	for index := range s.assets {
		if s.assets[index].Tags[cloudlease.TagLeaseID] == query.LeaseID {
			s.assets[index].Tags[lifecycleStateTag] = state
		}
	}
	return nil
}

func (s *lifecycleAPIStub) DeleteAsset(_ context.Context, asset LifecycleAsset) error {
	s.deleted = append(s.deleted, cloneLifecycleAsset(asset))
	if remaining := s.deleteFailures[asset.ID]; remaining != 0 {
		if remaining > 0 {
			s.deleteFailures[asset.ID] = remaining - 1
		}
		return errors.New("injected delete failure")
	}
	for index := range s.assets {
		if s.assets[index].ID == asset.ID {
			s.assets = slices.Delete(s.assets, index, index+1)
			break
		}
	}
	return nil
}

func (s *lifecycleAPIStub) addressForRole(role string) string {
	for _, asset := range s.assets {
		if asset.Kind == ResourceKindInstance && asset.Role == role {
			return asset.PrivateAddress
		}
	}
	return ""
}

func stubAsset(id, kind, role, parent string, tags map[string]string) LifecycleAsset {
	resourceTags := maps.Clone(tags)
	resourceTags[cloudlease.TagResourceRole] = role
	return LifecycleAsset{ID: id, Kind: kind, Role: role, ParentID: parent, Tags: resourceTags}
}
