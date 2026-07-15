package alibaba

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	openapiutil "github.com/alibabacloud-go/darabonba-openapi/v2/utils"
	"github.com/alibabacloud-go/tea/dara"
)

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
