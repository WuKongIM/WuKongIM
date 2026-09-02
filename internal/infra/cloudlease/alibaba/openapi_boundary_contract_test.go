package alibaba

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/url"
	"reflect"
	"sync"
	"testing"

	openapiutil "github.com/alibabacloud-go/darabonba-openapi/v2/utils"
	ecs "github.com/alibabacloud-go/ecs-20140526/v7/client"
	quotas "github.com/alibabacloud-go/quotas-20200510/v2/client"
	ram "github.com/alibabacloud-go/ram-20150501/v2/client"
	sts "github.com/alibabacloud-go/sts-20150401/v2/client"
)

func TestOpenAPIReadBoundaryPreservesProviderAuthorityAcrossQuoteInputs(t *testing.T) {
	const (
		accountID    = "1234567890123456"
		roleName     = "CloudLeaseObserver"
		policyName   = "cloud-lease-observe"
		zoneID       = "cn-hangzhou-h"
		instanceType = "ecs.g8i.xlarge"
		imageID      = "ubuntu-24-latest"
	)
	trust := `{"Version":"1","Statement":[]}`
	expectedPolicy := QuoteRolePolicyDocument()
	var mutex sync.Mutex
	var calls []identityOpenAPICall
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		action, form := identityOpenAPIRequest(t, request)
		mutex.Lock()
		calls = append(calls, identityOpenAPICall{action: action, form: form})
		mutex.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		response := openAPIReadBoundaryResponse(t, action, form, accountID, roleName, policyName, trust, expectedPolicy, zoneID, instanceType, imageID)
		if action == "DeleteInstance" {
			writeIdentityOpenAPIError(writer, http.StatusForbidden, "Forbidden.RAM", "mutation denied")
			return
		}
		_ = json.NewEncoder(writer).Encode(response)
	})
	api := newReadOpenAPITestClient(t, handler)
	ctx := context.Background()

	hash, err := api.AccountIDHash(ctx)
	if err != nil {
		t.Fatalf("AccountIDHash() error = %v", err)
	}
	wantHash := sha256.Sum256([]byte(accountID))
	if hash != "sha256:"+hex.EncodeToString(wantHash[:]) {
		t.Fatalf("account hash = %q", hash)
	}
	arn, err := api.CallerPrincipalARN(ctx)
	if err != nil || arn != "acs:ram::"+accountID+":assumed-role/"+roleName+"/github-run" {
		t.Fatalf("CallerPrincipalARN() = %q, %v", arn, err)
	}
	if err := api.AssertCallerRole(ctx, roleName); err != nil {
		t.Fatalf("AssertCallerRole() error = %v", err)
	}
	if err := api.AssertExactRoleTrust(ctx, roleName, trust); err != nil {
		t.Fatalf("AssertExactRoleTrust() error = %v", err)
	}

	zones, err := api.Zones(ctx, RegionHangzhou)
	if err != nil || !reflect.DeepEqual(zones, []Zone{{ID: zoneID, SupportsESSD: true}, {ID: "cn-hangzhou-z", SupportsESSD: false}}) {
		t.Fatalf("Zones() = %#v, %v", zones, err)
	}
	instanceTypes, err := api.InstanceTypes(ctx, RegionHangzhou, 4, 16*bytesPerGiB)
	if err != nil || len(instanceTypes) != 1 || instanceTypes[0].ID != instanceType || instanceTypes[0].VCPUs != 4 ||
		instanceTypes[0].MemoryBytes != 16*bytesPerGiB || instanceTypes[0].GPUCount != 0 {
		t.Fatalf("InstanceTypes() = %#v, %v", instanceTypes, err)
	}
	images, err := api.Images(ctx, RegionHangzhou, instanceType)
	if err != nil || len(images) != 1 || images[0].ID != imageID || !images[0].Official || !images[0].CloudInit || images[0].Architecture != providerArchitectureX86 {
		t.Fatalf("Images() = %#v, %v", images, err)
	}

	availabilityRequest := AvailabilityRequest{
		Region: RegionHangzhou, Zone: zoneID, InstanceType: instanceType, HostCount: 4,
		SystemDiskSizesGiB: []int{40, 80}, DataDiskSizesGiB: []int{200, 400},
	}
	availability, err := api.Availability(ctx, availabilityRequest)
	if err != nil || !availability.Instance || !availability.SystemESSDPL0 || !availability.DataESSDPL0 ||
		availability.InstanceReason != availabilityWithStock || availability.SystemESSDPL0Reason != availabilityWithStock || availability.DataESSDPL0Reason != availabilityWithStock {
		t.Fatalf("Availability() = %#v, %v", availability, err)
	}

	vcpu, err := api.PostPaidVCPUQuota(ctx, RegionHangzhou, zoneID)
	if err != nil || vcpu != (VCPUQuota{Limit: 128, Used: 32}) {
		t.Fatalf("PostPaidVCPUQuota() = %#v, %v", vcpu, err)
	}
	eip, err := api.EIPQuota(ctx, RegionHangzhou)
	if err != nil || eip != (EIPQuota{Limit: 20, Used: 3}) {
		t.Fatalf("EIPQuota() = %#v, %v", eip, err)
	}
	if err := api.AssertExactRolePolicy(ctx, roleName, policyName, expectedPolicy); err != nil {
		t.Fatalf("AssertExactRolePolicy() error = %v", err)
	}
	if err := api.AssertMutationDenied(ctx); err != nil {
		t.Fatalf("AssertMutationDenied() error = %v", err)
	}

	hostPrice, err := api.Price(ctx, PriceRequest{
		Kind: PriceKindHost, Region: RegionHangzhou, Zone: zoneID,
		InstanceType: instanceType, ImageID: imageID, BillingModel: providerBillingPostPaid,
		SystemDiskGiB: 40, SystemDiskClass: providerDiskESSD, SystemDiskLevel: providerDiskLevelPL0,
		DataDiskGiB: 200, DataDiskClass: providerDiskESSD, DataDiskLevel: providerDiskLevelPL0,
	})
	if err != nil || hostPrice != (Price{Currency: "CNY", UnitCostMicros: 1_250_000}) {
		t.Fatalf("host Price() = %#v, %v", hostPrice, err)
	}
	eipPrice, err := api.Price(ctx, PriceRequest{
		Kind: PriceKindEIPTraffic, Region: RegionHangzhou,
		InternetCharge: providerInternetPayTraffic, PeakBandwidthMbps: 100,
	})
	if err != nil || eipPrice != hostPrice {
		t.Fatalf("EIP Price() = %#v, %v", eipPrice, err)
	}

	actions := identityOpenAPIActions(calls)
	if actions["DescribeAvailableResource"] != 3 || actions["DescribePrice"] != 2 || actions["GetCallerIdentity"] != 3 {
		t.Fatalf("provider call cardinality = %#v", actions)
	}
	for _, destination := range []string{"InstanceType", "SystemDisk", "DataDisk"} {
		if !identityOpenAPIHasForm(calls, "DescribeAvailableResource", "DestinationResource", destination) {
			t.Fatalf("missing availability request for %s: %#v", destination, calls)
		}
	}
}

