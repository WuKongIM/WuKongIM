package app

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/channels"
	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestSingleNodeClusterSendToSendack(t *testing.T) {
	cfg := singleNodeClusterAppConfig(t)
	channelID := channelv2.ChannelID{ID: "room-sendack", Type: 1}
	node := newSendackSmokeSingleNodeCluster(t, cfg.Cluster, channelID)
	app, err := New(cfg, WithCluster(node))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	startCtx, startCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer startCancel()
	if err := app.Start(startCtx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		if err := app.Stop(stopCtx); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	})

	waitSingleNodeClusterRouteLeader(t, node, channelID.ID, cfg.NodeID)

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
	if ack.MessageID != int64(uint64(cfg.NodeID<<48)+1) {
		t.Fatalf("sendack message id = %d, want first node-scoped id %d", ack.MessageID, uint64(cfg.NodeID<<48)+1)
	}
	if ack.MessageSeq != 1 {
		t.Fatalf("sendack message sequence = %d, want first committed channel sequence 1", ack.MessageSeq)
	}
}

func singleNodeClusterAppConfig(t *testing.T) Config {
	t.Helper()
	nodeID := uint64(1)
	listenAddr := freeSendackSmokeTCPAddr(t)
	cfg := Config{
		NodeID:  nodeID,
		DataDir: t.TempDir(),
		Cluster: clusterv2.Config{
			NodeID:     nodeID,
			ListenAddr: listenAddr,
			DataDir:    t.TempDir(),
			Control: clusterv2.ControlConfig{
				ClusterID:      "internalv2-sendack-smoke",
				Voters:         []clusterv2.ControlVoter{{NodeID: nodeID, Addr: listenAddr}},
				AllowBootstrap: true,
			},
			Slots: clusterv2.SlotConfig{
				InitialSlotCount: 1,
				HashSlotCount:    4,
				ReplicaCount:     1,
			},
			Channel: clusterv2.ChannelConfig{TickInterval: time.Millisecond},
		},
		Gateway: GatewayConfig{SendTimeout: time.Second},
	}
	return cfg
}

func newSendackSmokeSingleNodeCluster(t *testing.T, cfg clusterv2.Config, channelID channelv2.ChannelID) *clusterv2.Node {
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
	node, err := clusterv2.New(cfg, clusterv2.WithChannels(channelSvc))
	if err != nil {
		t.Fatalf("clusterv2.New() error = %v", err)
	}
	return node
}

func waitSingleNodeClusterRouteLeader(t *testing.T, node *clusterv2.Node, key string, want uint64) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var lastRoute clusterv2.Route
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
