package alibaba

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	ecs "github.com/alibabacloud-go/ecs-20140526/v7/client"
	"github.com/alibabacloud-go/tea/dara"
)

func TestDiscoverInstanceTypesPaginatesUntilNextTokenIsEmpty(t *testing.T) {
	calls := make([]string, 0, 2)
	got, err := discoverInstanceTypes(context.Background(), func(_ context.Context, token string) ([]InstanceType, string, error) {
		calls = append(calls, token)
		switch token {
		case "":
			return []InstanceType{{ID: "ecs.g8.large"}}, "page-2", nil
		case "page-2":
			return []InstanceType{{ID: "ecs.c8.large"}}, "", nil
		default:
			return nil, "", errors.New("unexpected token")
		}
	})
	if err != nil {
		t.Fatalf("discoverInstanceTypes() error = %v", err)
	}
	if !reflect.DeepEqual(calls, []string{"", "page-2"}) {
		t.Fatalf("page tokens = %#v, want initial and page-2", calls)
	}
	if len(got) != 2 || got[0].ID != "ecs.g8.large" || got[1].ID != "ecs.c8.large" {
		t.Fatalf("instance types = %#v, want both pages", got)
	}
}

func TestDiscoverInstanceTypesFailsClosedOnTokenCycle(t *testing.T) {
	_, err := discoverInstanceTypes(context.Background(), func(context.Context, string) ([]InstanceType, string, error) {
		return []InstanceType{{ID: "ecs.g8.large"}}, "cycle", nil
	})
	if !errors.Is(err, ErrDiscoveryUnavailable) {
		t.Fatalf("discoverInstanceTypes() error = %v, want ErrDiscoveryUnavailable", err)
	}
}

func TestDiscoverImagesPaginatesAndPreservesProviderEvidence(t *testing.T) {
	calls := make([]int32, 0, 2)
	got, err := discoverImages(context.Background(), func(_ context.Context, page int32) ([]Image, int32, error) {
		calls = append(calls, page)
		if page == 1 {
			items := make([]Image, discoveryPageSize)
			for index := range items {
				items[index] = Image{ID: "image-page-1-" + strconv.Itoa(index)}
			}
			return items, int32(discoveryPageSize + 1), nil
		}
		return []Image{{ID: "ubuntu_24_04_x64_20G_alibase_20260701.vhd", CreationTime: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)}}, int32(discoveryPageSize + 1), nil
	})
	if err != nil {
		t.Fatalf("discoverImages() error = %v", err)
	}
	if !reflect.DeepEqual(calls, []int32{1, 2}) {
		t.Fatalf("page numbers = %#v, want 1 and 2", calls)
	}
	if len(got) != discoveryPageSize+1 {
		t.Fatalf("images = %d, want %d", len(got), discoveryPageSize+1)
	}
}

func TestDiscoverImagesFailsClosedOnRepeatedOrChangingProviderEvidence(t *testing.T) {
	tests := []struct {
		name  string
		fetch imagePageFetcher
	}{
		{name: "duplicate across pages", fetch: func(_ context.Context, page int32) ([]Image, int32, error) {
			return []Image{{ID: "same-image"}}, 2, nil
		}},
		{name: "total changes", fetch: func(_ context.Context, page int32) ([]Image, int32, error) {
			if page == 1 {
				return []Image{{ID: "image-1"}}, 2, nil
			}
			return []Image{{ID: "image-2"}}, 1, nil
		}},
		{name: "negative total", fetch: func(context.Context, int32) ([]Image, int32, error) {
			return nil, -1, nil
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := discoverImages(context.Background(), test.fetch)
			if !errors.Is(err, ErrDiscoveryUnavailable) {
				t.Fatalf("discoverImages() error = %v, want ErrDiscoveryUnavailable", err)
			}
		})
	}
}

func TestDiscoverImagesFailsClosedWhenPositiveTotalHasMissingPage(t *testing.T) {
	_, err := discoverImages(context.Background(), func(_ context.Context, page int32) ([]Image, int32, error) {
		if page == 1 {
			return []Image{{ID: "only-one"}}, 2, nil
		}
		return nil, 2, nil
	})
	if !errors.Is(err, ErrDiscoveryUnavailable) {
		t.Fatalf("discoverImages() error = %v, want ErrDiscoveryUnavailable", err)
	}
}