func TestOpenAPIReadBoundaryRejectsInvalidInputsBeforeProviderCalls(t *testing.T) {
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		t.Errorf("invalid request reached provider: %s", request.URL)
		writer.WriteHeader(http.StatusInternalServerError)
	})
	api := newReadOpenAPITestClient(t, handler)

	if _, err := api.Zones(context.Background(), "cn-shanghai"); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("invalid zones error = %v", err)
	}
	for _, input := range []struct {
		vcpus  int
		memory int64
	}{
		{0, 16 * bytesPerGiB}, {4, 0}, {4, 16*bytesPerGiB + 1}, {math.MaxInt32 + 1, 16 * bytesPerGiB}, {4, (math.MaxInt32 + 1) * bytesPerGiB},
	} {
		if _, err := api.InstanceTypes(context.Background(), RegionHangzhou, input.vcpus, input.memory); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("invalid instance input %#v error = %v", input, err)
		}
	}
	if _, err := api.Images(context.Background(), RegionHangzhou, " "); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("invalid image input error = %v", err)
	}
	if _, err := api.Availability(context.Background(), AvailabilityRequest{Region: RegionHangzhou}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("invalid availability error = %v", err)
	}
	if _, err := api.PostPaidVCPUQuota(context.Background(), RegionHangzhou, " "); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("invalid vCPU quota error = %v", err)
	}
	if _, err := api.EIPQuota(context.Background(), "cn-shanghai"); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("invalid EIP quota error = %v", err)
	}
	if err := api.AssertExactRoleTrust(context.Background(), "", `{}`); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("invalid role trust error = %v", err)
	}
	if err := api.AssertExactRolePolicy(context.Background(), "role", "", `{}`); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("invalid role policy error = %v", err)
	}
	if _, err := api.Price(context.Background(), PriceRequest{Kind: PriceKind("unknown"), Region: RegionHangzhou}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("invalid price error = %v", err)
	}
	var unavailable *OpenAPI
	if err := unavailable.AssertMutationDenied(context.Background()); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("nil mutation probe error = %v", err)
	}
}

