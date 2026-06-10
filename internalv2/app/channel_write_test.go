package app

import (
	"context"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/internalv2/runtime/channelwrite"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
)

func TestChannelWriteRecipientResolverUsesBatchRouteNode(t *testing.T) {
	node := &batchRecipientRouteNodeForChannelWriteTest{
		routes: map[string]clusterv2.Route{
			"u1": {HashSlot: 1, SlotID: 11, Leader: 10, Revision: 100, AuthorityEpoch: 1000},
			"u2": {HashSlot: 2, SlotID: 22, Leader: 20, Revision: 200, AuthorityEpoch: 2000},
		},
	}
	resolver := channelWriteRecipientResolver{node: node}

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
	want := map[string]channelwrite.RecipientAuthorityTarget{
		"u1": {HashSlot: 1, SlotID: 11, LeaderNodeID: 10, RouteRevision: 100, AuthorityEpoch: 1000},
		"u2": {HashSlot: 2, SlotID: 22, LeaderNodeID: 20, RouteRevision: 200, AuthorityEpoch: 2000},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %#v, want %#v", got, want)
	}
}

type batchRecipientRouteNodeForChannelWriteTest struct {
	routes      map[string]clusterv2.Route
	singleCalls int
	batchCalls  int
	batchKeys   []string
}

func (n *batchRecipientRouteNodeForChannelWriteTest) RouteKey(key string) (clusterv2.Route, error) {
	n.singleCalls++
	return n.routes[key], nil
}

func (n *batchRecipientRouteNodeForChannelWriteTest) RouteKeys(keys []string) ([]clusterv2.Route, error) {
	n.batchCalls++
	n.batchKeys = append([]string(nil), keys...)
	routes := make([]clusterv2.Route, len(keys))
	for i, key := range keys {
		routes[i] = n.routes[key]
	}
	return routes, nil
}
