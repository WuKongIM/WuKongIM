package alibaba

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudsim"
	openapiutil "github.com/alibabacloud-go/darabonba-openapi/v2/utils"
	"github.com/alibabacloud-go/tea/dara"
	legacytea "github.com/alibabacloud-go/tea/tea"
)

func TestPageCollectorsFailClosedOnCancellationAndProviderDrift(t *testing.T) {
	t.Parallel()

	t.Run("numbered pages honor cancellation before fetch", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := collectPages(ctx, func(int32) ([]int, int, error) {
			t.Fatal("fetch called after cancellation")
			return nil, 0, nil
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("collectPages() error = %v, want context.Canceled", err)
		}
	})

	t.Run("numbered pages preserve provider error", func(t *testing.T) {
		providerErr := errors.New("inventory unavailable")
		_, err := collectPages(context.Background(), func(int32) ([]int, int, error) {
			return nil, 0, providerErr
		})
		if !errors.Is(err, providerErr) {
			t.Fatalf("collectPages() error = %v, want provider error", err)
		}
	})

	t.Run("numbered pages reject impossible total", func(t *testing.T) {
		_, err := collectPages(context.Background(), func(int32) ([]int, int, error) {
			return []int{1, 2}, 1, nil
		})
		if !errors.Is(err, ErrAmbiguousInventory) {
			t.Fatalf("collectPages() error = %v, want ErrAmbiguousInventory", err)
		}
	})

	t.Run("token pages honor cancellation before fetch", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := collectTokenPages(ctx, func(string) ([]int, string, error) {
			t.Fatal("fetch called after cancellation")
			return nil, "", nil
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("collectTokenPages() error = %v, want context.Canceled", err)
		}
	})

	t.Run("token pages preserve provider error", func(t *testing.T) {
		providerErr := errors.New("inventory unavailable")
		_, err := collectTokenPages(context.Background(), func(string) ([]int, string, error) {
			return nil, "", providerErr
		})
		if !errors.Is(err, providerErr) {
			t.Fatalf("collectTokenPages() error = %v, want provider error", err)
		}
	})

	t.Run("token pages reject a stationary cursor", func(t *testing.T) {
		_, err := collectTokenPages(context.Background(), func(token string) ([]int, string, error) {
			if token == "" {
				return []int{1}, "page-2", nil
			}
			return []int{2}, "page-2", nil
		})
		if !errors.Is(err, ErrAmbiguousInventory) {
			t.Fatalf("collectTokenPages() error = %v, want ErrAmbiguousInventory", err)
		}
	})

	t.Run("token pages reject a cursor cycle", func(t *testing.T) {
		_, err := collectTokenPages(context.Background(), func(token string) ([]int, string, error) {
			switch token {
			case "":
				return []int{1}, "page-2", nil
			case "page-2":
				return []int{2}, "page-3", nil
			default:
				return []int{3}, "page-2", nil
			}
		})
		if !errors.Is(err, ErrAmbiguousInventory) {
			t.Fatalf("collectTokenPages() error = %v, want ErrAmbiguousInventory", err)
		}
	})
}

func TestLinuxImageDiscoveryRequiresABoundedCompleteCandidateSet(t *testing.T) {
	t.Parallel()

	if _, err := discoverLatestLinuxImage(context.Background(), nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("discoverLatestLinuxImage(nil) error = %v, want ErrInvalidConfig", err)
	}

	providerErr := errors.New("DescribeImages unavailable")
	if _, err := discoverLatestLinuxImage(context.Background(), func(context.Context, int32, int32) ([]linuxImageCandidate, int32, error) {
		return nil, 0, providerErr
	}); !errors.Is(err, providerErr) {
		t.Fatalf("discoverLatestLinuxImage(provider error) = %v", err)
	}

	if _, err := discoverLatestLinuxImage(context.Background(), func(context.Context, int32, int32) ([]linuxImageCandidate, int32, error) {
		return []linuxImageCandidate{{ID: "image-without-cloud-init"}}, 1, nil
	}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("discoverLatestLinuxImage(no candidate) error = %v, want ErrInvalidConfig", err)
	}

	pageCalls := 0
	_, err := discoverLatestLinuxImage(context.Background(), func(context.Context, int32, int32) ([]linuxImageCandidate, int32, error) {
		pageCalls++
		return make([]linuxImageCandidate, discoveryPageSize), int32(maxDiscoveryPages+1) * discoveryPageSize, nil
	})
	if !errors.Is(err, ErrInvalidConfig) || pageCalls != maxDiscoveryPages {
		t.Fatalf("discoverLatestLinuxImage(page budget) = calls %d, error %v", pageCalls, err)
	}
}

