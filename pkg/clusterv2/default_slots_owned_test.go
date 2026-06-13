package clusterv2

import (
	"context"
	"errors"
	"testing"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/transportv2"
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

type recordingOwnedSlotSender struct {
	nodeID         uint64
	serviceID      uint8
	payload        []byte
	sendCount      int
	ownedSendCount int
}

func (s *recordingOwnedSlotSender) Send(context.Context, uint64, uint8, []byte) error {
	s.sendCount++
	return errors.New("normal send")
}

func (s *recordingOwnedSlotSender) SendOwned(_ context.Context, nodeID uint64, serviceID uint8, payload transportv2.OwnedBuffer) error {
	s.ownedSendCount++
	s.nodeID = nodeID
	s.serviceID = serviceID
	s.payload = append([]byte(nil), payload.Bytes()...)
	payload.Release()
	return nil
}
