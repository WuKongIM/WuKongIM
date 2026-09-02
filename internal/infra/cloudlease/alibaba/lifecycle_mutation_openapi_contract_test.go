package alibaba

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudlease"
)

func TestLifecycleOpenAPICreatesNetworkAndPublicAddressAtExactProviderBoundary(t *testing.T) {
	var mutex sync.Mutex
	var calls []identityOpenAPICall
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		action, form := identityOpenAPIRequest(t, request)
		mutex.Lock()
		calls = append(calls, identityOpenAPICall{action: action, form: form})
		mutex.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		var response any
		switch action {
		case "CreateVpc":
			response = map[string]any{"RequestId": "request-vpc", "VpcId": "vpc-lease"}
		case "DescribeVpcAttribute":
			response = map[string]any{"RequestId": "request-vpc-ready", "VpcId": "vpc-lease", "Status": "Available"}
		case "CreateVSwitch":
			response = map[string]any{"RequestId": "request-vswitch", "VSwitchId": "vsw-lease"}
		case "DescribeVSwitchAttributes":
			response = map[string]any{"RequestId": "request-vswitch-ready", "VSwitchId": "vsw-lease", "Status": "Available"}
		case "CreateSecurityGroup":
			response = map[string]any{"RequestId": "request-group", "SecurityGroupId": "sg-lease"}
		case "AllocateEipAddress":
			response = map[string]any{"RequestId": "request-eip", "AllocationId": "eip-lease", "EipAddress": "198.51.100.10"}
		case "AssociateEipAddress":
			response = map[string]any{"RequestId": "request-associate"}
		default:
			t.Errorf("unexpected lifecycle action %q with form %#v", action, form)
			response = map[string]any{"RequestId": "unexpected", "Code": "UnexpectedAction"}
		}
		_ = json.NewEncoder(writer).Encode(response)
	})
	api := newLifecycleOpenAPITestClient(t, handler)
	tags := lifecycleOpenAPIContractTags()

	assets, err := api.CreateNetwork(context.Background(), NetworkCreateRequest{
		Region: RegionHangzhou, Zone: "cn-hangzhou-h", VPCIPv4CIDR: "10.42.0.0/16",
		VSwitchIPv4CIDR: "10.42.0.0/24", ClientToken: "create-network-token", Tags: tags,
	})
	if err != nil {
		t.Fatalf("CreateNetwork() error = %v", err)
	}
	if len(assets) != 3 || assets[0].ID != "vpc-lease" || assets[0].Kind != ResourceKindVPC ||
		assets[1].ID != "vsw-lease" || assets[1].ParentID != "vpc-lease" ||
		assets[2].ID != "sg-lease" || assets[2].ParentID != "vpc-lease" {
		t.Fatalf("CreateNetwork() assets = %#v", assets)
	}
	for _, asset := range assets {
		if asset.Role != "network" || asset.Tags[cloudlease.TagLeaseID] != tags[cloudlease.TagLeaseID] ||
			asset.Tags[cloudlease.TagResourceRole] != "network" {
			t.Fatalf("network asset identity = %#v", asset)
		}
	}

	eip, err := api.CreatePublicAddress(context.Background(), PublicAddressCreateRequest{
		Region: RegionHangzhou, Role: "load", PeakBandwidthMbps: 25,
		InternetChargeType: providerInternetPayTraffic, ClientToken: "allocate-eip-token", Tags: tags,
	})
	if err != nil {
		t.Fatalf("CreatePublicAddress() error = %v", err)
	}
	if eip.ID != "eip-lease" || eip.Kind != ResourceKindEIP || eip.Role != "load" || !eip.Billable ||
		eip.PublicAddress != "198.51.100.10" || eip.Tags[cloudlease.TagResourceRole] != "load" {
		t.Fatalf("CreatePublicAddress() asset = %#v", eip)
	}
	if err := api.AssociatePublicAddress(context.Background(), PublicAddressAssociationRequest{
		Region: RegionHangzhou, Role: "load", AllocationID: eip.ID, InstanceID: "i-load", ClientToken: "associate-token", Tags: tags,
	}); err != nil {
		t.Fatalf("AssociatePublicAddress() error = %v", err)
	}

	mutex.Lock()
	gotCalls := append([]identityOpenAPICall(nil), calls...)
	mutex.Unlock()
	for _, want := range []string{"CreateVpc", "DescribeVpcAttribute", "CreateVSwitch", "DescribeVSwitchAttributes", "CreateSecurityGroup", "AllocateEipAddress", "AssociateEipAddress"} {
		if countLifecycleAction(gotCalls, want) != 1 {
			t.Fatalf("action %s calls = %d, calls %#v", want, countLifecycleAction(gotCalls, want), identityOpenAPIActions(gotCalls))
		}
	}
	if form := identityOpenAPIForm(gotCalls, "CreateVpc"); form.Get("CidrBlock") != "10.42.0.0/16" || form.Get("ClientToken") != "create-network-token" {
		t.Fatalf("CreateVpc form = %#v", form)
	}
	if form := identityOpenAPIForm(gotCalls, "CreateVSwitch"); form.Get("VpcId") != "vpc-lease" || form.Get("CidrBlock") != "10.42.0.0/24" || form.Get("ZoneId") != "cn-hangzhou-h" {
		t.Fatalf("CreateVSwitch form = %#v", form)
	}
	if form := identityOpenAPIForm(gotCalls, "CreateSecurityGroup"); form.Get("VpcId") != "vpc-lease" || form.Get("SecurityGroupType") != "normal" {
		t.Fatalf("CreateSecurityGroup form = %#v", form)
	}
	if form := identityOpenAPIForm(gotCalls, "AllocateEipAddress"); form.Get("InstanceChargeType") != providerBillingPostPaid ||
		form.Get("InternetChargeType") != providerInternetPayTraffic || form.Get("Bandwidth") != "25" || form.Get("ClientToken") != "allocate-eip-token" {
		t.Fatalf("AllocateEipAddress form = %#v", form)
	}
	if form := identityOpenAPIForm(gotCalls, "AssociateEipAddress"); form.Get("AllocationId") != "eip-lease" ||
		form.Get("InstanceId") != "i-load" || form.Get("InstanceType") != "EcsInstance" || form.Get("ClientToken") != "associate-token" {
		t.Fatalf("AssociateEipAddress form = %#v", form)
	}
}

