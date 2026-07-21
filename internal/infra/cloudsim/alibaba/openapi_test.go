package alibaba

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudsim"
	openapiutil "github.com/alibabacloud-go/darabonba-openapi/v2/utils"
	"github.com/alibabacloud-go/tea/dara"
	"gopkg.in/yaml.v3"
)

func TestNewOpenAPIAppliesBoundedTransportTimeouts(t *testing.T) {
	api, err := newOpenAPI(&openapiutil.Config{
		AccessKeyId:     dara.String("test-access-key"),
		AccessKeySecret: dara.String("test-access-secret"),
		RegionId:        dara.String("cn-hangzhou"),
	})
	if err != nil {
		t.Fatalf("newOpenAPI() error = %v", err)
	}
	if api.ecs.ConnectTimeout == nil || *api.ecs.ConnectTimeout <= 0 {
		t.Fatalf("ECS ConnectTimeout = %v, want a bounded positive timeout", api.ecs.ConnectTimeout)
	}
	if api.ecs.ReadTimeout == nil || *api.ecs.ReadTimeout <= 0 {
		t.Fatalf("ECS ReadTimeout = %v, want a bounded positive timeout", api.ecs.ReadTimeout)
	}
	if api.vpc.ConnectTimeout == nil || *api.vpc.ConnectTimeout != *api.ecs.ConnectTimeout ||
		api.sts.ConnectTimeout == nil || *api.sts.ConnectTimeout != *api.ecs.ConnectTimeout {
		t.Fatalf("SDK connect timeouts are inconsistent: ECS=%v VPC=%v STS=%v", api.ecs.ConnectTimeout, api.vpc.ConnectTimeout, api.sts.ConnectTimeout)
	}
	if api.vpc.ReadTimeout == nil || *api.vpc.ReadTimeout != *api.ecs.ReadTimeout ||
		api.sts.ReadTimeout == nil || *api.sts.ReadTimeout != *api.ecs.ReadTimeout {
		t.Fatalf("SDK read timeouts are inconsistent: ECS=%v VPC=%v STS=%v", api.ecs.ReadTimeout, api.vpc.ReadTimeout, api.sts.ReadTimeout)
	}
}

func TestCloudInitConfiguresOnlyProviderAssignedSecondaryAddresses(t *testing.T) {
	content := cloudInit("ssh-ed25519 AAAATEST run", []string{"10.42.0.21", "10.42.0.22"}, 24)
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(content), &document); err != nil {
		t.Fatalf("cloud-init YAML error = %v:\n%s", err, content)
	}
	for _, expected := range []string{"ssh_pwauth: false", "ip address replace 10.42.0.21/24", "ip address replace 10.42.0.22/24"} {
		if !strings.Contains(content, expected) {
			t.Fatalf("cloud-init missing %q:\n%s", expected, content)
		}
	}
	if strings.Contains(content, "10.42.0.20/24") {
		t.Fatalf("cloud-init must not replace the primary address:\n%s", content)
	}
}

func TestClientTokenKeepsLongRunSuffixesDistinct(t *testing.T) {
	runID := strings.Repeat("shared-prefix-", 20)
	first := clientToken(runID, "node-1")
	second := clientToken(runID, "node-2")
	if first == second || len(first) != 64 || len(second) != 64 {
		t.Fatalf("client tokens = %q / %q, want distinct 64-character digests", first, second)
	}
}

func TestCollectPagesRequiresCompleteProviderInventory(t *testing.T) {
	pages := map[int32][]int{1: {1, 2}, 2: {3}}
	values, err := collectPages(context.Background(), func(page int32) ([]int, int, error) {
		return pages[page], 3, nil
	})
	if err != nil || len(values) != 3 {
		t.Fatalf("collectPages() = %v, %v, want three complete values", values, err)
	}
	_, err = collectPages(context.Background(), func(page int32) ([]int, int, error) {
		if page == 1 {
			return []int{1}, 2, nil
		}
		return nil, 2, nil
	})
	if !errors.Is(err, ErrAmbiguousInventory) {
		t.Fatalf("incomplete inventory error = %v, want ErrAmbiguousInventory", err)
	}
}

