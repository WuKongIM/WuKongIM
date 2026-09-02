package alibaba

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/netip"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudlease"
	"github.com/alibabacloud-go/tea/dara"
)

func TestReadOnlyOpenAPICannotReachLifecycleMethods(t *testing.T) {
	api := &OpenAPI{region: RegionHangzhou}
	if api.lifecycleReady() {
		t.Fatal("quote OpenAPI unexpectedly authorizes paid lifecycle methods")
	}
	_, err := api.CreateNetwork(context.Background(), NetworkCreateRequest{
		Region: RegionHangzhou, Zone: "cn-hangzhou-h", VPCIPv4CIDR: "invalid",
		VSwitchIPv4CIDR: "10.42.0.0/24", ClientToken: "token",
	})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("CreateNetwork() error = %v, want ErrInvalidConfig", err)
	}
}

func TestLifecycleOpenAPIRequiresExactExplicitAuthorization(t *testing.T) {
	t.Setenv(lifecycleAuthorizationEnv, "")
	if _, err := NewLifecycleOpenAPIFromOIDCEnvironment(RegionHangzhou); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("missing authorization error = %v, want ErrInvalidConfig", err)
	}
	t.Setenv(lifecycleAuthorizationEnv, lifecycleAuthorizationValue+"-almost")
	if _, err := NewLifecycleOpenAPIFromOIDCEnvironment(RegionHangzhou); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("wrong authorization error = %v, want ErrInvalidConfig", err)
	}
	t.Setenv(lifecycleAuthorizationEnv, lifecycleAuthorizationValue)
	t.Setenv(credentialAccessKeyIDEnv, "")
	if _, err := NewLifecycleOpenAPIFromOIDCEnvironment(RegionHangzhou); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("authorized without temporary credentials error = %v, want ErrInvalidConfig", err)
	}
	t.Setenv(credentialAccessKeyIDEnv, "temporary-access-key")
	t.Setenv(credentialAccessKeySecretEnv, "temporary-secret")
	t.Setenv(credentialSecurityTokenEnv, "temporary-token")
	api, err := NewLifecycleOpenAPIFromOIDCEnvironment(RegionHangzhou)
	if err != nil || !api.lifecycleReady() {
		t.Fatalf("authorized constructor = %#v, %v, want lifecycle-ready SDK clients", api, err)
	}
}

func TestInventoryOpenAPIUsesTemporaryOIDCWithoutPaidMutationAuthorization(t *testing.T) {
	t.Setenv(credentialAccessKeyIDEnv, "temporary-id")
	t.Setenv(credentialAccessKeySecretEnv, "temporary-secret")
	t.Setenv(credentialSecurityTokenEnv, "temporary-token")
	t.Setenv(lifecycleAuthorizationEnv, "")
	api, err := NewInventoryOpenAPIFromOIDCEnvironment(RegionHangzhou)
	if err != nil {
		t.Fatalf("NewInventoryOpenAPIFromOIDCEnvironment() error = %v", err)
	}
	if !api.inventoryReady() || api.lifecycleReady() || api.lifecycleAuthorized {
		t.Fatal("inventory OpenAPI did not preserve the read-only lifecycle boundary")
	}
}

func TestLifecycleListedInstanceDecodesNestedVPCRelationship(t *testing.T) {
	var instance lifecycleListedInstance
	err := decodeLifecycleSDKBody(map[string]any{
		"InstanceId": "i-load-1",
		"VpcAttributes": map[string]any{
			"VpcId":     "vpc-1",
			"VSwitchId": "vsw-1",
			"PrivateIpAddress": map[string]any{
				"IpAddress": []string{"10.42.0.10"},
			},
		},
	}, &instance)
	if err != nil {
		t.Fatal(err)
	}
	vpcID, vSwitchID := instance.networkIDs()
	if vpcID != "vpc-1" || vSwitchID != "vsw-1" {
		t.Fatalf("networkIDs() = %q/%q, want nested VPC and vSwitch relationships", vpcID, vSwitchID)
	}
}

func TestQuoteOpenAPIConstructorKeepsLifecycleGuardFalse(t *testing.T) {
	t.Setenv(credentialAccessKeyIDEnv, "temporary-access-key")
	t.Setenv(credentialAccessKeySecretEnv, "temporary-secret")
	t.Setenv(credentialSecurityTokenEnv, "temporary-token")
	t.Setenv(lifecycleAuthorizationEnv, lifecycleAuthorizationValue)
	api, err := NewOpenAPIFromOIDCEnvironment(RegionHangzhou)
	if err != nil {
		t.Fatal(err)
	}
	if api.lifecycleReady() || api.lifecycleAuthorized {
		t.Fatal("Quote constructor inherited lifecycle authorization from ambient environment")
	}
}

