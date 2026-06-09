package app

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/internalv2/runtime/channelwrite"
	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
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

func TestChannelWriteConversationProjectorAdmitsPatchesAndLogsFailure(t *testing.T) {
	logger := &recordingAppLogger{}
	client := &recordingConversationProjectionClient{admitErr: conversationusecase.ErrStaleRoute}
	projector := channelWriteConversationProjector{client: client, logger: logger}

	err := projector.AdmitRecipientPatches(context.Background(), []channelwrite.ConversationPatch{{
		UID:         "u1",
		ChannelID:   "g1",
		ChannelType: 2,
		ActiveAt:    100,
		UpdatedAt:   101,
		MessageSeq:  7,
	}})
	if !errors.Is(err, conversationusecase.ErrStaleRoute) {
		t.Fatalf("AdmitRecipientPatches() error = %v, want ErrStaleRoute", err)
	}
	if client.admitCalls != 1 {
		t.Fatalf("AdmitPatches calls = %d, want 1", client.admitCalls)
	}
	if len(client.patches) != 1 || client.patches[0].UID != "u1" || client.patches[0].MessageSeq != 7 {
		t.Fatalf("admitted patches = %#v, want one converted patch", client.patches)
	}
	entry := requireAppLogEvent(t, logger, "ERROR", "internalv2.app.channelwrite.conversation_projection_failed")
	requireAppLogField(t, entry, "uid", "u1")
	requireAppLogField(t, entry, "patchCount", 1)
	requireAppLogField(t, entry, "channelID", "g1")
	requireAppLogField(t, entry, "channelType", int64(2))
	requireAppLogField(t, entry, "messageSeq", uint64(7))
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

type recordingConversationProjectionClient struct {
	admitCalls int
	admitErr   error
	patches    []conversationusecase.ActivePatch
}

func (c *recordingConversationProjectionClient) AdmitPatches(_ context.Context, patches []conversationusecase.ActivePatch) error {
	c.admitCalls++
	c.patches = append(c.patches, patches...)
	return c.admitErr
}
