package alibaba

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudlease"
)

func TestLifecycleListReconstructsCompleteLeasesInStableLeaseOrder(t *testing.T) {
	now := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	readAPI := completeReadAPI()
	lifecycleAPI := newLifecycleAPIStub()
	provider := NewLifecycle(readAPI, lifecycleAPI, Options{Now: func() time.Time { return now }})
	controller := cloudlease.NewController(provider, func() time.Time { return now })
	plan := approvedLifecyclePlan(now)
	quote, err := controller.Quote(context.Background(), plan)
	if err != nil {
		t.Fatalf("Quote() error = %v", err)
	}
	if _, err := controller.AcquireWithBootstrap(context.Background(), plan, quote, lifecycleBootstrap(t)); err != nil {
		t.Fatalf("AcquireWithBootstrap() error = %v", err)
	}

	secondLeaseAssets := cloneLifecycleAssets(lifecycleAPI.assets)
	for index := range secondLeaseAssets {
		secondLeaseAssets[index].Tags[cloudlease.TagLeaseID] = "lease-before"
		secondLeaseAssets[index].Tags[cloudlease.TagRequestID] = "request-before"
	}
	lifecycleAPI.assets = append(lifecycleAPI.assets, secondLeaseAssets...)
	receipts, err := provider.List(context.Background(), cloudlease.InventoryFilter{Repository: plan.Repository})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(receipts) != 2 || receipts[0].LeaseID != "lease-before" || receipts[1].LeaseID != plan.LeaseID {
		t.Fatalf("List() lease order = %#v", receipts)
	}
	for _, receipt := range receipts {
		if receipt.State != cloudlease.StateActive || receipt.Repository != plan.Repository ||
			receipt.AccountIDHash != readAPI.accountHash || len(receipt.Resources) != len(lifecycleAPI.assets)/2 {
			t.Fatalf("reconstructed receipt = %#v", receipt)
		}
	}
	if _, err := provider.List(context.Background(), cloudlease.InventoryFilter{Repository: " " + plan.Repository}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("List(non-canonical repository) error = %v", err)
	}

	lifecycleAPI.assets = append(lifecycleAPI.assets, LifecycleAsset{
		ID: "foreign-instance", Kind: ResourceKindInstance,
		Tags: map[string]string{cloudlease.TagRepository: plan.Repository},
	})
	if _, err := provider.List(context.Background(), cloudlease.InventoryFilter{Repository: plan.Repository}); !errors.Is(err, ErrAmbiguousInventory) {
		t.Fatalf("List(asset without lease identity) error = %v", err)
	}
}

func TestLifecycleListPropagatesInventoryAndAccountAuthorityFailures(t *testing.T) {
	repository := "WuKongIM/WuKongIM"
	if _, err := New(completeReadAPI(), Options{}).List(context.Background(), cloudlease.InventoryFilter{Repository: repository}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("quote-only List() error = %v", err)
	}
	inventoryErr := errors.New("inventory unavailable")
	provider := NewLifecycle(completeReadAPI(), &listErrorLifecycleAPI{
		LifecycleAPI: newLifecycleAPIStub(), err: inventoryErr,
	}, Options{})
	if _, err := provider.List(context.Background(), cloudlease.InventoryFilter{Repository: repository}); !errors.Is(err, inventoryErr) {
		t.Fatalf("List(inventory failure) error = %v", err)
	}
	accountErr := errors.New("account authority unavailable")
	provider = NewLifecycle(&accountErrorReadAPI{ReadAPI: completeReadAPI(), err: accountErr}, newLifecycleAPIStub(), Options{})
	if _, err := provider.List(context.Background(), cloudlease.InventoryFilter{Repository: repository}); !errors.Is(err, accountErr) {
		t.Fatalf("List(account failure) error = %v", err)
	}
}

type listErrorLifecycleAPI struct {
	LifecycleAPI
	err error
}

func (a *listErrorLifecycleAPI) ListAssets(context.Context, InventoryQuery) ([]LifecycleAsset, error) {
	return nil, a.err
}

type accountErrorReadAPI struct {
	ReadAPI
	err error
}

func (a *accountErrorReadAPI) AccountIDHash(context.Context) (string, error) {
	return "", a.err
}