func TestLifecycleOpenAPICreatesPrivateHostAndDiscoversExactBillableChildren(t *testing.T) {
	var mutex sync.Mutex
	var calls []identityOpenAPICall
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		action, form := identityOpenAPIRequest(t, request)
		mutex.Lock()
		calls = append(calls, identityOpenAPICall{action: action, form: form})
		mutex.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		var response any
		switch action {
		case "RunInstances":
			response = map[string]any{"RequestId": "request-run", "InstanceIdSets": map[string]any{"InstanceIdSet": []string{"i-service"}}}
		case "DescribeInstances":
			response = map[string]any{"RequestId": "request-instance", "TotalCount": 1, "Instances": map[string]any{"Instance": []any{map[string]any{
				"InstanceId": "i-service", "Status": "Running",
				"VpcAttributes": map[string]any{"PrivateIpAddress": map[string]any{"IpAddress": []string{"10.42.0.10"}}},
			}}}}
		case "DescribeDisks":
			response = map[string]any{"RequestId": "request-disks", "TotalCount": 2, "Disks": map[string]any{"Disk": []any{
				map[string]any{"DiskId": "disk-system", "InstanceId": "i-service", "Type": "system", "Category": providerDiskESSD, "PerformanceLevel": providerDiskLevelPL0, "Size": 40},
				map[string]any{"DiskId": "disk-data", "InstanceId": "i-service", "Type": "data", "Category": providerDiskESSD, "PerformanceLevel": providerDiskLevelPL0, "Size": 200},
			}}}
		case "DescribeNetworkInterfaces":
			response = map[string]any{"RequestId": "request-eni", "NextToken": "", "NetworkInterfaceSets": map[string]any{"NetworkInterfaceSet": []any{map[string]any{
				"NetworkInterfaceId": "eni-service", "InstanceId": "i-service", "PrivateIpAddress": "10.42.0.10", "Type": "Primary",
			}}}}
		case "TagResources":
			response = map[string]any{"RequestId": "request-tag"}
		default:
			t.Errorf("unexpected host action %q with form %#v", action, form)
			response = map[string]any{"RequestId": "unexpected", "Code": "UnexpectedAction"}
		}
		_ = json.NewEncoder(writer).Encode(response)
	})
	api := newLifecycleOpenAPITestClient(t, handler)
	autoReleaseAt := time.Date(2026, 9, 2, 15, 4, 5, 999, time.UTC)
	request := HostCreateRequest{
		Region: RegionHangzhou, Zone: "cn-hangzhou-h", Role: "service", Ordinal: 1,
		InstanceType: "ecs.g8i.xlarge", ImageID: "ubuntu-24", VSwitchID: "vsw-lease", SecurityGroupID: "sg-lease",
		SystemDiskGiB: 40, DataDiskGiB: 200, AutoReleaseAt: autoReleaseAt, ClientToken: "create-service-token",
		BootstrapAuthorizedKeys: lifecycleBootstrap(t).AuthorizedKeys, Tags: lifecycleOpenAPIContractTags(),
	}

	assets, err := api.CreateHost(context.Background(), request)
	if err != nil {
		t.Fatalf("CreateHost() error = %v", err)
	}
	if len(assets) != 5 {
		t.Fatalf("CreateHost() assets = %#v", assets)
	}
	byKind := make(map[string][]LifecycleAsset)
	for _, asset := range assets {
		byKind[asset.Kind] = append(byKind[asset.Kind], asset)
	}
	instance := byKind[ResourceKindInstance][0]
	if instance.ID != "i-service" || instance.ParentID != "vsw-lease" || instance.PrivateAddress != "10.42.0.10" || !instance.Billable ||
		instance.Attributes["instance_type"] != request.InstanceType || instance.Attributes["image_id"] != request.ImageID || instance.Attributes["ordinal"] != "1" {
		t.Fatalf("instance asset = %#v", instance)
	}
	if len(byKind[ResourceKindDisk]) != 2 || !byKind[ResourceKindDisk][0].Billable || !byKind[ResourceKindDisk][1].Billable {
		t.Fatalf("disk assets = %#v", byKind[ResourceKindDisk])
	}
	if got := byKind[ResourceKindDiskAttachment][0]; got.ParentID != "i-service" || got.Attributes["disk_id"] != "disk-data" {
		t.Fatalf("disk attachment = %#v", got)
	}
	if got := byKind[ResourceKindENI][0]; got.ID != "eni-service" || got.ParentID != "i-service" || got.PrivateAddress != "10.42.0.10" {
		t.Fatalf("ENI asset = %#v", got)
	}

	mutex.Lock()
	gotCalls := append([]identityOpenAPICall(nil), calls...)
	mutex.Unlock()
	if countLifecycleAction(gotCalls, "RunInstances") != 1 || countLifecycleAction(gotCalls, "DescribeInstances") != 1 ||
		countLifecycleAction(gotCalls, "DescribeDisks") != 1 || countLifecycleAction(gotCalls, "DescribeNetworkInterfaces") != 1 ||
		countLifecycleAction(gotCalls, "TagResources") != 2 {
		t.Fatalf("host action counts = %#v", identityOpenAPIActions(gotCalls))
	}
	form := identityOpenAPIForm(gotCalls, "RunInstances")
	if form.Get("InstanceChargeType") != providerBillingPostPaid || form.Get("SpotStrategy") != providerSpotNoSpot ||
		form.Get("InternetMaxBandwidthOut") != "0" || form.Get("Amount") != "1" || form.Get("MinAmount") != "1" ||
		form.Get("VSwitchId") != "vsw-lease" || form.Get("SecurityGroupId") != "sg-lease" ||
		form.Get("SystemDisk.Category") != providerDiskESSD || form.Get("SystemDisk.PerformanceLevel") != providerDiskLevelPL0 ||
		form.Get("SystemDisk.Size") != "40" || form.Get("DataDisk.1.Category") != providerDiskESSD ||
		form.Get("DataDisk.1.PerformanceLevel") != providerDiskLevelPL0 || form.Get("DataDisk.1.Size") != "200" ||
		form.Get("DataDisk.1.DeleteWithInstance") != "true" || form.Get("AutoReleaseTime") != "2026-09-02T15:04:00Z" ||
		form.Get("ClientToken") != "create-service-token" {
		t.Fatalf("RunInstances form = %#v", form)
	}
	cloudInit, err := base64.StdEncoding.DecodeString(form.Get("UserData"))
	if err != nil || !strings.Contains(string(cloudInit), "name: wkdeploy") || strings.Contains(string(cloudInit), "ssh_pwauth: true") {
		t.Fatalf("RunInstances UserData = %q, %v", cloudInit, err)
	}
	resourceTypes := make(map[string]int)
	for _, call := range gotCalls {
		if call.action == "TagResources" {
			resourceTypes[call.form.Get("ResourceType")]++
		}
	}
	if resourceTypes["disk"] != 1 || resourceTypes["eni"] != 1 {
		t.Fatalf("tag resource types = %#v", resourceTypes)
	}
}

