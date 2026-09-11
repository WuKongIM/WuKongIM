package cluster

import (
	"context"
	"hash/fnv"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	clusterchannels "github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
)

func TestChannelIdempotencyStoreLookupSendMapsCommittedHit(t *testing.T) {
	node := &recordingIdempotencyNode{
		hit: channelstore.IdempotencyHit{
			Message:     channelruntime.Message{MessageID: 42, MessageSeq: 7},
			PayloadHash: idempotencyTestHash([]byte("payload")),
		},
		ok: true,
	}
	store := NewChannelIdempotencyStore(node)

	result, ok, err := store.LookupSend(context.Background(), channelappend.IdempotencyQuery{
		FromUID:     "u1",
		ClientMsgNo: "client-1",
		ChannelID:   "room",
		ChannelType: 2,
		PayloadHash: idempotencyTestHash([]byte("payload")),
	})
	if err != nil {
		t.Fatalf("LookupSend() error = %v", err)
	}
	if !ok {
		t.Fatal("LookupSend() ok = false, want true")
	}
	if result.MessageID != 42 || result.MessageSeq != 7 || result.Reason != channelappend.ReasonSuccess {
		t.Fatalf("LookupSend() result = %#v, want committed success", result)
	}
	if node.id != (channelruntime.ChannelID{ID: "room", Type: 2}) || node.fromUID != "u1" || node.clientMsgNo != "client-1" {
		t.Fatalf("lookup request = id:%#v from:%q client:%q", node.id, node.fromUID, node.clientMsgNo)
	}
}

func TestChannelIdempotencyStoreLookupSendRejectsPayloadHashMismatch(t *testing.T) {
	node := &recordingIdempotencyNode{
		hit: channelstore.IdempotencyHit{
			Message:     channelruntime.Message{MessageID: 42, MessageSeq: 7},
			PayloadHash: idempotencyTestHash([]byte("old")),
		},
		ok: true,
	}
	store := NewChannelIdempotencyStore(node)

	_, ok, err := store.LookupSend(context.Background(), channelappend.IdempotencyQuery{
		FromUID:     "u1",
		ClientMsgNo: "client-1",
		ChannelID:   "room",
		ChannelType: 2,
		PayloadHash: idempotencyTestHash([]byte("new")),
	})
	if err != nil {
		t.Fatalf("LookupSend() error = %v", err)
	}
	if ok {
		t.Fatal("LookupSend() ok = true, want false for payload hash mismatch")
	}
}

func TestChannelIdempotencyStoreLookupSendTreatsReadinessErrorsAsMiss(t *testing.T) {
	store := NewChannelIdempotencyStore(&recordingIdempotencyNode{err: cluster.ErrNotStarted})

	_, ok, err := store.LookupSend(context.Background(), channelappend.IdempotencyQuery{
		FromUID:     "u1",
		ClientMsgNo: "client-1",
		ChannelID:   "room",
		ChannelType: 2,
		PayloadHash: idempotencyTestHash([]byte("payload")),
	})
	if err != nil {
		t.Fatalf("LookupSend() error = %v, want nil readiness miss", err)
	}
	if ok {
		t.Fatal("LookupSend() ok = true, want false readiness miss")
	}
}

type recordingIdempotencyNode struct {
	id          channelruntime.ChannelID
	fromUID     string
	clientMsgNo string
	hit         channelstore.IdempotencyHit
	ok          bool
	err         error
	uncommitted bool
	reads       []clusterchannels.CommittedRead
	readErr     error
	itemErr     error
	committed   *channelruntime.Message
}

func (n *recordingIdempotencyNode) LookupChannelIdempotency(_ context.Context, id channelruntime.ChannelID, fromUID string, clientMsgNo string) (channelstore.IdempotencyHit, bool, error) {
	n.id = id
	n.fromUID = fromUID
	n.clientMsgNo = clientMsgNo
	return n.hit, n.ok, n.err
}

func idempotencyTestHash(payload []byte) uint64 {
	h := fnv.New64a()
	_, _ = h.Write(payload)
	return h.Sum64()
}

