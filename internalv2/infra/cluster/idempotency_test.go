package cluster

import (
	"context"
	"hash/fnv"
	"testing"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/channelappend"
	channelv2 "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
)

func TestChannelIdempotencyStoreLookupSendMapsCommittedHit(t *testing.T) {
	node := &recordingIdempotencyNode{
		hit: channelstore.IdempotencyHit{
			Message:     channelv2.Message{MessageID: 42, MessageSeq: 7},
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
	if node.id != (channelv2.ChannelID{ID: "room", Type: 2}) || node.fromUID != "u1" || node.clientMsgNo != "client-1" {
		t.Fatalf("lookup request = id:%#v from:%q client:%q", node.id, node.fromUID, node.clientMsgNo)
	}
}

func TestChannelIdempotencyStoreLookupSendRejectsPayloadHashMismatch(t *testing.T) {
	node := &recordingIdempotencyNode{
		hit: channelstore.IdempotencyHit{
			Message:     channelv2.Message{MessageID: 42, MessageSeq: 7},
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
	id          channelv2.ChannelID
	fromUID     string
	clientMsgNo string
	hit         channelstore.IdempotencyHit
	ok          bool
	err         error
}

func (n *recordingIdempotencyNode) LookupChannelIdempotency(_ context.Context, id channelv2.ChannelID, fromUID string, clientMsgNo string) (channelstore.IdempotencyHit, bool, error) {
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