func TestOpenAPIRejectsCanceledOperationsBeforeCallingTheSDK(t *testing.T) {
	t.Parallel()

	api := newNoNetworkOpenAPI(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	canceledCalls := []struct {
		name string
		call func() error
	}{
		{name: "offers", call: func() error {
			_, err := api.Offers(ctx, OfferRequest{InstanceTypes: []string{"ecs.c8i.large"}})
			return err
		}},
		{name: "list assets", call: func() error {
			_, err := api.ListAssets(ctx, ListAssetsRequest{Region: "cn-hangzhou"})
			return err
		}},
		{name: "create network", call: func() error {
			_, err := api.CreateNetwork(ctx, NetworkRequest{})
			return err
		}},
		{name: "create public address", call: func() error {
			_, err := api.CreatePublicAddress(ctx, PublicAddressRequest{})
			return err
		}},
		{name: "associate public address", call: func() error {
			return api.AssociatePublicAddress(ctx, "eip-1", "i-sim")
		}},
		{name: "set ingress", call: func() error {
			return api.SetIngress(ctx, IngressRequest{})
		}},
		{name: "list ingress", call: func() error {
			_, err := api.ListIngress(ctx, IngressListRequest{RunID: "run-1", SecurityGroupID: "sg-1"})
			return err
		}},
		{name: "update run state", call: func() error {
			return api.UpdateRunState(ctx, StateUpdateRequest{})
		}},
		{name: "delete asset", call: func() error {
			return api.DeleteAsset(ctx, Asset{ID: "i-1", Kind: "compute"})
		}},
		{name: "SDK retry wait", call: func() error {
			return waitContext(ctx, time.Hour)
		}},
		{name: "provider cleanup wait", call: func() error {
			return waitForCleanup(ctx, time.Hour)
		}},
	}
	for _, test := range canceledCalls {
		t.Run(test.name, func(t *testing.T) {
			if err := test.call(); !errors.Is(err, context.Canceled) {
				t.Fatalf("operation error = %v, want context.Canceled", err)
			}
		})
	}
}

func TestOpenAPIRejectsInvalidRequestsBeforeCallingTheSDK(t *testing.T) {
	t.Parallel()

	api := newNoNetworkOpenAPI(t)
	var nilAPI *OpenAPI
	if _, err := nilAPI.Offers(context.Background(), OfferRequest{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("nil Offers() error = %v", err)
	}
	if _, err := api.EligibleSpotZones(context.Background(), ""); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("EligibleSpotZones() error = %v", err)
	}
	if _, err := api.LatestLinuxImage(context.Background(), ""); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("LatestLinuxImage() error = %v", err)
	}
	if _, err := api.InstanceTypes(context.Background(), "cn-hangzhou", 0, 4); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("InstanceTypes() error = %v", err)
	}
	if _, err := api.AvailableInstanceTypes(context.Background(), "", "cn-hangzhou-a"); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("AvailableInstanceTypes() error = %v", err)
	}
	if _, err := api.ListIngress(context.Background(), IngressListRequest{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("ListIngress() error = %v", err)
	}
	if err := api.UpdateRunState(context.Background(), StateUpdateRequest{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("UpdateRunState(empty) error = %v", err)
	}
	if err := api.UpdateRunState(context.Background(), StateUpdateRequest{
		Region: "cn-hangzhou", Assets: []Asset{{ID: "i-1", Kind: "compute"}}, State: cloudsim.StateRunning,
	}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("UpdateRunState(running without deadline) error = %v", err)
	}
	if err := api.UpdateRunState(context.Background(), StateUpdateRequest{
		Region: "cn-hangzhou", Assets: []Asset{{Kind: "compute"}}, State: cloudsim.StateReady,
	}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("UpdateRunState(empty asset ID) error = %v", err)
	}
	if err := api.UpdateRunState(context.Background(), StateUpdateRequest{
		Region: "cn-hangzhou", Assets: []Asset{{ID: "unknown-1", Kind: "unknown"}}, State: cloudsim.StateReady,
	}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("UpdateRunState(unknown asset) error = %v", err)
	}
	if _, err := newOpenAPI(nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("newOpenAPI(nil) error = %v", err)
	}
	if _, err := NewOpenAPIFromDefaultCredential(""); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("NewOpenAPIFromDefaultCredential() error = %v", err)
	}
}

