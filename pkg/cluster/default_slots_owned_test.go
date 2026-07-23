package cluster

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"go.etcd.io/raft/v3/raftpb"
)

func TestNetworkSlotTransportUsesOwnedSenderWhenAvailable(t *testing.T) {
	sender := &recordingOwnedSlotSender{}
	transport := networkSlotTransport{sender: sender}

	err := transport.Send(context.Background(), []multiraft.Envelope{
		{SlotID: 7, Message: raftpb.Message{From: 1, To: 2, Type: raftpb.MsgHeartbeat, Term: 4}},
	})

	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if sender.ownedSendCount != 1 || sender.sendCount != 0 {
		t.Fatalf("send counts owned=%d normal=%d, want owned only", sender.ownedSendCount, sender.sendCount)
	}
	if sender.nodeID != 2 || sender.serviceID != clusternet.MsgSlotRaftBatch {
		t.Fatalf("target=(%d,%d), want node=2 service=%d", sender.nodeID, sender.serviceID, clusternet.MsgSlotRaftBatch)
	}
	envelopes, err := decodeSlotRaftBatch(sender.payload)
	if err != nil {
		t.Fatalf("decodeSlotRaftBatch() error = %v", err)
	}
	if len(envelopes) != 1 || envelopes[0].SlotID != 7 || envelopes[0].Message.Term != 4 {
		t.Fatalf("envelopes = %#v, want one slot=7 term=4", envelopes)
	}
}

func TestNetworkSlotTransportOwnsReadyMessagePayloads(t *testing.T) {
	var transport any = networkSlotTransport{}
	owner, ok := transport.(multiraft.ReadyMessagePayloadOwner)
	if !ok {
		t.Fatal("networkSlotTransport must declare synchronous payload ownership")
	}
	if !owner.OwnsReadyMessagePayloads() {
		t.Fatal("networkSlotTransport should own Ready message payloads")
	}
}

func TestNetworkSlotTransportAttemptsEveryTargetAfterSendErrors(t *testing.T) {
	sendErr := errors.New("peer unavailable")
	sender := &failingSlotSender{err: sendErr}
	transport := networkSlotTransport{sender: sender}

	err := transport.Send(context.Background(), []multiraft.Envelope{
		{SlotID: 7, Message: raftpb.Message{From: 2, To: 1, Type: raftpb.MsgVote, Term: 3}},
		{SlotID: 7, Message: raftpb.Message{From: 2, To: 3, Type: raftpb.MsgVote, Term: 3}},
		{SlotID: 8, Message: raftpb.Message{From: 2, To: 4, Type: raftpb.MsgHeartbeat, Term: 5}},
	})

	if !errors.Is(err, sendErr) {
		t.Fatalf("Send() error = %v, want %v", err, sendErr)
	}
	got := append([]uint64(nil), sender.nodeIDs...)
	slices.Sort(got)
	if want := []uint64{1, 3, 4}; !slices.Equal(got, want) {
		t.Fatalf("attempted node IDs = %v, want %v", got, want)
	}
}

func TestEncodeSlotRaftBatchUsesBinaryFraming(t *testing.T) {
	data := bytes.Repeat([]byte("x"), 1024)
	payload, err := encodeSlotRaftBatch([]multiraft.Envelope{
		{
			SlotID: 7,
			Message: raftpb.Message{
				From:    1,
				To:      2,
				Type:    raftpb.MsgApp,
				Term:    4,
				Entries: []raftpb.Entry{{Index: 1, Term: 4, Data: data}},
			},
		},
	})
	if err != nil {
		t.Fatalf("encodeSlotRaftBatch() error = %v", err)
	}
	if !bytes.HasPrefix(payload, []byte("WKSRB1")) {
		t.Fatalf("encoded payload prefix = %q, want binary Slot Raft batch frame", payload[:min(len(payload), 16)])
	}
	if bytes.Contains(payload, []byte(`"message"`)) {
		t.Fatalf("encoded payload contains JSON field name, want raw binary frame")
	}
	envelopes, err := decodeSlotRaftBatch(payload)
	if err != nil {
		t.Fatalf("decodeSlotRaftBatch() error = %v", err)
	}
	if len(envelopes) != 1 || envelopes[0].SlotID != 7 {
		t.Fatalf("decoded envelopes = %#v, want one slot=7", envelopes)
	}
	if got := envelopes[0].Message.Entries; len(got) != 1 || !bytes.Equal(got[0].Data, data) {
		t.Fatalf("decoded entries = %#v, want original payload", got)
	}
}

type recordingOwnedSlotSender struct {
	nodeID         uint64
	serviceID      uint8
	payload        []byte
	sendCount      int
	ownedSendCount int
}

type failingSlotSender struct {
	err     error
	nodeIDs []uint64
}

func (s *failingSlotSender) Send(_ context.Context, nodeID uint64, _ uint8, _ []byte) error {
	s.nodeIDs = append(s.nodeIDs, nodeID)
	return s.err
}

func (s *recordingOwnedSlotSender) Send(context.Context, uint64, uint8, []byte) error {
	s.sendCount++
	return errors.New("normal send")
}

func (s *recordingOwnedSlotSender) SendOwned(_ context.Context, nodeID uint64, serviceID uint8, payload transport.OwnedBuffer) error {
	s.ownedSendCount++
	s.nodeID = nodeID
	s.serviceID = serviceID
	s.payload = append([]byte(nil), payload.Bytes()...)
	payload.Release()
	return nil
}