func TestLifecycleOpenAPIAccessRuleConvergesIdempotentlyAndRemovesExactOwnedRule(t *testing.T) {
	var mutex sync.Mutex
	var calls []identityOpenAPICall
	var rulePresent bool
	var description string
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		action, form := identityOpenAPIRequest(t, request)
		mutex.Lock()
		calls = append(calls, identityOpenAPICall{action: action, form: form})
		writer.Header().Set("Content-Type", "application/json")
		var response any
		switch action {
		case "DescribeSecurityGroupAttribute":
			permissions := []any{}
			if rulePresent {
				permissions = append(permissions, map[string]any{
					"SecurityGroupRuleId": "rule-load-http", "Description": description,
					"IpProtocol": "TCP", "PortRange": "80/80", "SourceCidrIp": "203.0.113.0/24", "DestCidrIp": "10.42.0.10/32",
				})
			}
			response = map[string]any{"RequestId": "request-describe-rule", "NextToken": "", "Permissions": map[string]any{"Permission": permissions}}
		case "AuthorizeSecurityGroup":
			description = form.Get("Permissions.1.Description")
			rulePresent = true
			response = map[string]any{"RequestId": "request-authorize"}
		case "RevokeSecurityGroup":
			rulePresent = false
			response = map[string]any{"RequestId": "request-revoke"}
		default:
			t.Errorf("unexpected access-rule action %q with form %#v", action, form)
			response = map[string]any{"RequestId": "unexpected", "Code": "UnexpectedAction"}
		}
		mutex.Unlock()
		_ = json.NewEncoder(writer).Encode(response)
	})
	api := newLifecycleOpenAPITestClient(t, handler)
	request := AccessRuleRequest{
		Region: RegionHangzhou, Kind: AccessRuleGrant, ID: "load-http", SecurityGroupID: "sg-lease", TargetRole: "load",
		Protocol: cloudlease.ProtocolTCP, PortFrom: 80, PortTo: 80,
		SourcePrefix: netip.MustParsePrefix("203.0.113.0/24"), DestinationPrefix: netip.MustParsePrefix("10.42.0.10/32"),
		Until: time.Date(2026, 9, 2, 18, 30, 0, 123, time.UTC), Tags: lifecycleOpenAPIContractTags(),
	}

	if err := api.SetAccessRule(context.Background(), request); err != nil {
		t.Fatalf("SetAccessRule(create) error = %v", err)
	}
	if description == "" {
		t.Fatal("provider rule did not receive an ownership description")
	}
	decoded, ok := parseSecurityRuleDescription(description)
	if !ok || decoded.LeaseID != request.Tags[cloudlease.TagLeaseID] || decoded.ID != request.ID || decoded.TargetRole != request.TargetRole ||
		decoded.PortFrom != 80 || decoded.PortTo != 80 || decoded.Source != request.SourcePrefix.String() ||
		decoded.Destination != request.DestinationPrefix.String() || decoded.UntilNanosecond != 123 {
		t.Fatalf("owned rule description = %#v/%v", decoded, ok)
	}
	if err := api.SetAccessRule(context.Background(), request); err != nil {
		t.Fatalf("SetAccessRule(idempotent create) error = %v", err)
	}
	request.Remove = true
	if err := api.SetAccessRule(context.Background(), request); err != nil {
		t.Fatalf("SetAccessRule(remove) error = %v", err)
	}

	mutex.Lock()
	gotCalls := append([]identityOpenAPICall(nil), calls...)
	stillPresent := rulePresent
	mutex.Unlock()
	if stillPresent || countLifecycleAction(gotCalls, "AuthorizeSecurityGroup") != 1 || countLifecycleAction(gotCalls, "RevokeSecurityGroup") != 1 ||
		countLifecycleAction(gotCalls, "DescribeSecurityGroupAttribute") != 5 {
		t.Fatalf("access-rule convergence = present:%v calls:%#v", stillPresent, identityOpenAPIActions(gotCalls))
	}
	authorize := identityOpenAPIForm(gotCalls, "AuthorizeSecurityGroup")
	if authorize.Get("RegionId") != RegionHangzhou || authorize.Get("SecurityGroupId") != "sg-lease" ||
		authorize.Get("Permissions.1.IpProtocol") != "TCP" || authorize.Get("Permissions.1.PortRange") != "80/80" ||
		authorize.Get("Permissions.1.SourceCidrIp") != "203.0.113.0/24" || authorize.Get("Permissions.1.DestCidrIp") != "10.42.0.10/32" ||
		authorize.Get("Permissions.1.Policy") != "accept" || authorize.Get("Permissions.1.Priority") != "1" || authorize.Get("ClientToken") == "" {
		t.Fatalf("AuthorizeSecurityGroup form = %#v", authorize)
	}
	revoke := identityOpenAPIForm(gotCalls, "RevokeSecurityGroup")
	if revoke.Get("SecurityGroupId") != "sg-lease" || revoke.Get("SecurityGroupRuleId.1") != "rule-load-http" {
		t.Fatalf("RevokeSecurityGroup form = %#v", revoke)
	}
}