func TestCloseOwnedIngressFindsSecondTokenPageAndVerifiesRemoval(t *testing.T) {
	deadline := time.Date(2026, 7, 14, 9, 0, 0, 0, time.UTC)
	revoked := false
	listCalls := 0
	list := func(ctx context.Context) ([]securityGroupPermission, error) {
		listCalls++
		return collectTokenPages(ctx, func(token string) ([]securityGroupPermission, string, error) {
			switch token {
			case "":
				return []securityGroupPermission{{SecurityGroupRuleID: "unrelated", Description: "other", PortRange: "443/443"}}, "page-2", nil
			case "page-2":
				if revoked {
					return nil, "", nil
				}
				return []securityGroupPermission{{
					SecurityGroupRuleID: "owned-second-page",
					Description:         ingressDescription("run-1", 19092, deadline),
					PortRange:           "19092/19092",
					SourceCidrIP:        "198.51.100.8/32",
				}}, "", nil
			default:
				return nil, "", ErrAmbiguousInventory
			}
		})
	}
	revoke := func(_ context.Context, permission securityGroupPermission) error {
		if permission.SecurityGroupRuleID != "owned-second-page" {
			t.Fatalf("revoked permission = %#v", permission)
		}
		revoked = true
		return nil
	}
	if err := closeOwnedIngress(context.Background(), "run-1", 19092, list, revoke); err != nil {
		t.Fatalf("closeOwnedIngress() error = %v", err)
	}
	if !revoked || listCalls != 2 {
		t.Fatalf("revoked = %v, list calls = %d; want removal plus zero-match verification", revoked, listCalls)
	}
}

func TestAttachableInstanceStatusMatchesAlibabaAttachDiskContract(t *testing.T) {
	for _, status := range []string{"Running", "Stopped"} {
		if !attachableInstanceStatus(status) {
			t.Fatalf("status %q should be attachable", status)
		}
	}
	for _, status := range []string{"", "Pending", "Starting", "Stopping"} {
		if attachableInstanceStatus(status) {
			t.Fatalf("status %q must not be attachable", status)
		}
	}
}

func TestIngressWindowsFromPermissionsPreservesOwnedDeadlinesAndMarksMalformedRules(t *testing.T) {
	deadline := time.Date(2026, 7, 14, 9, 0, 0, 0, time.UTC)
	windows := ingressWindowsFromPermissions("run-1", []securityGroupPermission{
		{Description: ingressDescription("run-1", 19092, deadline), PortRange: "19092/19092", SourceCidrIP: "198.51.100.8/32"},
		{Description: ingressDescription("run-1", cloudsim.CloudViewPort, deadline), PortRange: "19443/19443", SourceCidrIP: "0.0.0.0/0"},
		{Description: ingressDescriptionPrefix("run-1", 22) + "invalid", PortRange: "22/22", SourceCidrIP: "203.0.113.10/24"},
		{Description: ingressDescription("other-run", 19092, deadline), PortRange: "19092/19092", SourceCidrIP: "198.51.100.9/32"},
	})
	if len(windows) != 3 {
		t.Fatalf("windows = %#v, want three run-owned rules", windows)
	}
	if windows[0].Port != 19092 || windows[0].Source != netip.MustParsePrefix("198.51.100.8/32") || !windows[0].Until.Equal(deadline) {
		t.Fatalf("valid analysis window = %#v", windows[0])
	}
	if windows[1].Port != cloudsim.CloudViewPort || windows[1].Source != netip.MustParsePrefix("0.0.0.0/0") || !windows[1].Until.Equal(deadline) {
		t.Fatalf("valid public view window = %#v", windows[1])
	}
	if windows[2].Port != 22 || windows[2].Source.IsValid() || !windows[2].Until.IsZero() {
		t.Fatalf("malformed deployment window = %#v, want zero values for cleanup", windows[2])
	}
}
