package plugin

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
)

func TestClusterConfigHandlerUsesTimeoutContextAndWritesConfig(t *testing.T) {
	uc := &fakeUsecase{clusterConfigResp: &pluginproto.ClusterConfig{
		Nodes: []*pluginproto.Node{{
			Id:            1,
			ClusterAddr:   "127.0.0.1:7001",
			ApiServerAddr: "http://127.0.0.1:5001",
			Online:        true,
		}},
		Slots: []*pluginproto.Slot{{
			Id:       7,
			Leader:   2,
			Term:     3,
			Replicas: []uint64{1, 2, 3},
		}},
	}}
	srv := mustServer(t, uc)
	ctx := newFakeRPCContext(nil)
	ctx.uid = "plugin.cluster"

	srv.handlePath("/cluster/config", ctx)

	if ctx.err != nil {
		t.Fatalf("unexpected WriteErr: %v", ctx.err)
	}
	if uc.clusterConfigCalls != 1 {
		t.Fatalf("ClusterConfig calls = %d, want 1", uc.clusterConfigCalls)
	}
	if uc.clusterConfigCaller != "plugin.cluster" {
		t.Fatalf("ClusterConfig caller = %q", uc.clusterConfigCaller)
	}
	deadline, ok := uc.clusterConfigCtx.Deadline()
	if !ok {
		t.Fatal("ClusterConfig context has no deadline")
	}
	if remaining := time.Until(deadline); remaining <= 0 || remaining > time.Second {
		t.Fatalf("deadline remaining = %v, want within server timeout", remaining)
	}
	var got pluginproto.ClusterConfig
	mustUnmarshal(t, ctx.written, &got)
	if len(got.GetNodes()) != 1 || got.GetNodes()[0].GetId() != 1 || !got.GetNodes()[0].GetOnline() {
		t.Fatalf("nodes = %#v", got.GetNodes())
	}
	if len(got.GetSlots()) != 1 || got.GetSlots()[0].GetId() != 7 || got.GetSlots()[0].GetLeader() != 2 || got.GetSlots()[0].GetTerm() != 3 {
		t.Fatalf("slots = %#v", got.GetSlots())
	}
	if got := got.GetSlots()[0].GetReplicas(); len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Fatalf("slot replicas = %#v", got)
	}
}

func TestClusterChannelsBelongNodeHandlerDecodesRequestUsesTimeoutAndWritesBatch(t *testing.T) {
	uc := &fakeUsecase{clusterBelongNodeResp: &pluginproto.ClusterChannelBelongNodeBatchResp{
		ClusterChannelBelongNodeResps: []*pluginproto.ClusterChannelBelongNodeResp{{
			NodeId:   2,
			Channels: []*pluginproto.Channel{{ChannelId: "g1", ChannelType: 2}},
		}},
	}}
	srv := mustServer(t, uc)
	ctx := newFakeRPCContext(mustMarshal(t, &pluginproto.ClusterChannelBelongNodeReq{
		Channels: []*pluginproto.Channel{{ChannelId: "g1", ChannelType: 2}},
	}))
	ctx.uid = "plugin.cluster"

	srv.handlePath("/cluster/channels/belongNode", ctx)

	if ctx.err != nil {
		t.Fatalf("unexpected WriteErr: %v", ctx.err)
	}
	if uc.clusterBelongNodeCalls != 1 {
		t.Fatalf("ClusterChannelsBelongNode calls = %d, want 1", uc.clusterBelongNodeCalls)
	}
	if uc.clusterBelongNodeCaller != "plugin.cluster" {
		t.Fatalf("ClusterChannelsBelongNode caller = %q", uc.clusterBelongNodeCaller)
	}
	if got := uc.clusterBelongNodeReq.GetChannels(); len(got) != 1 || got[0].GetChannelId() != "g1" || got[0].GetChannelType() != 2 {
		t.Fatalf("ClusterChannelsBelongNode request = %#v", uc.clusterBelongNodeReq)
	}
	deadline, ok := uc.clusterBelongNodeCtx.Deadline()
	if !ok {
		t.Fatal("ClusterChannelsBelongNode context has no deadline")
	}
	if remaining := time.Until(deadline); remaining <= 0 || remaining > time.Second {
		t.Fatalf("deadline remaining = %v, want within server timeout", remaining)
	}
	var got pluginproto.ClusterChannelBelongNodeBatchResp
	mustUnmarshal(t, ctx.written, &got)
	if len(got.GetClusterChannelBelongNodeResps()) != 1 || got.GetClusterChannelBelongNodeResps()[0].GetNodeId() != 2 {
		t.Fatalf("belong-node response = %#v", &got)
	}
}

func TestClusterHostRPCHandlersKeepShorterIncomingDeadline(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		body []byte
		ctx  func(*fakeUsecase) context.Context
	}{
		{name: "cluster config", path: "/cluster/config", body: nil, ctx: func(uc *fakeUsecase) context.Context { return uc.clusterConfigCtx }},
		{name: "belong node", path: "/cluster/channels/belongNode", body: mustMarshal(t, &pluginproto.ClusterChannelBelongNodeReq{}), ctx: func(uc *fakeUsecase) context.Context { return uc.clusterBelongNodeCtx }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			uc := &fakeUsecase{}
			srv := mustServer(t, uc)
			incoming, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			ctx := newFakeRPCContext(tc.body)
			ctx.uid = "plugin.deadline"
			ctx.ctx = incoming

			srv.handlePath(tc.path, ctx)

			if ctx.err != nil {
				t.Fatalf("unexpected WriteErr: %v", ctx.err)
			}
			deadline, ok := tc.ctx(uc).Deadline()
			if !ok {
				t.Fatal("usecase context has no deadline")
			}
			if remaining := time.Until(deadline); remaining <= 0 || remaining > 250*time.Millisecond {
				t.Fatalf("deadline remaining = %v, want incoming shorter deadline", remaining)
			}
		})
	}
}