func TestLifecycleOpenAPIDeleteMapsEveryInventoryKindAndTreatsAbsenceAsSuccess(t *testing.T) {
	var mutex sync.Mutex
	var calls []identityOpenAPICall
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		action, form := identityOpenAPIRequest(t, request)
		mutex.Lock()
		calls = append(calls, identityOpenAPICall{action: action, form: form})
		mutex.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		if action == "DeleteDisk" && form.Get("DiskId") == "disk-absent" {
			writeIdentityOpenAPIError(writer, http.StatusNotFound, "InvalidDiskId.NotFound", "disk already absent")
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"RequestId": "request-delete"})
	})
	api := newLifecycleOpenAPITestClient(t, handler)
	assets := []LifecycleAsset{
		{ID: "rule-1", Kind: ResourceKindSecurityRule, ParentID: "sg-1"},
		{ID: "route-1", Kind: ResourceKindRouteEntry},
		{ID: "association-1", Kind: ResourceKindEIPAssociation, ParentID: "i-1", Attributes: map[string]string{"eip_id": "eip-1"}},
		{ID: "eip-1", Kind: ResourceKindEIP},
		{ID: "attachment-1", Kind: ResourceKindDiskAttachment, ParentID: "i-1", Attributes: map[string]string{"disk_id": "disk-data"}},
		{ID: "i-1", Kind: ResourceKindInstance},
		{ID: "disk-1", Kind: ResourceKindDisk},
		{ID: "eni-1", Kind: ResourceKindENI},
		{ID: "sg-1", Kind: ResourceKindSecurityGroup},
		{ID: "nat-1", Kind: ResourceKindNATGateway},
		{ID: "vsw-1", Kind: ResourceKindVSwitch},
		{ID: "vpc-1", Kind: ResourceKindVPC},
	}
	for _, asset := range assets {
		if err := api.DeleteAsset(context.Background(), asset); err != nil {
			t.Fatalf("DeleteAsset(%s/%s) error = %v", asset.Kind, asset.ID, err)
		}
	}
	if err := api.DeleteAsset(context.Background(), LifecycleAsset{ID: "disk-absent", Kind: ResourceKindDisk}); err != nil {
		t.Fatalf("DeleteAsset(already absent) error = %v", err)
	}

	mutex.Lock()
	gotCalls := append([]identityOpenAPICall(nil), calls...)
	mutex.Unlock()
	wantActions := []string{
		"RevokeSecurityGroup", "DeleteRouteEntry", "UnassociateEipAddress", "ReleaseEipAddress", "DetachDisk", "DeleteInstance",
		"DeleteDisk", "DeleteNetworkInterface", "DeleteSecurityGroup", "DeleteNatGateway", "DeleteVSwitch", "DeleteVpc", "DeleteDisk",
	}
	gotActions := make([]string, 0, len(gotCalls))
	for _, call := range gotCalls {
		gotActions = append(gotActions, call.action)
	}
	if !reflect.DeepEqual(gotActions, wantActions) {
		t.Fatalf("delete actions = %#v, want %#v", gotActions, wantActions)
	}
	if form := gotCalls[0].form; form.Get("SecurityGroupId") != "sg-1" || form.Get("SecurityGroupRuleId.1") != "rule-1" {
		t.Fatalf("security-rule delete form = %#v", form)
	}
	if form := gotCalls[2].form; form.Get("AllocationId") != "eip-1" || form.Get("InstanceId") != "i-1" || form.Get("Force") != "true" {
		t.Fatalf("EIP association delete form = %#v", form)
	}
	if form := gotCalls[4].form; form.Get("InstanceId") != "i-1" || form.Get("DiskId") != "disk-data" {
		t.Fatalf("disk attachment delete form = %#v", form)
	}
	if form := gotCalls[5].form; form.Get("InstanceId") != "i-1" || form.Get("Force") != "true" {
		t.Fatalf("instance delete form = %#v", form)
	}
}

