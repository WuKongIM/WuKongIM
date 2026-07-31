package delivery

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/contracts/authority"
	channelappendcontract "github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
	"github.com/WuKongIM/WuKongIM/internal/contracts/onlinedelivery"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	presenceusecase "github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

func TestPresenceResolverPreservesExactTargetsAndAlignedResults(t *testing.T) {
	first := authority.Target{
		HashSlot: 1, SlotID: 11, LeaderNodeID: 10, LeaderTerm: 101,
		ConfigEpoch: 1001, RouteRevision: 100, AuthorityEpoch: 1000,
	}
	second := authority.Target{
		HashSlot: 2, SlotID: 22, LeaderNodeID: 20, LeaderTerm: 202,
		ConfigEpoch: 2002, RouteRevision: 200, AuthorityEpoch: 2000,
	}
	fake := &targetedPresenceAuthorityForChannelAppendTest{
		results: []presenceusecase.EndpointLookupResult{{
			Routes: []presenceusecase.Route{{
				UID: "u1", OwnerNodeID: 3, OwnerBootID: 4, OwnerSeq: 5,
				SessionID: 6, DeviceID: "d1", DeviceFlag: 1, DeviceLevel: 2,
			}},
		}},
	}
	resolver := NewPresenceResolver(presenceusecase.New(presenceusecase.Options{Authority: fake}))

	got := resolver.EndpointsByTargets(context.Background(), []onlinedelivery.RecipientTargetBatch{
		{Target: first, Recipients: []channelappendcontract.Recipient{{UID: "u1"}}},
		{Target: second, Recipients: []channelappendcontract.Recipient{{UID: "u2"}}},
	})

	if len(got) != 2 || got[0].Err != nil || !errors.Is(got[1].Err, runtimedelivery.ErrPresenceResultMissing) {
		t.Fatalf("target results = %#v, want first success and aligned missing second result", got)
	}
	wantRoutes := []onlinedelivery.Route{{
		UID: "u1", OwnerNodeID: 3, OwnerBootID: 4, OwnerSeq: 5,
		SessionID: 6, DeviceID: "d1", DeviceFlag: 1, DeviceLevel: 2,
	}}
	if !reflect.DeepEqual(got[0].Routes, wantRoutes) {
		t.Fatalf("first routes = %#v, want %#v", got[0].Routes, wantRoutes)
	}
	wantGroups := []presenceusecase.EndpointLookupGroup{
		{Target: presenceTargetFromOnlineDeliveryTarget(first), UIDs: []string{"u1"}},
		{Target: presenceTargetFromOnlineDeliveryTarget(second), UIDs: []string{"u2"}},
	}
	if !reflect.DeepEqual(fake.groups, wantGroups) {
		t.Fatalf("presence groups = %#v, want exact targets %#v", fake.groups, wantGroups)
	}
	if fake.legacyCalls != 0 {
		t.Fatalf("legacy endpoint calls = %d, want 0", fake.legacyCalls)
	}
}

func TestPresenceResolverReportsUnavailableDependencyPerTarget(t *testing.T) {
	got := NewPresenceResolver(nil).EndpointsByTargets(context.Background(), []onlinedelivery.RecipientTargetBatch{
		{Recipients: []channelappendcontract.Recipient{{UID: "u1"}}},
		{Recipients: []channelappendcontract.Recipient{{UID: "u2"}}},
	})

	if len(got) != 2 {
		t.Fatalf("target results = %d, want 2", len(got))
	}
	for i := range got {
		if !errors.Is(got[i].Err, presenceusecase.ErrAuthorityUnavailable) {
			t.Fatalf("target result %d error = %v, want ErrAuthorityUnavailable", i, got[i].Err)
		}
	}
}