func openAPIReadBoundaryResponse(
	t *testing.T,
	action string,
	form url.Values,
	accountID, roleName, policyName, trust, policyDocument, zoneID, instanceType, imageID string,
) any {
	t.Helper()
	switch action {
	case "GetCallerIdentity":
		return map[string]any{
			"RequestId": "request-identity", "AccountId": accountID,
			"Arn":          "acs:ram::" + accountID + ":assumed-role/" + roleName + "/github-run",
			"IdentityType": "AssumedRoleUser", "RoleId": "role-identity-1",
		}
	case "GetRole":
		return map[string]any{"RequestId": "request-role", "Role": map[string]any{
			"RoleName": roleName, "MaxSessionDuration": 3600,
			"AssumeRolePolicyDocument": url.QueryEscape(trust),
		}}
	case "DescribeZones":
		return map[string]any{"RequestId": "request-zones", "Zones": map[string]any{"Zone": []any{
			map[string]any{"ZoneId": zoneID, "ZoneType": "AvailabilityZone", "AvailableDiskCategories": map[string]any{"DiskCategories": []string{providerDiskESSD}}},
			map[string]any{"ZoneId": "cn-hangzhou-z", "ZoneType": "AvailabilityZone", "AvailableDiskCategories": map[string]any{"DiskCategories": []string{"cloud_efficiency"}}},
			map[string]any{"ZoneId": "ignored-local", "ZoneType": "Local", "AvailableDiskCategories": map[string]any{"DiskCategories": []string{providerDiskESSD}}},
			nil,
		}}}
	case "DescribeInstanceTypes":
		return map[string]any{"RequestId": "request-types", "NextToken": "", "InstanceTypes": map[string]any{"InstanceType": []any{
			map[string]any{"InstanceTypeId": instanceType, "CpuArchitecture": providerArchitectureX86, "CpuCoreCount": 4, "MemorySize": 16, "GPUAmount": 0, "InstanceFamilyLevel": "EnterpriseLevel"},
			nil,
		}}}
	case "DescribeImages":
		return map[string]any{"RequestId": "request-images", "TotalCount": 1, "Images": map[string]any{"Image": []any{
			map[string]any{"ImageId": imageID, "CreationTime": "2026-09-01T00:00:00Z", "ImageOwnerAlias": "system", "IsSupportCloudinit": true, "Architecture": providerArchitectureX86},
		}}}
	case "DescribeAvailableResource":
		destination := form.Get("DestinationResource")
		expected := instanceType
		min, max := 0, 0
		if destination == "SystemDisk" || destination == "DataDisk" {
			expected, min, max = providerDiskESSD, 20, 2048
		}
		return map[string]any{"RequestId": "request-availability", "AvailableZones": map[string]any{"AvailableZone": []any{
			map[string]any{
				"ZoneId": zoneID, "Status": "Available", "StatusCategory": "WithStock",
				"AvailableResources": map[string]any{"AvailableResource": []any{
					map[string]any{"Type": destination, "SupportedResources": map[string]any{"SupportedResource": []any{
						map[string]any{"Value": expected, "Status": "Available", "StatusCategory": "WithStock", "Unit": "GiB", "Min": min, "Max": max},
					}}},
				}},
			},
		}}}
	case "DescribeAccountAttributes":
		return map[string]any{"RequestId": "request-vcpu", "AccountAttributeItems": map[string]any{"AccountAttributeItem": []any{
			map[string]any{"AttributeName": maxPostPaidVCPUAttribute, "AttributeValues": map[string]any{"ValueItem": []any{map[string]any{"Value": "128"}}}},
			map[string]any{"AttributeName": usedPostPaidVCPUAttribute, "AttributeValues": map[string]any{"ValueItem": []any{map[string]any{"Value": "32"}}}},
		}}}
	case "ListProductQuotas":
		return map[string]any{
			"RequestId": "request-eip", "TotalCount": 1, "NextToken": "", "MaxResults": 100,
			"Quotas": []any{map[string]any{
				"ProductCode": eipQuotaProductCode, "QuotaActionCode": "eip-quota-action",
				"QuotaCategory": eipQuotaCategory, "QuotaName": eipQuotaName,
				"TotalQuota": 20, "TotalUsage": 3,
			}},
		}
	case "ListPoliciesForRole":
		return map[string]any{"RequestId": "request-policies", "Policies": map[string]any{"Policy": []any{
			map[string]any{"PolicyName": policyName, "PolicyType": "Custom", "DefaultVersion": "v7"},
		}}}
	case "GetPolicyVersion":
		return map[string]any{"RequestId": "request-policy-version", "PolicyVersion": map[string]any{
			"VersionId": "v7", "IsDefaultVersion": true, "PolicyDocument": url.QueryEscape(policyDocument),
		}}
	case "DescribePrice":
		return map[string]any{"RequestId": "request-price", "PriceInfo": map[string]any{"Price": map[string]any{
			"Currency": "CNY", "TradePrice": 1.25,
		}}}
	case "DeleteInstance":
		return nil
	default:
		t.Errorf("unexpected Alibaba read action %q with form %#v", action, form)
		return map[string]any{"Code": "UnexpectedAction", "Message": action, "RequestId": "unexpected"}
	}
}

