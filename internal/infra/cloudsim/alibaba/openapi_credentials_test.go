package alibaba

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudsim"
	openapiutil "github.com/alibabacloud-go/darabonba-openapi/v2/utils"
	"github.com/alibabacloud-go/tea/dara"
)

func TestCreateHostRetriesAttachWhileInstanceStatusConverges(t *testing.T) {
	var (
		mu          sync.Mutex
		attachCalls int
	)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_ = request.ParseForm()
		action := request.Header.Get("x-acs-action")
		response.Header().Set("Content-Type", "application/json")
		switch action {
		case "RunInstances":
			_, _ = response.Write([]byte(`{"RequestId":"request-1","InstanceIdSets":{"InstanceIdSet":["i-1"]}}`))
		case "CreateDisk":
			_, _ = response.Write([]byte(`{"RequestId":"request-2","DiskId":"d-1"}`))
		case "DescribeDisks":
			if request.Form.Get("DiskIds") != "" {
				_, _ = response.Write([]byte(`{"RequestId":"request-3","Disks":{"Disk":[{"DiskId":"d-1","Status":"Available"}]}}`))
				return
			}
			_, _ = response.Write([]byte(`{"RequestId":"request-7","TotalCount":1,"Disks":{"Disk":[{"DiskId":"d-1","InstanceId":"i-1","Tags":{"Tag":[{"TagKey":"wukongim-resource-role","TagValue":"node-2"}]}}]}}`))
		case "DescribeInstances":
			if request.Form.Get("InstanceIds") != "" {
				_, _ = response.Write([]byte(`{"RequestId":"request-4","Instances":{"Instance":[{"InstanceId":"i-1","Status":"Running"}]}}`))
				return
			}
			_, _ = response.Write([]byte(`{"RequestId":"request-6","TotalCount":1,"Instances":{"Instance":[{"InstanceId":"i-1","VpcAttributes":{"PrivateIpAddress":{"IpAddress":["10.42.0.21"]}},"Tags":{"Tag":[{"TagKey":"wukongim-resource-role","TagValue":"node-2"}]}}]}}`))
		case "AttachDisk":
			mu.Lock()
			attachCalls++
			call := attachCalls
			mu.Unlock()
			if call == 1 {
				response.WriteHeader(http.StatusBadRequest)
				_, _ = response.Write([]byte(`{"RequestId":"request-5","Code":"IncorrectInstanceStatus","Message":"The current status of the resource does not support this operation."}`))
				return
			}
			_, _ = response.Write([]byte(`{"RequestId":"request-5-retry"}`))
		case "DescribeSecurityGroups", "DescribeVpcs", "DescribeVSwitches", "DescribeEipAddresses":
			_, _ = response.Write([]byte(`{"RequestId":"request-8","TotalCount":0}`))
		case "DeleteInstance", "DeleteDisk":
			_, _ = response.Write([]byte(`{"RequestId":"rollback"}`))
		default:
			http.Error(response, "unexpected action "+action, http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)

	api, err := newOpenAPI(&openapiutil.Config{
		AccessKeyId:     dara.String("test-access-key-id"),
		AccessKeySecret: dara.String("test-access-key-secret"),
		RegionId:        dara.String("cn-hangzhou"),
		Endpoint:        dara.String(strings.TrimPrefix(server.URL, "http://")),
		Protocol:        dara.String("http"),
	})
	if err != nil {
		t.Fatalf("newOpenAPI() error = %v", err)
	}
	api.pollInterval = 0
	api.waitTimeout = time.Second

	assets, err := api.CreateHost(context.Background(), HostRequest{
		Region: "cn-hangzhou", ZoneID: "cn-hangzhou-a", Role: "node-2",
		ImageID: "aliyun_3_x64_20G_alibase_20260101.vhd", InstanceType: "ecs.c7.large",
		VSwitchID: "vsw-1", SecurityGroupID: "sg-1", PrivateIPv4: "10.42.0.21",
		SystemDiskCategory: "cloud_essd", SystemDiskSizeGiB: 40,
		DataDiskCategory: "cloud_essd", DataDiskSizeGiB: 40,
		AutoReleaseAt: time.Now().Add(3 * time.Hour), SSHPublicKey: "ssh-ed25519 AAAATEST run",
		Tags: map[string]string{
			cloudsim.TagManagedBy: cloudsim.ManagedByValue,
			cloudsim.TagRunID:     "run-1", cloudsim.TagResourceRole: "node-2",
		},
	})
	if err != nil {
		t.Fatalf("CreateHost() error = %v", err)
	}
	if len(assets) != 2 || assets[0].Role != "node-2" || assets[1].Role != "node-2" {
		t.Fatalf("CreateHost() assets = %#v, want node-2 compute and disk", assets)
	}
	mu.Lock()
	defer mu.Unlock()
	if attachCalls != 2 {
		t.Fatalf("AttachDisk calls = %d, want one bounded retry", attachCalls)
	}
}

func TestCreateNetworkWaitsForVSwitchBeforeCreatingSecurityGroup(t *testing.T) {
	var (
		mu             sync.Mutex
		actions        []string
		vSwitchQueries int
	)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		action := request.Header.Get("x-acs-action")
		mu.Lock()
		actions = append(actions, action)
		if action == "DescribeVSwitchAttributes" {
			vSwitchQueries++
		}
		query := vSwitchQueries
		mu.Unlock()

		response.Header().Set("Content-Type", "application/json")
		switch action {
		case "CreateVpc":
			_, _ = response.Write([]byte(`{"RequestId":"request-1","VpcId":"vpc-1"}`))
		case "DescribeVpcAttribute":
			_, _ = response.Write([]byte(`{"RequestId":"request-2","VpcId":"vpc-1","Status":"Available"}`))
		case "CreateVSwitch":
			_, _ = response.Write([]byte(`{"RequestId":"request-3","VSwitchId":"vsw-1"}`))
		case "DescribeVSwitchAttributes":
			status := "Pending"
			if query > 1 {
				status = "Available"
			}
			_, _ = response.Write([]byte(`{"RequestId":"request-4","VSwitchId":"vsw-1","Status":"` + status + `"}`))
		case "CreateSecurityGroup":
			_, _ = response.Write([]byte(`{"RequestId":"request-5","SecurityGroupId":"sg-1"}`))
		case "AuthorizeSecurityGroup":
			_, _ = response.Write([]byte(`{"RequestId":"request-6"}`))
		default:
			http.Error(response, "unexpected action "+action, http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)

	api, err := newOpenAPI(&openapiutil.Config{
		AccessKeyId:     dara.String("test-access-key-id"),
		AccessKeySecret: dara.String("test-access-key-secret"),
		RegionId:        dara.String("cn-hangzhou"),
		Endpoint:        dara.String(strings.TrimPrefix(server.URL, "http://")),
		Protocol:        dara.String("http"),
	})
	if err != nil {
		t.Fatalf("newOpenAPI() error = %v", err)
	}
	api.pollInterval = 0
	api.waitTimeout = time.Second

	assets, err := api.CreateNetwork(context.Background(), NetworkRequest{
		Region: "cn-hangzhou", ZoneID: "cn-hangzhou-a",
		VPCIPv4CIDR: "10.42.0.0/16", VSwitchIPv4CIDR: "10.42.0.0/24",
		Tags: map[string]string{"wukongim:run-id": "run-1"},
	})
	if err != nil {
		t.Fatalf("CreateNetwork() error = %v", err)
	}
	if len(assets) != 3 {
		t.Fatalf("CreateNetwork() assets = %v, want three assets", assets)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{
		"CreateVpc", "DescribeVpcAttribute", "CreateVSwitch",
		"DescribeVSwitchAttributes", "DescribeVSwitchAttributes",
		"CreateSecurityGroup", "AuthorizeSecurityGroup",
	}
	if !reflect.DeepEqual(actions, want) {
		t.Fatalf("Alibaba actions = %v, want %v", actions, want)
	}
}

func TestNewOpenAPIFromEnvironmentAcceptsCredentialModes(t *testing.T) {
	tests := []struct {
		name            string
		accessKeyID     string
		accessKeySecret string
		securityToken   string
		wantError       bool
	}{
		{
			name:            "long-lived access key",
			accessKeyID:     "test-access-key-id",
			accessKeySecret: "test-access-key-secret",
		},
		{
			name:            "OIDC exchanged STS credential",
			accessKeyID:     "test-access-key-id",
			accessKeySecret: "test-access-key-secret",
			securityToken:   "test-security-token",
		},
		{name: "missing credentials", wantError: true},
		{name: "missing secret", accessKeyID: "test-access-key-id", wantError: true},
		{name: "missing id", accessKeySecret: "test-access-key-secret", wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("ALIBABA_CLOUD_ACCESS_KEY_ID", test.accessKeyID)
			t.Setenv("ALIBABA_CLOUD_ACCESS_KEY_SECRET", test.accessKeySecret)
			t.Setenv("ALIBABA_CLOUD_SECURITY_TOKEN", test.securityToken)

			_, err := NewOpenAPIFromEnvironment("cn-hangzhou")
			if test.wantError && err == nil {
				t.Fatal("NewOpenAPIFromEnvironment() error = nil, want error")
			}
			if !test.wantError && err != nil {
				t.Fatalf("NewOpenAPIFromEnvironment() error = %v", err)
			}
		})
	}
}

func TestNewOpenAPIFromEnvironmentRejectsMissingRegion(t *testing.T) {
	t.Setenv("ALIBABA_CLOUD_ACCESS_KEY_ID", "test-access-key-id")
	t.Setenv("ALIBABA_CLOUD_ACCESS_KEY_SECRET", "test-access-key-secret")
	t.Setenv("ALIBABA_CLOUD_SECURITY_TOKEN", "")

	if _, err := NewOpenAPIFromEnvironment(""); err == nil {
		t.Fatal("NewOpenAPIFromEnvironment() error = nil, want error")
	}
}

func TestEligibleSpotZonesUsesAlibabaSDKRequestRuntime(t *testing.T) {
	actions := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		actions <- request.Header.Get("x-acs-action")
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
			"RequestId":"request-1",
			"Zones":{"Zone":[{
				"ZoneId":"cn-hangzhou-a",
				"ZoneType":"AvailabilityZone",
				"AvailableDiskCategories":{"DiskCategories":["cloud_essd"]}
			}]}
		}`))
	}))
	t.Cleanup(server.Close)

	api, err := newOpenAPI(&openapiutil.Config{
		AccessKeyId:     dara.String("test-access-key-id"),
		AccessKeySecret: dara.String("test-access-key-secret"),
		RegionId:        dara.String("cn-hangzhou"),
		Endpoint:        dara.String(strings.TrimPrefix(server.URL, "http://")),
		Protocol:        dara.String("http"),
	})
	if err != nil {
		t.Fatalf("newOpenAPI() error = %v", err)
	}

	zones, err := api.EligibleSpotZones(context.Background(), "cn-hangzhou")
	if err != nil {
		t.Fatalf("EligibleSpotZones() error = %v", err)
	}
	if !reflect.DeepEqual(zones, []string{"cn-hangzhou-a"}) {
		t.Fatalf("EligibleSpotZones() = %v, want [cn-hangzhou-a]", zones)
	}
	if action := <-actions; action != "DescribeZones" {
		t.Fatalf("Action = %q, want DescribeZones", action)
	}
}

func TestListAssetsUsesAlibabaAPIPageLimits(t *testing.T) {
	type observedRequest struct {
		action   string
		pageSize string
	}
	requests := make(chan observedRequest, 6)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_ = request.ParseForm()
		requests <- observedRequest{
			action:   request.Header.Get("x-acs-action"),
			pageSize: request.Form.Get("PageSize"),
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"RequestId":"request-1","PageNumber":1,"TotalCount":0}`))
	}))
	t.Cleanup(server.Close)

	api, err := newOpenAPI(&openapiutil.Config{
		AccessKeyId:     dara.String("test-access-key-id"),
		AccessKeySecret: dara.String("test-access-key-secret"),
		RegionId:        dara.String("cn-hangzhou"),
		Endpoint:        dara.String(strings.TrimPrefix(server.URL, "http://")),
		Protocol:        dara.String("http"),
	})
	if err != nil {
		t.Fatalf("newOpenAPI() error = %v", err)
	}

	assets, err := api.ListAssets(context.Background(), ListAssetsRequest{Region: "cn-hangzhou", RunID: "run-1"})
	if err != nil {
		t.Fatalf("ListAssets() error = %v", err)
	}
	if len(assets) != 0 {
		t.Fatalf("ListAssets() = %v, want empty inventory", assets)
	}

	wantPageSizes := map[string]string{
		"DescribeInstances":      "100",
		"DescribeDisks":          "100",
		"DescribeSecurityGroups": "100",
		"DescribeVpcs":           "50",
		"DescribeVSwitches":      "50",
		"DescribeEipAddresses":   "100",
	}
	for range wantPageSizes {
		observed := <-requests
		want, exists := wantPageSizes[observed.action]
		if !exists {
			t.Fatalf("unexpected Alibaba action %q", observed.action)
		}
		if observed.pageSize != want {
			t.Fatalf("%s PageSize = %q, want %q", observed.action, observed.pageSize, want)
		}
		delete(wantPageSizes, observed.action)
	}
}

