package alibaba

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudlease"
	openapiutil "github.com/alibabacloud-go/darabonba-openapi/v2/utils"
	ecs "github.com/alibabacloud-go/ecs-20140526/v7/client"
	vpc "github.com/alibabacloud-go/vpc-20160428/v6/client"
)

func TestOpenAPIInventoryProvesEveryTaggedRootScopeEmpty(t *testing.T) {
	var mutex sync.Mutex
	var actions []string
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := request.ParseForm(); err != nil {
			t.Errorf("ParseForm() error = %v", err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		action := request.Header.Get("x-acs-action")
		if action == "" {
			action = request.Form.Get("Action")
		}
		mutex.Lock()
		actions = append(actions, action)
		mutex.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"RequestId": "test-request", "TotalCount": 0,
		})
	})

	api := newInventoryOpenAPITestClient(t, handler)
	assets, err := api.ListAssets(context.Background(), InventoryQuery{
		Region: RegionHangzhou, LeaseID: "lease-1", Repository: "WuKongIM/WuKongIM",
	})
	if err != nil {
		t.Fatalf("ListAssets() error = %v", err)
	}
	if len(assets) != 0 {
		t.Fatalf("ListAssets() = %#v, want proven empty inventory", assets)
	}
	sort.Strings(actions)
	want := []string{
		"DescribeDisks", "DescribeEipAddresses", "DescribeInstances", "DescribeNetworkInterfaces",
		"DescribeSecurityGroups", "DescribeVSwitches", "DescribeVpcs",
	}
	if strings.Join(actions, ",") != strings.Join(want, ",") {
		t.Fatalf("inventory actions = %#v, want every tagged root scope %#v", actions, want)
	}
}

func TestOpenAPIInventoryPaginatesRootsAndRecoversUntaggedRelations(t *testing.T) {
	until := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
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
	actionCalls := make(map[string]int)
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := request.ParseForm(); err != nil {
			t.Errorf("ParseForm() error = %v", err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		action := request.Header.Get("x-acs-action")
		mutex.Lock()
		actionCalls[action]++
		mutex.Unlock()
		response := inventoryContractResponse(t, action, request.Form, ruleDescription)
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(response)
	})

	api := newInventoryOpenAPITestClient(t, handler)
	assets, err := api.ListAssets(context.Background(), InventoryQuery{
		Region: RegionHangzhou, LeaseID: "lease-1", Repository: "WuKongIM/WuKongIM",
	})
	if err != nil {
		t.Fatalf("ListAssets() error = %v", err)
	}
	if actionCalls["DescribeInstances"] != 2 {
		t.Fatalf("DescribeInstances calls = %d, want complete two-page inventory", actionCalls["DescribeInstances"])
	}
	byIdentity := make(map[string]LifecycleAsset, len(assets))
	for _, asset := range assets {
		byIdentity[asset.Kind+"/"+asset.ID] = asset
	}
	for _, identity := range []string{
		"instance/i-service", "instance/i-load", "disk/d-service-data", "disk/d-load-system",
		"disk-attachment/disk-attachment:d-service-data:i-service", "eni/eni-load",
		"security-group/sg-1", "security-rule/rule-private", "vpc/vpc-1", "vswitch/vsw-1",
		"eip/eip-1", "eip-association/eip-association:eip-1:i-load", "nat-gateway/nat-1", "route-entry/route-1",
	} {
		if _, exists := byIdentity[identity]; !exists {
			t.Fatalf("complete inventory missing %s: %#v", identity, assets)
		}
	}
	for _, identity := range []string{"disk/d-service-data", "eni/eni-load", "nat-gateway/nat-1", "route-entry/route-1"} {
		if !byIdentity[identity].IdentityInherited {
			t.Fatalf("relationship asset %s did not inherit exact parent identity: %#v", identity, byIdentity[identity])
		}
	}
	if got := byIdentity["disk/d-service-data"]; got.Role != "service" || got.ParentID != "i-service" {
		t.Fatalf("inherited disk identity = %#v, want service/i-service", got)
	}
}

