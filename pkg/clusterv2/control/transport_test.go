package control

import (
	"context"
	"testing"
	"time"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
	cv2state "github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
	cv2sync "github.com/WuKongIM/WuKongIM/pkg/controllerv2/sync"
	"go.etcd.io/raft/v3/raftpb"
)

func TestRaftTransportSendsBatchByDestination(t *testing.T) {
	network := clusternet.NewLocalNetwork()
	stepper := &recordingRaftStepper{}
	network.Register(2, clusternet.RPCControlRaft, NewRaftHandler(stepper))

	transport := NewRaftTransport(network)
	transport.Send([]raftpb.Message{
		{From: 1, To: 2, Type: raftpb.MsgHeartbeat, Term: 4},
		{From: 1, To: 0, Type: raftpb.MsgBeat},
	})

	if len(stepper.messages) != 1 || stepper.messages[0].To != 2 || stepper.messages[0].Term != 4 {
		t.Fatalf("stepped messages = %#v", stepper.messages)
	}
}

func TestRaftTransportUsesOneWaySend(t *testing.T) {
	network := &recordingRaftMessenger{
		sent:  make(chan sentControlMessage, 1),
		calls: make(chan struct{}, 1),
	}
	transport := NewRaftTransport(network)
	transport.Send([]raftpb.Message{{From: 1, To: 2, Type: raftpb.MsgHeartbeat, Term: 4}})

	select {
	case sent := <-network.sent:
		if sent.nodeID != 2 || sent.serviceID != clusternet.RPCControlRaft {
			t.Fatalf("sent target=(%d,%d), want node=2 service=%d", sent.nodeID, sent.serviceID, clusternet.RPCControlRaft)
		}
		messages, err := DecodeRaftBatch(sent.payload)
		if err != nil {
			t.Fatalf("DecodeRaftBatch() error = %v", err)
		}
		if len(messages) != 1 || messages[0].Term != 4 {
			t.Fatalf("messages = %#v, want one term=4 heartbeat", messages)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for one-way send")
	}

	select {
	case <-network.calls:
		t.Fatal("RaftTransport called synchronous Call, want one-way Send")
	default:
	}
}

func TestStateSyncClientCallsRemoteEndpoint(t *testing.T) {
	network := clusternet.NewLocalNetwork()
	server := cv2sync.NewServer(cv2sync.ServerConfig{
		NodeID:    1,
		ClusterID: "cluster-a",
		LeaderID:  func() uint64 { return 1 },
		Ready:     func() bool { return true },
		Snapshot:  func(context.Context) (cv2state.ClusterState, error) { return controllerV2State(), nil },
	})
	network.Register(1, clusternet.RPCControlStateSync, NewStateSyncHandler(server))

	endpoint := NewStateSyncEndpoint(network, 1)
	resp, err := endpoint.GetState(context.Background(), cv2sync.GetStateRequest{ClusterID: "cluster-a"})
	if err != nil {
		t.Fatalf("GetState() error = %v", err)
	}
	if resp.Revision == 0 || len(resp.Payload) == 0 {
		t.Fatalf("GetState() = %#v, want payload", resp)
	}
}

type recordingRaftStepper struct{ messages []raftpb.Message }

func (s *recordingRaftStepper) Step(ctx context.Context, msg raftpb.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.messages = append(s.messages, msg)
	return nil
}

type sentControlMessage struct {
	nodeID    uint64
	serviceID uint8
	payload   []byte
}

type recordingRaftMessenger struct {
	sent  chan sentControlMessage
	calls chan struct{}
}

func (m *recordingRaftMessenger) Send(_ context.Context, nodeID uint64, serviceID uint8, payload []byte) error {
	m.sent <- sentControlMessage{nodeID: nodeID, serviceID: serviceID, payload: append([]byte(nil), payload...)}
	return nil
}

func (m *recordingRaftMessenger) Call(context.Context, uint64, uint8, []byte) ([]byte, error) {
	m.calls <- struct{}{}
	return nil, nil
}
