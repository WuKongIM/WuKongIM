package app

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	channelv2 "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestSingleNodeClusterSendToSendack(t *testing.T) {
	cfg := singleNodeClusterAppConfig(t)
	channelID := channelv2.ChannelID{ID: "room-sendack", Type: frame.ChannelTypeGroup}
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
		Payload:     []byte("hello from internalv2"),
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
	channelID := channelv2.ChannelID{ID: "room-default-meta", Type: frame.ChannelTypeGroup}
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
		t.Fatalf("first message seq = %d, want 1", first.MessageSeq)
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

func sendDefaultMetaSmokePacket(t *testing.T, app *App, channelID channelv2.ChannelID, clientSeq uint64, clientMsgNo string) *frame.SendackPacket {
	t.Helper()
	writes := &sendackSmokeSessionWrites{}
	sess := newSendackSmokeSession(writes)
	sess.SetValue(coregateway.SessionValueUID, "u1")
	sess.SetValue(coregateway.SessionValueProtocolVersion, uint8(frame.LatestVersion))
	send := &frame.SendPacket{
		ClientSeq:   clientSeq,
		ClientMsgNo: clientMsgNo,
		ChannelID:   channelID.ID,
		ChannelType: channelID.Type,
		Payload:     []byte("hello through default metadata"),
	}
	if err := app.Handler().OnFrame(coregateway.Context{
		Session:        sess,
		RequestContext: context.Background(),
	}, send); err != nil {
		t.Fatalf("OnFrame() error = %v", err)
	}
	ack := writes.requireOnlySendack(t)
	if ack.ClientSeq != send.ClientSeq || ack.ClientMsgNo != send.ClientMsgNo {
		t.Fatalf("sendack client mapping = seq:%d msgNo:%q, want seq:%d msgNo:%q", ack.ClientSeq, ack.ClientMsgNo, send.ClientSeq, send.ClientMsgNo)
	}
	return ack
}

func seedGroupSendPermission(t *testing.T, node *cluster.Node, channelID channelv2.ChannelID, uids ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := node.UpsertChannelMetadata(ctx, metadb.Channel{ChannelID: channelID.ID, ChannelType: int64(channelID.Type)}); err != nil {
		t.Fatalf("UpsertChannelMetadata() error = %v", err)
	}
	if len(uids) > 0 {
		if err := node.AddChannelSubscribers(ctx, channelID.ID, int64(channelID.Type), uids, 1); err != nil {
			t.Fatalf("AddChannelSubscribers() error = %v", err)
		}
	}
	waitUntil(t, 3*time.Second, func() bool {
		if _, err := node.GetChannelMetadata(context.Background(), channelID.ID, int64(channelID.Type)); err != nil {
			return false
		}
		for _, uid := range uids {
			ok, err := node.ContainsChannelSubscriber(context.Background(), channelID.ID, int64(channelID.Type), uid)
			if err != nil || !ok {
				return false
			}
		}
		return true
	})
}

func singleNodeClusterAppConfig(t *testing.T) Config {
	t.Helper()
	nodeID := uint64(1)
	listenAddr := freeSendackSmokeTCPAddr(t)
	cfg := Config{
		NodeID:  nodeID,
		DataDir: t.TempDir(),
		Cluster: cluster.Config{
			NodeID:     nodeID,
			ListenAddr: listenAddr,
			DataDir:    t.TempDir(),
			Control: cluster.ControlConfig{
				ClusterID:      "internalv2-sendack-smoke",
				Voters:         []cluster.ControlVoter{{NodeID: nodeID, Addr: listenAddr}},
				AllowBootstrap: true,
			},
			Slots: cluster.SlotConfig{
				InitialSlotCount: 1,
				HashSlotCount:    4,
				ReplicaCount:     1,
			},
			Channel: cluster.ChannelConfig{TickInterval: time.Millisecond},
			HealthReport: cluster.HealthReportConfig{
				Interval: 20 * time.Millisecond,
				TTL:      500 * time.Millisecond,
			},
		},
		Gateway: GatewayConfig{SendTimeout: time.Second},
	}
	return cfg
}

