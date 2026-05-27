package control

import (
	"context"
	"sync"
	"testing"
	"time"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
	cv2 "github.com/WuKongIM/WuKongIM/pkg/controllerv2"
	"go.etcd.io/raft/v3/raftpb"
)

func TestRaftTransportSendsBatchByDestination(t *testing.T) {
	network := clusternet.NewLocalNetwork()
	stepper := newRecordingRaftStepper()
	network.Register(2, clusternet.RPCControlRaft, NewRaftHandler(stepper))

	transport := NewRaftTransport(network)
	transport.Send([]raftpb.Message{
		{From: 1, To: 2, Type: raftpb.MsgHeartbeat, Term: 4},
		{From: 1, To: 0, Type: raftpb.MsgBeat},
	})

	select {
	case <-stepper.received:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for raft message")
	}
	messages := stepper.Messages()
	if len(messages) != 1 || messages[0].To != 2 || messages[0].Term != 4 {
		t.Fatalf("stepped messages = %#v", messages)
	}
}

func TestRaftTransportSendReturnsWithoutWaitingForSender(t *testing.T) {
	network := &blockingRaftMessenger{entered: make(chan struct{}), release: make(chan struct{})}
	transport := NewRaftTransport(network)

	returned := make(chan struct{})
	go func() {
		transport.Send([]raftpb.Message{{From: 1, To: 2, Type: raftpb.MsgHeartbeat, Term: 4}})
		close(returned)
	}()

	select {
	case <-returned:
	case <-time.After(50 * time.Millisecond):
		close(network.release)
		<-returned
		t.Fatal("RaftTransport.Send blocked on sender")
	}
	select {
	case <-network.entered:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for async sender")
	}
	close(network.release)
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
	server := cv2.NewStateSyncServer(cv2.StateSyncServerConfig{
		NodeID:    1,
		ClusterID: "cluster-a",
		LeaderID:  func() uint64 { return 1 },
		Ready:     func() bool { return true },
		Snapshot:  func(context.Context) (cv2.ClusterState, error) { return controllerV2State(), nil },
	})
	network.Register(1, clusternet.RPCControlStateSync, NewStateSyncHandler(server))

	endpoint := NewStateSyncEndpoint(network, 1)
	resp, err := endpoint.GetState(context.Background(), cv2.GetStateRequest{ClusterID: "cluster-a"})
	if err != nil {
		t.Fatalf("GetState() error = %v", err)
	}
	if resp.Revision == 0 || len(resp.Payload) == 0 {
		t.Fatalf("GetState() = %#v, want payload", resp)
	}
}

type recordingRaftStepper struct {
	mu       sync.Mutex
	once     sync.Once
	received chan struct{}
	messages []raftpb.Message
}

func newRecordingRaftStepper() *recordingRaftStepper {
	return &recordingRaftStepper{received: make(chan struct{})}
}

func (s *recordingRaftStepper) Step(ctx context.Context, msg raftpb.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	s.messages = append(s.messages, msg)
	s.mu.Unlock()
	s.once.Do(func() { close(s.received) })
	return nil
}

func (s *recordingRaftStepper) Messages() []raftpb.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]raftpb.Message(nil), s.messages...)
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

type blockingRaftMessenger struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (m *blockingRaftMessenger) Send(ctx context.Context, _ uint64, _ uint8, _ []byte) error {
	m.once.Do(func() { close(m.entered) })
	select {
	case <-m.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
