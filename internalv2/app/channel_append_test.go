package app

import (
	"context"
	"reflect"
	"testing"

	clusterinfra "github.com/WuKongIM/WuKongIM/internalv2/infra/cluster"
	"github.com/WuKongIM/WuKongIM/internalv2/runtime/channelappend"
	channelusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/channel"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
)

func TestChannelAppendRecipientResolverUsesBatchRouteNode(t *testing.T) {
	node := &batchRecipientRouteNodeForChannelAppendTest{
		routes: map[string]clusterv2.Route{
			"u1": {HashSlot: 1, SlotID: 11, Leader: 10, Revision: 100, AuthorityEpoch: 1000},
			"u2": {HashSlot: 2, SlotID: 22, Leader: 20, Revision: 200, AuthorityEpoch: 2000},
		},
	}
	resolver := channelAppendRecipientResolver{node: node}

	got, err := resolver.ResolveRecipientAuthorities(context.Background(), []string{"u1", "u2"})
	if err != nil {
		t.Fatalf("ResolveRecipientAuthorities() error = %v", err)
	}

	if node.singleCalls != 0 {
		t.Fatalf("RouteKey calls = %d, want 0 when RouteKeys is available", node.singleCalls)
	}
	if node.batchCalls != 1 {
		t.Fatalf("RouteKeys calls = %d, want 1", node.batchCalls)
	}
	if !reflect.DeepEqual(node.batchKeys, []string{"u1", "u2"}) {
		t.Fatalf("RouteKeys keys = %#v, want u1,u2", node.batchKeys)
	}
	want := map[string]channelappend.RecipientAuthorityTarget{
		"u1": {HashSlot: 1, SlotID: 11, LeaderNodeID: 10, RouteRevision: 100, AuthorityEpoch: 1000},
		"u2": {HashSlot: 2, SlotID: 22, LeaderNodeID: 20, RouteRevision: 200, AuthorityEpoch: 2000},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %#v, want %#v", got, want)
	}
}

func TestChannelAppendSubscriberMutationObserverRefreshesMetadataCache(t *testing.T) {
	cache := clusterinfra.NewChannelAppendMetadataCache()
	app := &App{channelAppendMetadata: cache}
	observer := channelAppendSubscriberMutationObserver{app: app}

	observer.ObserveSubscriberMutation(context.Background(), channelusecase.SubscriberMutationEvent{
		ChannelKey: channelusecase.ChannelKey{
			ChannelID:   "g1",
			ChannelType: 2,
		},
		Large:                     true,
		SubscriberMutationVersion: 7,
	})

	metadata, ok := cache.Lookup(channelappend.ChannelID{ID: "g1", Type: 2})
	if !ok || !metadata.Large || metadata.SubscriberMutationVersion != 7 {
		t.Fatalf("metadata cache = %#v ok=%v, want large version 7", metadata, ok)
	}
}

type batchRecipientRouteNodeForChannelAppendTest struct {
	routes      map[string]clusterv2.Route
	singleCalls int
	batchCalls  int
	batchKeys   []string
}

func (n *batchRecipientRouteNodeForChannelAppendTest) RouteKey(key string) (clusterv2.Route, error) {
	n.singleCalls++
	return n.routes[key], nil
}

func (n *batchRecipientRouteNodeForChannelAppendTest) RouteKeys(keys []string) ([]clusterv2.Route, error) {
	n.batchCalls++
	n.batchKeys = append([]string(nil), keys...)
	routes := make([]clusterv2.Route, len(keys))
	for i, key := range keys {
		routes[i] = n.routes[key]
	}
	return routes, nil
}