func newSendackSmokeSingleNodeCluster(t *testing.T, cfg cluster.Config, channelID channelv2.ChannelID) *cluster.Node {
	t.Helper()
	storeFactory := channelstore.NewMessageDBFactory(t.TempDir())
	t.Cleanup(func() {
		if err := storeFactory.Close(); err != nil {
			t.Fatalf("message store close error = %v", err)
		}
	})
	channelSvc, err := channels.NewService(channels.Config{
		LocalNode:    channelv2.NodeID(cfg.NodeID),
		ReactorCount: 1,
		MailboxSize:  16,
		Store:        storeFactory,
		MetaSource: channels.NewStaticMetaSource([]channelv2.Meta{{
			Key:         channelv2.ChannelKeyForID(channelID),
			ID:          channelID,
			Epoch:       1,
			LeaderEpoch: 1,
			Leader:      channelv2.NodeID(cfg.NodeID),
			Replicas:    []channelv2.NodeID{channelv2.NodeID(cfg.NodeID)},
			ISR:         []channelv2.NodeID{channelv2.NodeID(cfg.NodeID)},
			MinISR:      1,
			Status:      channelv2.StatusActive,
		}}),
	})
	if err != nil {
		t.Fatalf("channels.NewService() error = %v", err)
	}
	node, err := cluster.New(cfg, cluster.WithChannels(channelSvc))
	if err != nil {
		t.Fatalf("cluster.New() error = %v", err)
	}
	return node
}

func waitSingleNodeClusterRouteLeader(t *testing.T, node *cluster.Node, key string, want uint64) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var lastRoute cluster.Route
	var lastErr error
	for time.Now().Before(deadline) {
		route, err := node.RouteKey(key)
		if err == nil && route.Leader == want {
			return
		}
		lastRoute = route
		lastErr = err
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("route leader for key %q did not become %d; last route=%#v lastErr=%v", key, want, lastRoute, lastErr)
}

func waitSingleNodeClusterNodeSchedulable(t *testing.T, node *cluster.Node, want uint64) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var lastNode control.Node
	var lastErr error
	for time.Now().Before(deadline) {
		snapshot, err := node.LocalControlSnapshot(context.Background())
		if err == nil {
			for _, item := range snapshot.Nodes {
				if item.NodeID != want {
					continue
				}
				lastNode = item
				if control.NodeSchedulableForPlacement(item) {
					return
				}
			}
		} else {
			lastErr = err
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("node %d did not become schedulable; lastNode=%#v lastErr=%v", want, lastNode, lastErr)
}

func freeSendackSmokeTCPAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func newSendackSmokeSession(writes *sendackSmokeSessionWrites) session.Session {
	return session.New(session.Config{
		ID: 100,
		WriteFrameFn: func(f frame.Frame, _ session.OutboundMeta) error {
			writes.append(f)
			return nil
		},
	})
}

// sendackSmokeSessionWrites records outbound frames written by the test gateway session.
type sendackSmokeSessionWrites struct {
	// mu protects frames because gateway sessions serialize writes but test assertions read separately.
	mu sync.Mutex
	// frames stores the exact outbound frame sequence observed by the test session.
	frames []frame.Frame
}

func (w *sendackSmokeSessionWrites) append(f frame.Frame) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.frames = append(w.frames, f)
}

func (w *sendackSmokeSessionWrites) requireOnlySendack(t *testing.T) *frame.SendackPacket {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.frames) != 1 {
		t.Fatalf("written frame count = %d, want exactly 1", len(w.frames))
	}
	ack, ok := w.frames[0].(*frame.SendackPacket)
	if !ok {
		t.Fatalf("written frame = %T, want *frame.SendackPacket", w.frames[0])
	}
	return ack
}