func TestOpenAPIInventoryFailsClosedOnRepeatedProviderToken(t *testing.T) {
	eniCalls := 0
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := request.ParseForm(); err != nil {
			t.Errorf("ParseForm() error = %v", err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		action := request.Header.Get("x-acs-action")
		response := map[string]any{"RequestId": "test-request", "TotalCount": 0}
		if action == "DescribeNetworkInterfaces" {
			eniCalls++
			response["NextToken"] = "repeat-token"
			response["NetworkInterfaceSets"] = map[string]any{"NetworkInterfaceSet": []any{}}
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(response)
	})

	api := newInventoryOpenAPITestClient(t, handler)
	_, err := api.ListAssets(context.Background(), InventoryQuery{
		Region: RegionHangzhou, LeaseID: "lease-1", Repository: "WuKongIM/WuKongIM",
	})
	if !errors.Is(err, ErrAmbiguousInventory) {
		t.Fatalf("ListAssets(repeated token) error = %v, want ErrAmbiguousInventory", err)
	}
	if eniCalls != 2 {
		t.Fatalf("DescribeNetworkInterfaces calls = %d, want stop at first repeated token", eniCalls)
	}
}

func inventoryContractResponse(t *testing.T, action string, form map[string][]string, ruleDescription string) map[string]any {
	t.Helper()
	page, _ := strconv.Atoi(firstFormValue(form, "PageNumber"))
	instanceID := firstFormValue(form, "InstanceId")
	tags := func(role string, vpcStyle bool) map[string]any {
		pairs := []map[string]string{
			{"key": cloudlease.TagManagedBy, "value": cloudlease.ManagedByValue},
			{"key": cloudlease.TagLeaseID, "value": "lease-1"},
			{"key": cloudlease.TagRepository, "value": "WuKongIM/WuKongIM"},
			{"key": cloudlease.TagResourceRole, "value": role},
		}
		items := make([]map[string]string, 0, len(pairs))
		for _, pair := range pairs {
			if vpcStyle {
				items = append(items, map[string]string{"Key": pair["key"], "Value": pair["value"]})
			} else {
				items = append(items, map[string]string{"TagKey": pair["key"], "TagValue": pair["value"]})
			}
		}
		return map[string]any{"Tag": items}
	}
	instance := func(id, role, address string) map[string]any {
		return map[string]any{
			"InstanceId": id, "InstanceType": "ecs.g8i.xlarge", "ImageId": "ubuntu-24",
			"VpcAttributes": map[string]any{"VpcId": "vpc-1", "VSwitchId": "vsw-1", "PrivateIpAddress": map[string]any{"IpAddress": []string{address}}},
			"Tags":          tags(role, false),
		}
	}
	base := map[string]any{"RequestId": "test-request"}
	switch action {
	case "DescribeInstances":
		base["TotalCount"] = 2
		items := []map[string]any{instance("i-service", "service", "10.42.0.10")}
		if page == 2 {
			items = []map[string]any{instance("i-load", "load", "10.42.0.11")}
		}
		base["Instances"] = map[string]any{"Instance": items}
	case "DescribeDisks":
		var items []map[string]any
		if instanceID != "" {
			role := strings.TrimPrefix(instanceID, "i-")
			items = []map[string]any{
				{"DiskId": "d-" + role + "-system", "InstanceId": instanceID, "Type": "system", "Size": 40, "Category": providerDiskESSD, "PerformanceLevel": providerDiskLevelPL0},
				{"DiskId": "d-" + role + "-data", "InstanceId": instanceID, "Type": "data", "Size": 200, "Category": providerDiskESSD, "PerformanceLevel": providerDiskLevelPL0},
			}
		}
		base["TotalCount"] = len(items)
		base["Disks"] = map[string]any{"Disk": items}
	case "DescribeNetworkInterfaces":
		var items []map[string]any
		if instanceID != "" {
			role := strings.TrimPrefix(instanceID, "i-")
			items = []map[string]any{{
				"NetworkInterfaceId": "eni-" + role, "InstanceId": instanceID, "PrivateIpAddress": "10.42.0.20",
				"Type": "Primary", "VpcId": "vpc-1", "VSwitchId": "vsw-1",
			}}
		}
		base["NextToken"] = ""
		base["NetworkInterfaceSets"] = map[string]any{"NetworkInterfaceSet": items}
	case "DescribeSecurityGroups":
		base["TotalCount"] = 1
		base["SecurityGroups"] = map[string]any{"SecurityGroup": []map[string]any{{"SecurityGroupId": "sg-1", "VpcId": "vpc-1", "Tags": tags("network", false)}}}
	case "DescribeSecurityGroupAttribute":
		base["NextToken"] = ""
		base["Permissions"] = map[string]any{"Permission": []map[string]any{{
			"SecurityGroupRuleId": "rule-private", "Description": ruleDescription, "IpProtocol": "TCP",
			"PortRange": "1/65535", "SourceCidrIp": "10.42.0.0/24", "DestCidrIp": "0.0.0.0/0",
		}}}
	case "DescribeVpcs":
		base["TotalCount"] = 1
		base["Vpcs"] = map[string]any{"Vpc": []map[string]any{{"VpcId": "vpc-1", "Tags": tags("network", true)}}}
	case "DescribeVSwitches":
		base["TotalCount"] = 1
		base["VSwitches"] = map[string]any{"VSwitch": []map[string]any{{"VSwitchId": "vsw-1", "VpcId": "vpc-1", "Tags": tags("network", true)}}}
	case "DescribeEipAddresses":
		base["TotalCount"] = 1
		base["EipAddresses"] = map[string]any{"EipAddress": []map[string]any{{
			"AllocationId": "eip-1", "IpAddress": "198.51.100.10", "InstanceId": "i-load", "Tags": tags("load", true),
		}}}
	case "DescribeNatGateways":
		base["TotalCount"] = 1
		base["NatGateways"] = map[string]any{"NatGateway": []map[string]any{{"NatGatewayId": "nat-1", "VpcId": "vpc-1"}}}
	case "DescribeRouteTableList":
		base["TotalCount"] = 1
		base["RouterTableList"] = map[string]any{"RouterTableListType": []map[string]any{{"RouteTableId": "rt-1", "VpcId": "vpc-1"}}}
	case "DescribeRouteEntryList":
		base["NextToken"] = ""
		base["RouteEntrys"] = map[string]any{"RouteEntry": []map[string]any{{
			"RouteEntryId": "route-1", "RouteTableId": "rt-1", "DestinationCidrBlock": "203.0.113.0/24", "Origin": "CustomCreate", "Type": "Custom",
		}}}
	default:
		t.Errorf("unexpected Alibaba inventory action %q with form %#v", action, form)
		base["Code"] = "UnexpectedAction"
		base["Message"] = fmt.Sprintf("unexpected action %q", action)
	}
	return base
}

func firstFormValue(form map[string][]string, key string) string {
	values := form[key]
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func newInventoryOpenAPITestClient(t *testing.T, handler http.Handler) *OpenAPI {
	t.Helper()
	config := (&openapiutil.Config{}).
		SetAccessKeyId("test-access-key").
		SetAccessKeySecret("test-access-secret").
		SetProtocol("http").
		SetEndpoint("openapi.test").
		SetHttpClient(openAPITestHTTPClient{handler: handler})
	ecsClient, err := ecs.NewClient(config)
	if err != nil {
		t.Fatalf("create ECS fake client: %v", err)
	}
	vpcClient, err := vpc.NewClient(config)
	if err != nil {
		t.Fatalf("create VPC fake client: %v", err)
	}
	return &OpenAPI{region: RegionHangzhou, ecs: ecsClient, vpc: vpcClient}
}