func TestLifecycleOpenAPIMutationGuardsFailBeforeProvider(t *testing.T) {
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		t.Errorf("guarded lifecycle mutation reached provider: %s", request.Header.Get("x-acs-action"))
		writer.WriteHeader(http.StatusInternalServerError)
	})
	api := newLifecycleOpenAPITestClient(t, handler)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := api.AssociatePublicAddress(ctx, PublicAddressAssociationRequest{
		Region: RegionHangzhou, AllocationID: "eip-1", InstanceID: "i-1", ClientToken: "token",
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("AssociatePublicAddress(canceled) error = %v", err)
	}
	if err := api.DeleteAsset(ctx, LifecycleAsset{ID: "i-1", Kind: ResourceKindInstance}); !errors.Is(err, context.Canceled) {
		t.Fatalf("DeleteAsset(canceled) error = %v", err)
	}
	if err := api.DeleteAsset(context.Background(), LifecycleAsset{ID: "unknown-1", Kind: "unknown"}); !errors.Is(err, ErrAmbiguousInventory) {
		t.Fatalf("DeleteAsset(unknown kind) error = %v", err)
	}
	if _, err := api.CreateNetwork(context.Background(), NetworkCreateRequest{
		Region: RegionHangzhou, Zone: "cn-hangzhou-h", VPCIPv4CIDR: "10.42.0.1/16", VSwitchIPv4CIDR: "10.42.0.0/24", ClientToken: "token",
	}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("CreateNetwork(unmasked VPC) error = %v", err)
	}
}

