package alibaba

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudsim"
	openapiutil "github.com/alibabacloud-go/darabonba-openapi/v2/utils"
	"github.com/alibabacloud-go/tea/dara"
)

func TestOpenAPIDiscoveryAndOffersUseProviderResponses(t *testing.T) {
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := request.ParseForm(); err != nil {
			t.Fatalf("ParseForm() error = %v", err)
		}
		writer.Header().Set("Content-Type", "application/json")
		switch action := request.Header.Get("x-acs-action"); action {
		case "GetCallerIdentity":
			writeOpenAPIBoundaryJSON(t, writer, `{"RequestId":"request-account","AccountId":"1234567890123456"}`)
		case "DescribeImages":
			if request.Form.Get("ImageFamily") != "acs:alibaba_cloud_linux_3_2104_lts_x64" || request.Form.Get("IsSupportCloudinit") != "true" {
				t.Fatalf("DescribeImages form = %v", request.Form)
			}
			writeOpenAPIBoundaryJSON(t, writer, `{
				"RequestId":"request-images","TotalCount":2,
				"Images":{"Image":[
					{"ImageId":"aliyun-old","CreationTime":"2026-07-01T00:00:00Z","IsSupportCloudinit":true},
					{"ImageId":"aliyun-new","CreationTime":"2026-08-01T00:00:00Z","IsSupportCloudinit":true}
				]}
			}`)
		case "DescribeInstanceTypes":
			writeOpenAPIBoundaryJSON(t, writer, `{
				"RequestId":"request-types","NextToken":"",
				"InstanceTypes":{"InstanceType":[{
					"InstanceTypeId":"ecs.c7.large","CpuArchitecture":"X86","InstanceFamilyLevel":"EnterpriseLevel",
					"GPUAmount":0,"CpuCoreCount":2,"EniPrivateIpAddressQuantity":10
				}]}
			}`)
		case "DescribeAvailableResource":
			writeOpenAPIBoundaryJSON(t, writer, `{
				"RequestId":"request-availability",
				"AvailableZones":{"AvailableZone":[{
					"ZoneId":"cn-hangzhou-a","Status":"Available",
					"AvailableResources":{"AvailableResource":[{"SupportedResources":{"SupportedResource":[
						{"Value":"ecs.c7.large","Status":"Available"},
						{"Value":"ecs.g7.large","Status":"Closed"}
					]}}]}
				}]}
			}`)
		case "DescribePrice":
			writeOpenAPIBoundaryJSON(t, writer, `{"RequestId":"request-price","PriceInfo":{"Price":{"Currency":"CNY","TradePrice":0.123456}}}`)
		case "DescribeAccountAttributes":
			writeOpenAPIBoundaryJSON(t, writer, `{
				"RequestId":"request-quota","AccountAttributeItems":{"AccountAttributeItem":[
					{"AttributeName":"max-spot-instance-vcpu-count","AttributeValues":{"ValueItem":[{"Value":"100"}]}},
					{"AttributeName":"used-spot-instance-vcpu-count","AttributeValues":{"ValueItem":[{"Value":"10"}]}}
				]}
			}`)
		default:
			t.Fatalf("unexpected Alibaba action %q", action)
		}
	})
	api := newOpenAPIBoundary(t, handler)

	accountHash, err := api.AccountIDHash(context.Background())
	if err != nil {
		t.Fatalf("AccountIDHash() error = %v", err)
	}
	if accountHash != "sha256:7a51d064a1a216a692f753fcdab276e4ff201a01d8b66f56d50d4d719fd0dc87" {
		t.Fatalf("AccountIDHash() = %q", accountHash)
	}
	imageID, err := api.LatestLinuxImage(context.Background(), "cn-hangzhou")
	if err != nil || imageID != "aliyun-new" {
		t.Fatalf("LatestLinuxImage() = %q, %v", imageID, err)
	}
	types, err := api.InstanceTypes(context.Background(), "cn-hangzhou", 2, 4)
	if err != nil || !reflect.DeepEqual(types, []InstanceTypeCandidate{{
		ID: "ecs.c7.large", CPUArchitecture: "X86", FamilyLevel: "EnterpriseLevel", PrivateIPv4Capacity: 10,
	}}) {
		t.Fatalf("InstanceTypes() = %#v, %v", types, err)
	}
	available, err := api.AvailableInstanceTypes(context.Background(), "cn-hangzhou", "cn-hangzhou-a")
	if err != nil || !reflect.DeepEqual(available, map[string]bool{"ecs.c7.large": true}) {
		t.Fatalf("AvailableInstanceTypes() = %#v, %v", available, err)
	}
	offers, err := api.Offers(context.Background(), OfferRequest{
		Region: "cn-hangzhou", ZoneID: "cn-hangzhou-a", ImageID: imageID,
		InstanceTypes: []string{"ecs.c7.large"}, HostCount: 4,
		SystemDiskCategory: "cloud_essd", SystemDiskSizeGiB: 40,
		DataDiskCategory: "cloud_essd", DataDiskSizeGiB: 40,
		SimulatorPrivateIPv4Count: 3,
	})
	if err != nil || !reflect.DeepEqual(offers, []Offer{{
		InstanceType: "ecs.c7.large", ZoneID: "cn-hangzhou-a", HourlyCostMicros: 123456,
		Available: true, QuotaAvailable: true,
	}}) {
		t.Fatalf("Offers() = %#v, %v", offers, err)
	}
}