// A local exact proposal can survive a failed quorum attempt. Its index alone
// must never let a retry publish SENDACK success above the Leader's visible HW.
func TestChannelIdempotencyStoreRejectsDurableUncommittedHit(t *testing.T) {
	node := &recordingIdempotencyNode{hit: channelstore.IdempotencyHit{Message: channelruntime.Message{MessageID: 42, MessageSeq: 6}, PayloadHash: idempotencyTestHash([]byte("payload"))}, ok: true, uncommitted: true}
	result, ok, err := NewChannelIdempotencyStore(node).LookupSend(context.Background(), channelappend.IdempotencyQuery{FromUID: "u1", ClientMsgNo: "client-4", ChannelID: "room", ChannelType: 2, PayloadHash: idempotencyTestHash([]byte("payload"))})
	if err != nil {
		t.Fatal(err)
	}
	if ok || result.MessageID != 0 || result.MessageSeq != 0 {
		t.Fatalf("uncommitted durable row became success: %+v, found=%v", result, ok)
	}
}

func (n *recordingIdempotencyNode) ReadChannelCommittedBatch(_ context.Context, reads []clusterchannels.CommittedRead) ([]clusterchannels.CommittedReadResult, error) {
	n.reads = reads
	if n.readErr != nil {
		return nil, n.readErr
	}
	result := make([]clusterchannels.CommittedReadResult, len(reads))
	if !n.uncommitted {
		for i := range result {
			msg := n.hit.Message
			msg.FromUID = n.fromUID
			msg.ClientMsgNo = n.clientMsgNo
			if n.committed != nil {
				msg = *n.committed
			}
			result[i].Err = n.itemErr
			result[i].Read.Messages = []channelruntime.Message{msg}
		}
	}
	return result, nil
}

func TestChannelIdempotencyStoreRequiresExactCommittedIdentity(t *testing.T) {
	original := channelruntime.Message{MessageID: 42, MessageSeq: 6, FromUID: "u1", ClientMsgNo: "client-4", Payload: []byte("payload")}
	for _, tc := range []struct {
		name   string
		change func(*channelruntime.Message)
	}{
		{"message_id", func(m *channelruntime.Message) { m.MessageID++ }},
		{"sequence", func(m *channelruntime.Message) { m.MessageSeq++ }},
		{"sender", func(m *channelruntime.Message) { m.FromUID = "u2" }},
		{"client_key", func(m *channelruntime.Message) { m.ClientMsgNo = "another" }},
		{"payload", func(m *channelruntime.Message) { m.Payload = []byte("different") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			committed := original
			tc.change(&committed)
			node := &recordingIdempotencyNode{ok: true, hit: channelstore.IdempotencyHit{Message: original}, committed: &committed}
			result, ok, err := NewChannelIdempotencyStore(node).LookupSend(context.Background(), channelappend.IdempotencyQuery{FromUID: "u1", ClientMsgNo: "client-4", ChannelID: "room", ChannelType: 2})
			if err != nil || ok || result.MessageID != 0 {
				t.Fatalf("mismatched committed identity: %+v, %v, %v", result, ok, err)
			}
			if len(node.reads) != 1 {
				t.Fatalf("point reads = %d", len(node.reads))
			}
			req := node.reads[0].Request
			if req.FromSeq != 6 || req.MinSeq != 6 || req.MaxSeq != 6 || req.Limit != 1 || req.MaxBytes != len(original.Payload) {
				t.Fatalf("unbounded proof request: %+v", req)
			}
		})
	}
}

func TestChannelIdempotencyStoreCommittedProofErrorsCannotSucceed(t *testing.T) {
	for _, perItem := range []bool{false, true} {
		node := &recordingIdempotencyNode{ok: true, hit: channelstore.IdempotencyHit{Message: channelruntime.Message{MessageID: 42, MessageSeq: 6}}}
		if perItem {
			node.itemErr = context.DeadlineExceeded
		} else {
			node.readErr = context.DeadlineExceeded
		}
		result, ok, err := NewChannelIdempotencyStore(node).LookupSend(context.Background(), channelappend.IdempotencyQuery{FromUID: "u1", ClientMsgNo: "client-4", ChannelID: "room", ChannelType: 2})
		if err != context.DeadlineExceeded || ok || result.MessageID != 0 {
			t.Fatalf("failed proof became success: %+v, %v, %v", result, ok, err)
		}
	}
}