func TestLifecycleOpenAPIStateTransitionTagsEveryDiscoveredProviderResource(t *testing.T) {
	until := time.Date(2026, 9, 2, 19, 0, 0, 0, time.UTC)
	ruleDescription, err := securityRuleDescription(AccessRuleRequest{
		Kind: AccessRulePrivate, ID: "private-vswitch", TargetRole: "network", Protocol: cloudlease.ProtocolTCP,
		PortFrom: 1, PortTo: 65535, SourcePrefix: netip.MustParsePrefix("10.42.0.0/24"),
		DestinationPrefix: netip.MustParsePrefix("0.0.0.0/0"), Until: until,
		Tags: map[string]string{cloudlease.TagLeaseID: "lease-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var mutex sync.Mutex
	var tagCalls []identityOpenAPICall
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		action, form := identityOpenAPIRequest(t, request)
		writer.Header().Set("Content-Type", "application/json")
		if action == "TagResources" {
			mutex.Lock()
			tagCalls = append(tagCalls, identityOpenAPICall{action: action, form: form})
			mutex.Unlock()
			_ = json.NewEncoder(writer).Encode(map[string]any{"RequestId": "request-tag-state"})
			return
		}
		_ = json.NewEncoder(writer).Encode(inventoryContractResponse(t, action, form, ruleDescription))
	})
	api := newLifecycleOpenAPITestClient(t, handler)

	if err := api.SetLifecycleState(context.Background(), InventoryQuery{
		Region: RegionHangzhou, LeaseID: "lease-1", Repository: "WuKongIM/WuKongIM",
	}, lifecycleStateActive); err != nil {
		t.Fatalf("SetLifecycleState() error = %v", err)
	}
	mutex.Lock()
	gotCalls := append([]identityOpenAPICall(nil), tagCalls...)
	mutex.Unlock()
	resourceTypes := make(map[string]int)
	for _, call := range gotCalls {
		resourceType := call.form.Get("ResourceType")
		resourceTypes[resourceType]++
		if call.form.Get("Tag.1.Key") != lifecycleStateTag || call.form.Get("Tag.1.Value") != lifecycleStateActive {
			t.Fatalf("state tag form = %#v", call.form)
		}
	}
	wantResourceTypes := map[string]int{
		"instance": 1, "disk": 1, "eni": 1, "securitygroup": 1,
		"VPC": 1, "VSWITCH": 1, "EIP": 1,
	}
	if !reflect.DeepEqual(resourceTypes, wantResourceTypes) {
		t.Fatalf("state-tag resource types = %#v, want %#v", resourceTypes, wantResourceTypes)
	}
	if err := api.SetLifecycleState(context.Background(), InventoryQuery{Region: RegionHangzhou, LeaseID: "lease-1"}, "paused"); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("SetLifecycleState(invalid state) error = %v", err)
	}
}