func TestOpenAPIAddressIngressStateAndDeletionLifecycle(t *testing.T) {
	deadline := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	var (
		mu                 sync.Mutex
		actions            []string
		ingressOpen        bool
		ingressDescription string
		ingressPort        string
		ingressSource      string
		tagCalls           int
	)
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := request.ParseForm(); err != nil {
			t.Fatalf("ParseForm() error = %v", err)
		}
		action := request.Header.Get("x-acs-action")
		mu.Lock()
		actions = append(actions, action)
		mu.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		switch action {
		case "AllocateEipAddress":
			writeOpenAPIBoundaryJSON(t, writer, `{"RequestId":"request-eip","AllocationId":"eip-1","EipAddress":"203.0.113.8"}`)
		case "TagResources":
			mu.Lock()
			tagCalls++
			mu.Unlock()
			writeOpenAPIBoundaryJSON(t, writer, `{"RequestId":"request-tag"}`)
		case "AssociateEipAddress":
			if request.Form.Get("AllocationId") != "eip-1" || request.Form.Get("InstanceId") != "i-sim" {
				t.Fatalf("AssociateEipAddress form = %v", request.Form)
			}
			writeOpenAPIBoundaryJSON(t, writer, `{"RequestId":"request-associate"}`)
		case "AuthorizeSecurityGroup":
			mu.Lock()
			ingressOpen = true
			ingressDescription = request.Form.Get("Description")
			ingressPort = request.Form.Get("PortRange")
			ingressSource = request.Form.Get("SourceCidrIp")
			mu.Unlock()
			writeOpenAPIBoundaryJSON(t, writer, `{"RequestId":"request-authorize"}`)
		case "DescribeSecurityGroupAttribute":
			mu.Lock()
			open := ingressOpen
			description, port, source := ingressDescription, ingressPort, ingressSource
			mu.Unlock()
			if open {
				writeOpenAPIBoundaryValue(t, writer, map[string]any{
					"RequestId": "request-ingress",
					"Permissions": map[string]any{"Permission": []map[string]any{{
						"SecurityGroupRuleId": "sgr-1", "Description": description,
						"PortRange": port, "SourceCidrIp": source,
					}}},
				})
				return
			}
			writeOpenAPIBoundaryJSON(t, writer, `{"RequestId":"request-ingress","Permissions":{"Permission":[]}}`)
		case "RevokeSecurityGroup":
			mu.Lock()
			ingressOpen = false
			mu.Unlock()
			writeOpenAPIBoundaryJSON(t, writer, `{"RequestId":"request-revoke"}`)
		case "UnassociateEipAddress", "ReleaseEipAddress", "DeleteInstance", "DeleteSecurityGroup", "DeleteVSwitch", "DeleteVpc":
			writeOpenAPIBoundaryJSON(t, writer, `{"RequestId":"request-delete"}`)
		case "DeleteDisk":
			writer.WriteHeader(http.StatusBadRequest)
			writeOpenAPIBoundaryJSON(t, writer, `{"Code":"IncorrectDiskStatus","Message":"disk is already deleting","RequestId":"request-disk"}`)
		default:
			t.Fatalf("unexpected Alibaba action %q", action)
		}
	})
	api := newOpenAPIBoundary(t, handler)
	tags := map[string]string{
		cloudsim.TagManagedBy: cloudsim.ManagedByValue,
		cloudsim.TagRunID:     "run-1",
	}

	address, err := api.CreatePublicAddress(context.Background(), PublicAddressRequest{
		Region: "cn-hangzhou", BandwidthMbps: 5, Tags: tags,
	})
	if err != nil || address.ID != "eip-1" || address.PublicAddress != "203.0.113.8" || !address.Billable {
		t.Fatalf("CreatePublicAddress() = %#v, %v", address, err)
	}
	if err := api.AssociatePublicAddress(context.Background(), address.ID, "i-sim"); err != nil {
		t.Fatalf("AssociatePublicAddress() error = %v", err)
	}
	ingress := IngressRequest{
		RunID: "run-1", SecurityGroupID: "sg-1", Source: netip.MustParsePrefix("198.51.100.8/32"),
		Port: 19092, Until: deadline, Open: true,
	}
	if err := api.SetIngress(context.Background(), ingress); err != nil {
		t.Fatalf("SetIngress(open) error = %v", err)
	}
	windows, err := api.ListIngress(context.Background(), IngressListRequest{RunID: "run-1", SecurityGroupID: "sg-1"})
	if err != nil || len(windows) != 1 || windows[0].Port != 19092 || windows[0].Source != ingress.Source || !windows[0].Until.Equal(deadline) {
		t.Fatalf("ListIngress() = %#v, %v", windows, err)
	}
	ingress.Open = false
	if err := api.SetIngress(context.Background(), ingress); err != nil {
		t.Fatalf("SetIngress(close) error = %v", err)
	}
	windows, err = api.ListIngress(context.Background(), IngressListRequest{RunID: "run-1", SecurityGroupID: "sg-1"})
	if err != nil || len(windows) != 0 {
		t.Fatalf("ListIngress(after close) = %#v, %v", windows, err)
	}

	assets := []Asset{
		{ID: "i-sim", Kind: "compute"}, {ID: "d-1", Kind: "disk"}, {ID: "sg-1", Kind: "security-group"},
		{ID: "vpc-1", Kind: "vpc"}, {ID: "vsw-1", Kind: "subnet"}, {ID: "eip-1", Kind: "public-address"},
	}
	mu.Lock()
	tagsBeforeState := tagCalls
	mu.Unlock()
	if err := api.UpdateRunState(context.Background(), StateUpdateRequest{
		Region: "cn-hangzhou", Assets: assets, State: cloudsim.StateRunning, ActiveUntil: deadline,
	}); err != nil {
		t.Fatalf("UpdateRunState() error = %v", err)
	}
	mu.Lock()
	stateTagCalls := tagCalls - tagsBeforeState
	mu.Unlock()
	if stateTagCalls != 6 {
		t.Fatalf("UpdateRunState() tag calls = %d, want one per resource kind", stateTagCalls)
	}

	deleteAssets := []Asset{
		{ID: "eip-1", Kind: "public-address", AttachedTo: "i-sim"},
		{ID: "i-sim", Kind: "compute"}, {ID: "d-1", Kind: "disk"}, {ID: "sg-1", Kind: "security-group"},
		{ID: "vsw-1", Kind: "subnet"}, {ID: "vpc-1", Kind: "vpc"},
	}
	for _, asset := range deleteAssets {
		if err := api.DeleteAsset(context.Background(), asset); err != nil {
			t.Fatalf("DeleteAsset(%s) error = %v", asset.Kind, err)
		}
	}
	if err := api.DeleteAsset(context.Background(), Asset{ID: "unsupported", Kind: "unknown"}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("DeleteAsset(unknown) error = %v, want ErrInvalidConfig", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(actions) == 0 {
		t.Fatal("no Alibaba API actions were observed")
	}
}

func TestOpenAPIListAssetsNormalizesCompleteTaggedInventory(t *testing.T) {
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := request.ParseForm(); err != nil {
			t.Fatalf("ParseForm() error = %v", err)
		}
		if !strings.Contains(request.Form.Encode(), "run-1") || !strings.Contains(request.Form.Encode(), cloudsim.ManagedByValue) {
			t.Fatalf("inventory selector = %v, want managed-by and exact run tags", request.Form)
		}
		writer.Header().Set("Content-Type", "application/json")
		switch action := request.Header.Get("x-acs-action"); action {
		case "DescribeInstances":
			writeOpenAPIBoundaryJSON(t, writer, `{
				"RequestId":"request-instance","TotalCount":1,"Instances":{"Instance":[{
					"InstanceId":"i-sim","VpcAttributes":{"PrivateIpAddress":{"IpAddress":["10.42.0.20"]}},
					"Tags":{"Tag":[{"TagKey":"wukongim-resource-role","TagValue":"sim"},{"TagKey":"wukongim-run-id","TagValue":"run-1"}]}
				}]}
			}`)
		case "DescribeDisks":
			writeOpenAPIBoundaryJSON(t, writer, `{
				"RequestId":"request-disk","TotalCount":1,"Disks":{"Disk":[{
					"DiskId":"d-sim","InstanceId":"i-sim",
					"Tags":{"Tag":[{"TagKey":"wukongim-resource-role","TagValue":"sim"},{"TagKey":"wukongim-run-id","TagValue":"run-1"}]}
				}]}
			}`)
		case "DescribeSecurityGroups":
			writeOpenAPIBoundaryJSON(t, writer, `{
				"RequestId":"request-sg","TotalCount":1,"SecurityGroups":{"SecurityGroup":[{
					"SecurityGroupId":"sg-1","Tags":{"Tag":[{"TagKey":"wukongim-resource-role","TagValue":"run-network"},{"TagKey":"wukongim-run-id","TagValue":"run-1"}]}
				}]}
			}`)
		case "DescribeVpcs":
			writeOpenAPIBoundaryJSON(t, writer, `{
				"RequestId":"request-vpc","TotalCount":1,"Vpcs":{"Vpc":[{
					"VpcId":"vpc-1","Tags":{"Tag":[{"Key":"wukongim-resource-role","Value":"run-network"},{"Key":"wukongim-run-id","Value":"run-1"}]}
				}]}
			}`)
		case "DescribeVSwitches":
			writeOpenAPIBoundaryJSON(t, writer, `{
				"RequestId":"request-vswitch","TotalCount":1,"VSwitches":{"VSwitch":[{
					"VSwitchId":"vsw-1","Tags":{"Tag":[{"Key":"wukongim-resource-role","Value":"run-network"},{"Key":"wukongim-run-id","Value":"run-1"}]}
				}]}
			}`)
		case "DescribeEipAddresses":
			writeOpenAPIBoundaryJSON(t, writer, `{
				"RequestId":"request-eip","TotalCount":1,"EipAddresses":{"EipAddress":[{
					"AllocationId":"eip-1","IpAddress":"203.0.113.8","InstanceId":"i-sim",
					"Tags":{"Tag":[{"Key":"wukongim-resource-role","Value":"sim"},{"Key":"wukongim-run-id","Value":"run-1"}]}
				}]}
			}`)
		default:
			t.Fatalf("unexpected inventory action %q", action)
		}
	})
	api := newOpenAPIBoundary(t, handler)

	assets, err := api.ListAssets(context.Background(), ListAssetsRequest{Region: "cn-hangzhou", RunID: "run-1"})
	if err != nil {
		t.Fatalf("ListAssets() error = %v", err)
	}
	if len(assets) != 6 {
		t.Fatalf("ListAssets() = %#v, want all six supported resource kinds", assets)
	}
	byKind := make(map[string]Asset, len(assets))
	for _, asset := range assets {
		byKind[asset.Kind] = asset
	}
	if got := byKind["compute"]; got.ID != "i-sim" || got.Role != "sim" || got.PrivateAddress != "10.42.0.20" || !got.Billable {
		t.Fatalf("compute asset = %#v", got)
	}
	if got := byKind["disk"]; got.ID != "d-sim" || got.AttachedTo != "i-sim" || !got.Billable {
		t.Fatalf("disk asset = %#v", got)
	}
	if got := byKind["security-group"]; got.ID != "sg-1" || got.Role != "run-network" {
		t.Fatalf("security group asset = %#v", got)
	}
	if got := byKind["vpc"]; got.ID != "vpc-1" || got.Role != "run-network" {
		t.Fatalf("VPC asset = %#v", got)
	}
	if got := byKind["subnet"]; got.ID != "vsw-1" || got.Role != "run-network" {
		t.Fatalf("subnet asset = %#v", got)
	}
	if got := byKind["public-address"]; got.ID != "eip-1" || got.PublicAddress != "203.0.113.8" || got.AttachedTo != "i-sim" || !got.Billable {
		t.Fatalf("public address asset = %#v", got)
	}
}

func newOpenAPIBoundary(t *testing.T, handler http.Handler) *OpenAPI {
	t.Helper()
	config := (&openapiutil.Config{
		AccessKeyId: dara.String("test-access-key-id"), AccessKeySecret: dara.String("test-access-key-secret"),
		RegionId: dara.String("cn-hangzhou"), Endpoint: dara.String("openapi.test"),
		Protocol: dara.String("http"),
	}).SetHttpClient(openAPITestHTTPClient{handler: handler})
	api, err := newOpenAPI(config)
	if err != nil {
		t.Fatalf("newOpenAPI() error = %v", err)
	}
	return api
}

func writeOpenAPIBoundaryJSON(t *testing.T, writer http.ResponseWriter, body string) {
	t.Helper()
	if _, err := writer.Write([]byte(body)); err != nil {
		t.Fatalf("write OpenAPI response: %v", err)
	}
}

func writeOpenAPIBoundaryValue(t *testing.T, writer http.ResponseWriter, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal OpenAPI response: %v", err)
	}
	writeOpenAPIBoundaryJSON(t, writer, string(data))
}