func TestSecurityRuleDescriptionRoundTripsExactQuintuple(t *testing.T) {
	until := time.Date(2026, 8, 10, 12, 0, 0, 123456789, time.UTC)
	request := AccessRuleRequest{
		Kind: AccessRuleGrant, ID: "load-http", TargetRole: "load", Protocol: cloudlease.ProtocolTCP,
		PortFrom: 80, PortTo: 80, SourcePrefix: netip.MustParsePrefix("0.0.0.0/0"),
		DestinationPrefix: netip.MustParsePrefix("10.42.0.13/32"), Until: until,
		Tags: map[string]string{cloudlease.TagLeaseID: "lease-123"},
	}
	description, err := securityRuleDescription(request)
	if err != nil {
		t.Fatal(err)
	}
	decoded, ok := parseSecurityRuleDescription(description)
	if !ok || decoded.LeaseID != "lease-123" || decoded.ID != "load-http" || decoded.TargetRole != "load" ||
		decoded.Source != "0.0.0.0/0" || decoded.Destination != "10.42.0.13/32" || decoded.PortFrom != 80 ||
		decoded.UntilUnix != until.Unix() || decoded.UntilNanosecond != int32(until.Nanosecond()) {
		t.Fatalf("parseSecurityRuleDescription() = %#v/%v", decoded, ok)
	}
	permission := lifecycleSecurityPermission{
		RuleID: "rule-1", Description: description, IPProtocol: "TCP", PortRange: "80/80",
		SourceCIDRIP: "0.0.0.0/0", DestCIDRIP: "10.42.0.13/32",
	}
	if !lifecyclePermissionMatchesDescription(permission, decoded) {
		t.Fatal("provider quintuple did not match its owned description")
	}
	asset := lifecycleSecurityRuleAsset("sg-1", map[string]string{
		cloudlease.TagResourceRole: "network", cloudlease.TagManagedBy: cloudlease.ManagedByValue,
	}, permission)
	if asset.IdentityInherited || asset.Attributes["rule_kind"] != string(AccessRuleGrant) || asset.Grant == nil ||
		asset.Grant.ID != "load-http" || !asset.Grant.Until.Equal(until) {
		t.Fatalf("owned rule asset = %#v", asset)
	}
	permission.DestCIDRIP = "10.42.0.14/32"
	asset = lifecycleSecurityRuleAsset("sg-1", map[string]string{cloudlease.TagResourceRole: "network"}, permission)
	if !asset.IdentityInherited || asset.Attributes["rule_kind"] != "unknown" || asset.Grant != nil {
		t.Fatalf("divergent provider rule asset = %#v, want cleanup-only unknown", asset)
	}
}