func identityOpenAPIHasForm(calls []identityOpenAPICall, action, key, value string) bool {
	for _, call := range calls {
		if call.action == action && call.form.Get(key) == value {
			return true
		}
	}
	return false
}

func newReadOpenAPITestClient(t *testing.T, handler http.Handler) *OpenAPI {
	t.Helper()
	newConfig := func() *openapiutil.Config {
		return (&openapiutil.Config{}).
			SetAccessKeyId("test-access-key").
			SetAccessKeySecret("test-access-secret").
			SetSecurityToken("test-security-token").
			SetProtocol("http").
			SetEndpoint("openapi.test").
			SetHttpClient(openAPITestHTTPClient{handler: handler})
	}
	ecsClient, err := ecs.NewClient(newConfig())
	if err != nil {
		t.Fatalf("create ECS test client: %v", err)
	}
	quotaClient, err := quotas.NewClient(newConfig())
	if err != nil {
		t.Fatalf("create Quota test client: %v", err)
	}
	ramClient, err := ram.NewClient(newConfig())
	if err != nil {
		t.Fatalf("create RAM test client: %v", err)
	}
	stsClient, err := sts.NewClient(newConfig())
	if err != nil {
		t.Fatalf("create STS test client: %v", err)
	}
	return &OpenAPI{region: RegionHangzhou, ecs: ecsClient, quotas: quotaClient, ram: ramClient, sts: stsClient}
}

func TestOpenAPIReadBoundaryCancellationNeverReachesProvider(t *testing.T) {
	handler := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		t.Error("canceled operation reached provider")
		writer.WriteHeader(http.StatusInternalServerError)
	})
	api := newReadOpenAPITestClient(t, handler)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := api.PostPaidVCPUQuota(ctx, RegionHangzhou, "cn-hangzhou-h"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled vCPU quota error = %v", err)
	}
	if _, err := api.EIPQuota(ctx, RegionHangzhou); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled EIP quota error = %v", err)
	}
	if _, err := api.Price(ctx, PriceRequest{Kind: PriceKindEIPTraffic, Region: RegionHangzhou, InternetCharge: providerInternetPayTraffic, PeakBandwidthMbps: 1}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled price error = %v", err)
	}
	if err := api.AssertMutationDenied(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled mutation probe error = %v", err)
	}
}
