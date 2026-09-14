//go:build integration

package app

import (
	"context"
	"testing"
	"time"

	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestSingleNodeClusterSendToSendack(t *testing.T) {
	cfg := singleNodeClusterAppConfig(t)
	channelID := channelruntime.ChannelID{ID: "room-sendack", Type: frame.ChannelTypeGroup}
	node := newSendackSmokeSingleNodeCluster(t, cfg.Cluster, channelID)
	app, err := New(cfg, WithCluster(node))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		if err := app.Stop(stopCtx); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	})
	startCtx, startCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer startCancel()
	if err := app.Start(startCtx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	waitSingleNodeClusterRouteLeader(t, node, channelID.ID, cfg.NodeID)
	waitSingleNodeClusterNodeSchedulable(t, node, cfg.NodeID)
	seedGroupSendPermission(t, node, channelID, "u1")

	writes := &sendackSmokeSessionWrites{}
	sess := newSendackSmokeSession(writes)
	sess.SetValue(coregateway.SessionValueUID, "u1")
	sess.SetValue(coregateway.SessionValueProtocolVersion, uint8(frame.LatestVersion))
	send := &frame.SendPacket{
		ClientSeq:   77,
		ClientMsgNo: "client-sendack-1",
		ChannelID:   channelID.ID,
		ChannelType: channelID.Type,
		Payload:     []byte("hello from internal"),
	}

	if err := app.Handler().OnFrame(coregateway.Context{
		Session:        sess,
		RequestContext: context.Background(),
	}, send); err != nil {
		t.Fatalf("OnFrame() error = %v", err)
	}

	ack := writes.requireOnlySendack(t)
	if ack.ReasonCode != frame.ReasonSuccess {
		t.Fatalf("sendack reason = %v, want %v", ack.ReasonCode, frame.ReasonSuccess)
	}
	if ack.ClientSeq != send.ClientSeq || ack.ClientMsgNo != send.ClientMsgNo {
		t.Fatalf("sendack client mapping = seq:%d msgNo:%q, want seq:%d msgNo:%q", ack.ClientSeq, ack.ClientMsgNo, send.ClientSeq, send.ClientMsgNo)
	}
	requireSnowflakeMessageIDNode(t, ack.MessageID, cfg.NodeID)
	if ack.MessageSeq != 1 {
		t.Fatalf("sendack message sequence = %d, want first committed channel sequence 1", ack.MessageSeq)
	}
}

func TestSingleNodeClusterSendWithChannelMetaAndSendack(t *testing.T) {
	cfg := singleNodeClusterAppConfig(t)
	channelID := channelruntime.ChannelID{ID: "room-default-meta", Type: frame.ChannelTypeGroup}
	app, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		if err := app.Stop(stopCtx); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	})
	startCtx, startCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer startCancel()
	if err := app.Start(startCtx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	node, ok := app.cluster.(*cluster.Node)
	if !ok {
		t.Fatalf("cluster runtime = %T, want *cluster.Node", app.cluster)
	}
	waitSingleNodeClusterRouteLeader(t, node, channelID.ID, cfg.NodeID)
	waitSingleNodeClusterNodeSchedulable(t, node, cfg.NodeID)
	seedGroupSendPermission(t, node, channelID, "u1")

	first := sendDefaultMetaSmokePacket(t, app, channelID, 1, "client-default-meta-1")
	if first.ReasonCode != frame.ReasonSuccess {
		t.Fatalf("first sendack reason = %v, want %v", first.ReasonCode, frame.ReasonSuccess)
	}
	requireSnowflakeMessageIDNode(t, first.MessageID, cfg.NodeID)
	if first.MessageSeq != 1 {
		t.Fatalf("first message seq = %d, want first durable channel sequence 1", first.MessageSeq)
	}

	second := sendDefaultMetaSmokePacket(t, app, channelID, 2, "client-default-meta-2")
	if second.ReasonCode != frame.ReasonSuccess {
		t.Fatalf("second sendack reason = %v, want %v", second.ReasonCode, frame.ReasonSuccess)
	}
	requireSnowflakeMessageIDNode(t, second.MessageID, cfg.NodeID)
	if second.MessageID <= first.MessageID {
		t.Fatalf("second message id = %d, want greater than first %d", second.MessageID, first.MessageID)
	}
	if second.MessageSeq != 2 {
		t.Fatalf("second message seq = %d, want 2", second.MessageSeq)
	}
}