func TestSecurityRuleDescriptionReadsLegacySecondPrecision(t *testing.T) {
	until := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	legacy, err := json.Marshal(encodedSecurityRule{
		LeaseID: "lease-123", Kind: string(AccessRuleGrant), ID: "load-http", TargetRole: "load",
		Protocol: string(cloudlease.ProtocolTCP), PortFrom: 80, PortTo: 80, Source: "0.0.0.0/0",
		Destination: "10.42.0.13/32", UntilUnix: until.Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	description := securityRulePrefix + base64.RawURLEncoding.EncodeToString(legacy)
	decoded, ok := parseSecurityRuleDescription(description)
	if !ok || decoded.UntilNanosecond != 0 {
		t.Fatalf("parseSecurityRuleDescription(legacy) = %#v/%v", decoded, ok)
	}
	permission := lifecycleSecurityPermission{
		RuleID: "rule-1", Description: description, IPProtocol: "TCP", PortRange: "80/80",
		SourceCIDRIP: "0.0.0.0/0", DestCIDRIP: "10.42.0.13/32",
	}
	asset := lifecycleSecurityRuleAsset("sg-1", map[string]string{
		cloudlease.TagResourceRole: "network", cloudlease.TagManagedBy: cloudlease.ManagedByValue,
	}, permission)
	if asset.Grant == nil || !asset.Grant.Until.Equal(until) {
		t.Fatalf("legacy owned rule asset = %#v", asset)
	}
}

func TestSecurityRuleDescriptionRejectsMalformedOwnership(t *testing.T) {
	for _, value := range []string{"", securityRulePrefix, securityRulePrefix + "not-base64", "foreign-rule"} {
		if _, ok := parseSecurityRuleDescription(value); ok {
			t.Fatalf("parseSecurityRuleDescription(%q) accepted malformed rule", value)
		}
	}
}

func TestLifecycleInventoryPrefersTaggedRootAndRejectsDuplicates(t *testing.T) {
	inventory := newLifecycleInventory()
	inherited := LifecycleAsset{ID: "disk-1", Kind: ResourceKindDisk, ParentID: "i-1", IdentityInherited: true}
	actual := LifecycleAsset{ID: "disk-1", Kind: ResourceKindDisk, ParentID: "i-1", Role: "load"}
	if err := inventory.addRelated(inherited); err != nil {
		t.Fatal(err)
	}
	if err := inventory.addActual(actual); err != nil {
		t.Fatal(err)
	}
	if got := inventory.assets(); len(got) != 1 || got[0].IdentityInherited || got[0].Role != "load" {
		t.Fatalf("inventory assets = %#v", got)
	}
	if err := inventory.addActual(actual); !errors.Is(err, ErrAmbiguousInventory) {
		t.Fatalf("duplicate tagged root error = %v", err)
	}
	conflict := LifecycleAsset{ID: "disk-1", Kind: ResourceKindDisk, ParentID: "i-2"}
	if err := inventory.addRelated(conflict); !errors.Is(err, ErrAmbiguousInventory) {
		t.Fatalf("relationship conflict error = %v", err)
	}
}

func TestLifecyclePaginatorsFailClosedOnChangingEvidence(t *testing.T) {
	page := 0
	_, err := lifecycleCollectPages(context.Background(), func(int32) ([]string, int, error) {
		page++
		if page == 1 {
			return []string{"one"}, 2, nil
		}
		return []string{"two"}, 3, nil
	})
	if !errors.Is(err, ErrAmbiguousInventory) {
		t.Fatalf("changing total error = %v", err)
	}

	calls := make([]string, 0, 2)
	_, err = lifecycleCollectTokenPages(context.Background(), func(token string) ([]string, string, error) {
		calls = append(calls, token)
		return []string{"item"}, "same", nil
	})
	if !errors.Is(err, ErrAmbiguousInventory) || !reflect.DeepEqual(calls, []string{"", "same"}) {
		t.Fatalf("token cycle = %#v, %v", calls, err)
	}
}

func TestOpenAPIResourceTagsRejectsProviderTagOverflow(t *testing.T) {
	tags := make(map[string]string, 20)
	for index := 0; index < 20; index++ {
		tags["key-"+strconv.Itoa(index)] = "value"
	}
	if _, err := openAPIResourceTags(tags, "load"); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("openAPIResourceTags() error = %v, want ErrInvalidConfig", err)
	}
}

func TestLifecycleCloudInitCreatesOnlyKeyBasedDeploymentUser(t *testing.T) {
	access := lifecycleBootstrap(t)
	keys := make([]string, len(access.AuthorizedKeys))
	for index := range access.AuthorizedKeys {
		keys[index] = strings.TrimSpace(access.AuthorizedKeys[index])
	}
	encoded, err := lifecycleCloudInit(keys)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	document := string(decoded)
	for _, want := range []string{"name: wkdeploy", "lock_passwd: true", "ssh_pwauth: false", "disable_root: true", keys[0], keys[1]} {
		if !strings.Contains(document, want) {
			t.Fatalf("cloud-init missing %q:\n%s", want, document)
		}
	}
	if strings.Contains(strings.ToLower(document), "\n    password:") || strings.Contains(strings.ToLower(document), "\n    passwd:") {
		t.Fatalf("cloud-init contains password material:\n%s", document)
	}
}

func TestCreatedDiskMustBelongToTheNewInstance(t *testing.T) {
	disk := createdDiskJSON{
		DiskID: "d-system", InstanceID: "i-foreign", Type: "system",
		Category: providerDiskESSD, PerformanceLevel: providerDiskLevelPL0, Size: 40,
	}
	if validCreatedDisk(disk, "i-created", 40) {
		t.Fatal("created disk from a different instance was accepted")
	}
	disk.InstanceID = "i-created"
	if !validCreatedDisk(disk, "i-created", 40) {
		t.Fatal("exact created-instance disk was rejected")
	}
}

func TestLifecycleResourceIdentityHelpersStayCanonicalAndFailClosed(t *testing.T) {
	base := map[string]string{"z-key": "last", "a-key": "first"}
	tags, err := openAPIResourceTags(base, "load")
	if err != nil {
		t.Fatal(err)
	}
	if _, mutated := base[cloudlease.TagResourceRole]; mutated || tags[cloudlease.TagResourceRole] != "load" {
		t.Fatalf("openAPIResourceTags() mutated caller or omitted role: base=%#v tags=%#v", base, tags)
	}
	wantPairs := [][2]string{{"a-key", "first"}, {cloudlease.TagResourceRole, "load"}, {"z-key", "last"}}
	if got := sortedLifecycleTagPairs(tags); !reflect.DeepEqual(got, wantPairs) {
		t.Fatalf("sortedLifecycleTagPairs() = %#v, want %#v", got, wantPairs)
	}
	if got := openAPIResourceName("lease_one/two:three", "vpc"); got != "wklease-lease-one-two-three-vpc" {
		t.Fatalf("openAPIResourceName() = %q", got)
	}
	if alreadyAbsentError(errors.New("provider endpoint not found")) {
		t.Fatal("ordinary transport error was treated as proof that a cloud asset is absent")
	}
	if !alreadyAbsentError(&dara.SDKError{Code: dara.String("InvalidInstanceId.NotFound")}) {
		t.Fatal("Alibaba NotFound resource code was not recognized as idempotent deletion")
	}
}