func TestLifecycleOpenAPITaggingKeepsProviderBatchesBoundedAndComplete(t *testing.T) {
	var mutex sync.Mutex
	var calls []identityOpenAPICall
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		action, form := identityOpenAPIRequest(t, request)
		if action != "TagResources" {
			t.Errorf("unexpected batch-tag action %q", action)
		}
		mutex.Lock()
		calls = append(calls, identityOpenAPICall{action: action, form: form})
		mutex.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{"RequestId": "request-batch-tag"})
	})
	api := newLifecycleOpenAPITestClient(t, handler)
	assets := make([]LifecycleAsset, 0, 102)
	for index := 0; index < 51; index++ {
		assets = append(assets,
			LifecycleAsset{ID: "instance-" + strconv.Itoa(index), Kind: ResourceKindInstance},
			LifecycleAsset{ID: "vpc-" + strconv.Itoa(index), Kind: ResourceKindVPC},
		)
	}
	if err := api.tagLifecycleAssets(RegionHangzhou, assets, map[string]string{lifecycleStateTag: lifecycleStateCleanup}); err != nil {
		t.Fatalf("tagLifecycleAssets() error = %v", err)
	}
	mutex.Lock()
	gotCalls := append([]identityOpenAPICall(nil), calls...)
	mutex.Unlock()
	if len(gotCalls) != 4 {
		t.Fatalf("batch tag calls = %d, forms %#v", len(gotCalls), gotCalls)
	}
	counts := make(map[string][]int)
	for _, call := range gotCalls {
		counts[call.form.Get("ResourceType")] = append(counts[call.form.Get("ResourceType")], indexedFormValueCount(call.form, "ResourceId."))
	}
	for resourceType, got := range counts {
		sort.Ints(got)
		if !reflect.DeepEqual(got, []int{1, 50}) {
			t.Fatalf("%s batch sizes = %#v, want [1 50]", resourceType, got)
		}
	}
	if len(counts) != 2 || counts["instance"] == nil || counts["VPC"] == nil {
		t.Fatalf("batch tag resource types = %#v", counts)
	}
}

func newLifecycleOpenAPITestClient(t *testing.T, handler http.Handler) *OpenAPI {
	t.Helper()
	api := newInventoryOpenAPITestClient(t, handler)
	api.lifecycleAuthorized = true
	return api
}

func lifecycleOpenAPIContractTags() map[string]string {
	return map[string]string{
		cloudlease.TagManagedBy:  cloudlease.ManagedByValue,
		cloudlease.TagLeaseID:    "lease-contract",
		cloudlease.TagRequestID:  "request-contract",
		cloudlease.TagRepository: "WuKongIM/WuKongIM",
		cloudlease.TagProvider:   ProviderName,
		cloudlease.TagRegion:     RegionHangzhou,
	}
}

func countLifecycleAction(calls []identityOpenAPICall, action string) int {
	count := 0
	for _, call := range calls {
		if call.action == action {
			count++
		}
	}
	return count
}

func indexedFormValueCount(form map[string][]string, prefix string) int {
	count := 0
	for key := range form {
		if strings.HasPrefix(key, prefix) {
			count++
		}
	}
	return count
}