func TestSupportedResourceAvailableRequiresWithStockAndRequestedDiskRange(t *testing.T) {
	newResource := func() *ecs.DescribeAvailableResourceResponseBodyAvailableZonesAvailableZoneAvailableResourcesAvailableResourceSupportedResourcesSupportedResource {
		return (&ecs.DescribeAvailableResourceResponseBodyAvailableZonesAvailableZoneAvailableResourcesAvailableResourceSupportedResourcesSupportedResource{}).
			SetValue(providerDiskESSD).
			SetStatus("Available").
			SetStatusCategory("WithStock").
			SetUnit("GiB").
			SetMin(20).
			SetMax(2_048)
	}
	tests := []struct {
		name   string
		change func(*ecs.DescribeAvailableResourceResponseBodyAvailableZonesAvailableZoneAvailableResourcesAvailableResourceSupportedResourcesSupportedResource)
		sizes  []int
		want   bool
	}{
		{name: "matching disks", sizes: []int{500, 200}, want: true},
		{name: "instance ignores range", want: true},
		{name: "closed with stock", sizes: []int{500}, change: func(value *ecs.DescribeAvailableResourceResponseBodyAvailableZonesAvailableZoneAvailableResourcesAvailableResourceSupportedResourcesSupportedResource) {
			value.SetStatus("Closed")
		}},
		{name: "without stock", sizes: []int{500}, change: func(value *ecs.DescribeAvailableResourceResponseBodyAvailableZonesAvailableZoneAvailableResourcesAvailableResourceSupportedResourcesSupportedResource) {
			value.SetStatusCategory("WithoutStock")
		}},
		{name: "one requested size below minimum", sizes: []int{500, 200}, change: func(value *ecs.DescribeAvailableResourceResponseBodyAvailableZonesAvailableZoneAvailableResourcesAvailableResourceSupportedResourcesSupportedResource) {
			value.SetMin(300)
		}},
		{name: "all requested sizes below minimum", sizes: []int{500}, change: func(value *ecs.DescribeAvailableResourceResponseBodyAvailableZonesAvailableZoneAvailableResourcesAvailableResourceSupportedResourcesSupportedResource) {
			value.SetMin(600)
		}},
		{name: "above maximum", sizes: []int{500}, change: func(value *ecs.DescribeAvailableResourceResponseBodyAvailableZonesAvailableZoneAvailableResourcesAvailableResourceSupportedResourcesSupportedResource) {
			value.SetMax(499)
		}},
		{name: "missing range", sizes: []int{500}, change: func(value *ecs.DescribeAvailableResourceResponseBodyAvailableZonesAvailableZoneAvailableResourcesAvailableResourceSupportedResourcesSupportedResource) {
			value.Max = nil
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := newResource()
			if test.change != nil {
				test.change(value)
			}
			if got := supportedResourceAvailable(value, providerDiskESSD, test.sizes); got != test.want {
				t.Fatalf("supportedResourceAvailable() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestStockStatusAvailableRejectsClosedWithStock(t *testing.T) {
	if !stockStatusAvailable("Available", "WithStock") {
		t.Fatal("stockStatusAvailable() rejected continuously replenished stock")
	}
	for _, value := range [][2]string{
		{"Closed", "WithStock"},
		{"Available", "ClosedWithStock"},
		{"Available", "WithoutStock"},
	} {
		if stockStatusAvailable(value[0], value[1]) {
			t.Fatalf("stockStatusAvailable(%q, %q) = true, want false", value[0], value[1])
		}
	}
}

func TestParsePriceMicrosUsesConservativeDecimalConversion(t *testing.T) {
	tests := []struct {
		name     string
		currency string
		value    *float32
		want     int64
		wantErr  bool
	}{
		{name: "traffic unit", currency: "CNY", value: float32Pointer(0.8), want: 800_000},
		{name: "fractional micros round upward", currency: "CNY", value: float32Pointer(1.0000005), want: 1_000_001},
		{name: "missing", currency: "CNY", wantErr: true},
		{name: "zero", currency: "CNY", value: float32Pointer(0), wantErr: true},
		{name: "negative", currency: "CNY", value: float32Pointer(-1), wantErr: true},
		{name: "nan", currency: "CNY", value: float32Pointer(float32(math.NaN())), wantErr: true},
		{name: "currency", currency: "", value: float32Pointer(1), wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parsePriceMicros(test.currency, test.value)
			if (err != nil) != test.wantErr {
				t.Fatalf("parsePriceMicros() error = %v, wantErr %v", err, test.wantErr)
			}
			if got.UnitCostMicros != test.want || got.Currency != func() string {
				if test.wantErr {
					return ""
				}
				return test.currency
			}() {
				t.Fatalf("parsePriceMicros() = %#v, want %s/%d", got, test.currency, test.want)
			}
		})
	}
}

func TestWholeQuotaValueFailsClosedOnNonIntegralProviderValues(t *testing.T) {
	tests := []struct {
		name  string
		value *float32
		want  int64
		ok    bool
	}{
		{name: "whole", value: float32Pointer(20), want: 20, ok: true},
		{name: "zero", value: float32Pointer(0), ok: true},
		{name: "fraction", value: float32Pointer(1.5)},
		{name: "negative", value: float32Pointer(-1)},
		{name: "nan", value: float32Pointer(float32(math.NaN()))},
		{name: "missing"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := wholeQuotaValue(test.value)
			if got != test.want || ok != test.ok {
				t.Fatalf("wholeQuotaValue() = %d/%v, want %d/%v", got, ok, test.want, test.ok)
			}
		})
	}
}

func TestRequiredQuoteActionsAreReadOnlyAndExact(t *testing.T) {
	want := []string{
		"ecs:DescribeAccountAttributes",
		"ecs:DescribeAvailableResource",
		"ecs:DescribeImages",
		"ecs:DescribeInstanceTypes",
		"ecs:DescribePrice",
		"ecs:DescribeZones",
		"quotas:GetProductQuota",
		"ram:GetPolicyVersion",
		"ram:ListPoliciesForRole",
		"sts:GetCallerIdentity",
	}
	if got := RequiredQuoteActions(); !reflect.DeepEqual(got, want) {
		t.Fatalf("RequiredQuoteActions() = %#v, want %#v", got, want)
	}
}

func TestRequiredLifecycleActionsAreExplicitAndSeparated(t *testing.T) {
	observe := RequiredLifecycleObserveActions()
	provision := RequiredLifecycleProvisionActions()
	release := RequiredLifecycleReleaseActions()
	for name, actions := range map[string][]string{"observe": observe, "provision": provision, "release": release} {
		seen := make(map[string]struct{}, len(actions))
		for _, action := range actions {
			if action == "" || strings.Contains(action, "*") {
				t.Fatalf("%s action = %q, want exact non-wildcard", name, action)
			}
			if _, exists := seen[action]; exists {
				t.Fatalf("%s duplicate action = %q", name, action)
			}
			seen[action] = struct{}{}
		}
	}
	for _, action := range []string{"ecs:RunInstances", "vpc:AllocateEipAddress", "ecs:AuthorizeSecurityGroup"} {
		if !slices.Contains(provision, action) || slices.Contains(observe, action) || slices.Contains(release, action) {
			t.Fatalf("provision action separation failed for %q", action)
		}
	}
	for _, action := range []string{"ecs:DeleteInstance", "vpc:ReleaseEipAddress", "vpc:DeleteRouteEntry"} {
		if !slices.Contains(release, action) || slices.Contains(observe, action) || slices.Contains(provision, action) {
			t.Fatalf("release action separation failed for %q", action)
		}
	}
}

func TestRequiredBillingObserveActionsAreExactAndReadOnly(t *testing.T) {
	want := []string{"bssapi:QueryBill", "bssapi:QueryBillOverview", "bssapi:QueryInstanceBill"}
	if got := RequiredBillingObserveActions(); !reflect.DeepEqual(got, want) {
		t.Fatalf("RequiredBillingObserveActions() = %#v, want %#v", got, want)
	}
}

func TestQuoteRolePolicyDocumentIsCanonicalExactReadAllowlist(t *testing.T) {
	document := QuoteRolePolicyDocument()
	var decoded struct {
		Version   string `json:"Version"`
		Statement []struct {
			Effect   string   `json:"Effect"`
			Action   []string `json:"Action"`
			Resource []string `json:"Resource"`
		} `json:"Statement"`
	}
	if err := json.Unmarshal([]byte(document), &decoded); err != nil {
		t.Fatalf("QuoteRolePolicyDocument() decode error = %v", err)
	}
	if decoded.Version != "1" || len(decoded.Statement) != 1 || decoded.Statement[0].Effect != "Allow" ||
		!reflect.DeepEqual(decoded.Statement[0].Action, RequiredQuoteActions()) ||
		!reflect.DeepEqual(decoded.Statement[0].Resource, []string{"*"}) {
		t.Fatalf("QuoteRolePolicyDocument() = %s, want exact read allowlist", document)
	}
	if normalized := normalizeRAMPolicyDocument(url.QueryEscape(document)); normalized != document {
		t.Fatalf("normalizeRAMPolicyDocument() = %q, want %q", normalized, document)
	}
}

func TestPrincipalHasRoleMatchesOneExactRolePathSegment(t *testing.T) {
	tests := []struct {
		name string
		arn  string
		role string
		want bool
	}{
		{name: "assumed role", arn: "acs:ram::123:assumed-role/CloudLeaseQuote/session", role: "CloudLeaseQuote", want: true},
		{name: "role", arn: "acs:ram::123:role/CloudLeaseQuote/session", role: "CloudLeaseQuote", want: true},
		{name: "canonical lowercase ARN", arn: "acs:ram::123:role/cloudleasequote/session", role: "CloudLeaseQuote", want: true},
		{name: "substring", arn: "acs:ram::123:assumed-role/CloudLeaseQuoteAdmin/session", role: "CloudLeaseQuote"},
		{name: "wrong identity", arn: "acs:ram::123:user/CloudLeaseQuote", role: "CloudLeaseQuote"},
		{name: "role path injection", arn: "acs:ram::123:assumed-role/CloudLeaseQuote/session", role: "CloudLeaseQuote/session"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := principalHasRole(test.arn, test.role); got != test.want {
				t.Fatalf("principalHasRole(%q, %q) = %v, want %v", test.arn, test.role, got, test.want)
			}
		})
	}
}

func TestRAMPermissionDeniedRequiresAnAuthorizationErrorCode(t *testing.T) {
	tests := []struct {
		name   string
		status int
		code   string
		want   bool
	}{
		{name: "RAM forbidden", status: 403, code: "Forbidden.RAM", want: true},
		{name: "sub-user forbidden", status: 403, code: "Forbbiden.SubUser", want: true},
		{name: "not found", status: 404, code: "InvalidInstanceId.NotFound"},
		{name: "other forbidden", status: 403, code: "IncorrectInstanceStatus"},
		{name: "dry run allowed", status: 400, code: "DRYRUN.SUCCESS"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := &dara.SDKError{StatusCode: dara.Int(test.status), Code: dara.String(test.code)}
			if got := ramPermissionDenied(err); got != test.want {
				t.Fatalf("ramPermissionDenied() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestNewOpenAPIFromOIDCEnvironmentRequiresTemporaryCredentials(t *testing.T) {
	t.Setenv(credentialAccessKeyIDEnv, "temporary-access-key")
	t.Setenv(credentialAccessKeySecretEnv, "temporary-secret")
	t.Setenv(credentialSecurityTokenEnv, "")

	_, err := NewOpenAPIFromOIDCEnvironment(RegionHangzhou)
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("NewOpenAPIFromOIDCEnvironment() error = %v, want ErrInvalidConfig", err)
	}
}

func float32Pointer(value float32) *float32 { return &value }