func TestDiscoverLatestLinuxImageReadsEveryPage(t *testing.T) {
	var pages []int32
	imageID, err := discoverLatestLinuxImage(context.Background(), func(_ context.Context, pageNumber, pageSize int32) ([]linuxImageCandidate, int32, error) {
		pages = append(pages, pageNumber)
		if pageSize != discoveryPageSize {
			t.Fatalf("page size = %d, want %d", pageSize, discoveryPageSize)
		}
		if pageNumber == 1 {
			images := make([]linuxImageCandidate, discoveryPageSize)
			images[0] = linuxImageCandidate{ID: "older", CreationTime: "2026-07-01T00:00:00Z", SupportsCloudInit: true}
			return images, discoveryPageSize + 1, nil
		}
		return []linuxImageCandidate{{ID: "newest", CreationTime: "2026-07-15T00:00:00Z", SupportsCloudInit: true}}, discoveryPageSize + 1, nil
	})
	if err != nil {
		t.Fatalf("discoverLatestLinuxImage() error = %v", err)
	}
	if imageID != "newest" {
		t.Fatalf("image ID = %q, want newest", imageID)
	}
	if !reflect.DeepEqual(pages, []int32{1, 2}) {
		t.Fatalf("pages = %v, want [1 2]", pages)
	}
}