func TestIngressCloseAggregatesAmbiguityAndVerificationErrors(t *testing.T) {
	t.Parallel()

	deadline := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	owned := securityGroupPermission{
		SecurityGroupRuleID: "sgr-1",
		Description:         ingressDescription("run-1", 19092, deadline),
		PortRange:           "19092/19092",
		SourceCidrIP:        "198.51.100.8/32",
	}

	t.Run("initial list failure", func(t *testing.T) {
		listErr := errors.New("list failed")
		err := closeOwnedIngress(context.Background(), "run-1", 19092,
			func(context.Context) ([]securityGroupPermission, error) { return nil, listErr },
			func(context.Context, securityGroupPermission) error { return nil },
		)
		if !errors.Is(err, listErr) {
			t.Fatalf("closeOwnedIngress() error = %v", err)
		}
	})

	t.Run("no owned rule is idempotent", func(t *testing.T) {
		revoked := false
		err := closeOwnedIngress(context.Background(), "run-1", 19092,
			func(context.Context) ([]securityGroupPermission, error) {
				return []securityGroupPermission{{SecurityGroupRuleID: "other", Description: "other", PortRange: "443/443"}}, nil
			},
			func(context.Context, securityGroupPermission) error { revoked = true; return nil },
		)
		if err != nil || revoked {
			t.Fatalf("closeOwnedIngress() = %v, revoked=%t", err, revoked)
		}
	})

	t.Run("missing rule identity", func(t *testing.T) {
		calls := 0
		missingID := owned
		missingID.SecurityGroupRuleID = ""
		err := closeOwnedIngress(context.Background(), "run-1", 19092,
			func(context.Context) ([]securityGroupPermission, error) {
				calls++
				if calls == 1 {
					return []securityGroupPermission{missingID}, nil
				}
				return nil, nil
			},
			func(context.Context, securityGroupPermission) error { return nil },
		)
		if !errors.Is(err, ErrAmbiguousInventory) {
			t.Fatalf("closeOwnedIngress() error = %v", err)
		}
	})

	t.Run("revoke and verification errors are retained", func(t *testing.T) {
		revokeErr := errors.New("revoke failed")
		verifyErr := errors.New("verification failed")
		calls := 0
		err := closeOwnedIngress(context.Background(), "run-1", 19092,
			func(context.Context) ([]securityGroupPermission, error) {
				calls++
				if calls == 1 {
					return []securityGroupPermission{owned}, nil
				}
				return nil, verifyErr
			},
			func(context.Context, securityGroupPermission) error { return revokeErr },
		)
		if !errors.Is(err, revokeErr) || !errors.Is(err, verifyErr) {
			t.Fatalf("closeOwnedIngress() error = %v", err)
		}
	})

	t.Run("residual owned rule", func(t *testing.T) {
		err := closeOwnedIngress(context.Background(), "run-1", 19092,
			func(context.Context) ([]securityGroupPermission, error) {
				return []securityGroupPermission{owned}, nil
			},
			func(context.Context, securityGroupPermission) error { return nil },
		)
		if !errors.Is(err, ErrResidualResources) {
			t.Fatalf("closeOwnedIngress() error = %v, want ErrResidualResources", err)
		}
	})
}

func TestSDKErrorClassificationUsesTypedProviderCodesOnly(t *testing.T) {
	t.Parallel()

	legacyErr := fmt.Errorf("wrapped: %w", &legacytea.SDKError{Code: dara.String("Throttling")})
	daraErr := fmt.Errorf("wrapped: %w", &dara.SDKError{Code: dara.String("EntityNotExist.Role")})
	plainErr := errors.New("EntityNotExist appears only in an untrusted message")

	if got := sdkErrorCode(legacyErr); got != "Throttling" {
		t.Fatalf("sdkErrorCode(legacy) = %q", got)
	}
	if got := sdkErrorCode(daraErr); got != "EntityNotExist.Role" {
		t.Fatalf("sdkErrorCode(dara) = %q", got)
	}
	if got := sdkErrorCode(plainErr); got != "" {
		t.Fatalf("sdkErrorCode(plain) = %q", got)
	}
	if got := bootstrapSDKErrorCode(legacyErr); got != "Throttling" {
		t.Fatalf("bootstrapSDKErrorCode(legacy) = %q", got)
	}
	if got := bootstrapSDKErrorCode(daraErr); got != "EntityNotExist.Role" {
		t.Fatalf("bootstrapSDKErrorCode(dara) = %q", got)
	}
	if got := bootstrapSDKErrorCode(plainErr); got != "" {
		t.Fatalf("bootstrapSDKErrorCode(plain) = %q", got)
	}
	if bootstrapNotFound(plainErr) {
		t.Fatal("bootstrapNotFound() trusted an untyped error message")
	}
	if retryablePrivateIPAssignmentError(&legacytea.SDKError{Code: dara.String("InvalidParameter")}) {
		t.Fatal("non-retryable private IP error was classified as retryable")
	}
	if got := ignoreIncorrectDiskStatus(plainErr); !errors.Is(got, plainErr) {
		t.Fatalf("ignoreIncorrectDiskStatus() = %v", got)
	}
}

func TestSDKResponseProjectionPropagatesEncodingFailures(t *testing.T) {
	t.Parallel()

	var output struct {
		Count int `json:"count"`
	}
	if err := decodeSDKBody(make(chan int), &output); err == nil {
		t.Fatal("decodeSDKBody() accepted an unencodable SDK response")
	}
	if err := decodeSDKBody(map[string]any{"count": "not-an-integer"}, &output); err == nil {
		t.Fatal("decodeSDKBody() accepted an incompatible response projection")
	}
}

func newNoNetworkOpenAPI(t *testing.T) *OpenAPI {
	t.Helper()
	api, err := newOpenAPI(&openapiutil.Config{
		AccessKeyId:     dara.String("test-access-key-id"),
		AccessKeySecret: dara.String("test-access-key-secret"),
		RegionId:        dara.String("cn-hangzhou"),
	})
	if err != nil {
		t.Fatalf("newOpenAPI() error = %v", err)
	}
	return api
}
