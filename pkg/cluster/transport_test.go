package cluster

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"go.etcd.io/raft/v3/raftpb"
)

func TestRaftTransport_Send(t *testing.T) {
	// Start a server that captures raft messages
	srv := transport.NewServer()
	var receivedBody []byte
	done := make(chan struct{})
	srv.Handle(msgTypeRaft, func(body []byte) {
		receivedBody = body
		close(done)
	})
	if err := srv.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	d := NewStaticDiscovery([]NodeConfig{{NodeID: 2, Addr: srv.Listener().Addr().String()}})
	pool := transport.NewPool(d, 2, 5*time.Second)
	defer pool.Close()
	client := transport.NewClient(pool)
	defer client.Stop()

	rt := &raftTransport{client: client}

	msg := raftpb.Message{To: 2, From: 1, Type: raftpb.MsgHeartbeat}
	err := rt.Send(context.Background(), []multiraft.Envelope{
		{SlotID: 1, Message: msg},
	})
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}

	// Verify body is a valid raft body
	slotID, data, err := decodeRaftBody(receivedBody)
	if err != nil {
		t.Fatal(err)
	}
	if slotID != 1 {
		t.Fatalf("expected slotID=1, got %d", slotID)
	}
	var decoded raftpb.Message
	if err := decoded.Unmarshal(data); err != nil {
		t.Fatal(err)
	}
	if decoded.Type != raftpb.MsgHeartbeat {
		t.Fatalf("expected MsgHeartbeat, got %v", decoded.Type)
	}
}

func TestRaftTransportSendBatchesMessagesByTargetNode(t *testing.T) {
	srv := transport.NewServer()
	singleFrames := make(chan []byte, 2)
	batchFrames := make(chan []byte, 1)
	srv.Handle(msgTypeRaft, func(body []byte) {
		singleFrames <- append([]byte(nil), body...)
	})
	srv.Handle(msgTypeRaftBatch, func(body []byte) {
		batchFrames <- append([]byte(nil), body...)
	})
	if err := srv.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	d := NewStaticDiscovery([]NodeConfig{{NodeID: 2, Addr: srv.Listener().Addr().String()}})
	pool := transport.NewPool(d, 2, 5*time.Second)
	defer pool.Close()
	client := transport.NewClient(pool)
	defer client.Stop()

	rt := &raftTransport{client: client}
	messages := []multiraft.Envelope{
		{SlotID: 1, Message: raftpb.Message{To: 2, From: 1, Type: raftpb.MsgHeartbeat, Term: 3}},
		{SlotID: 2, Message: raftpb.Message{To: 2, From: 1, Type: raftpb.MsgApp, Term: 4}},
	}
	if err := rt.Send(context.Background(), messages); err != nil {
		t.Fatal(err)
	}

	select {
	case body := <-batchFrames:
		items, err := decodeRaftBatchBody(body)
		if err != nil {
			t.Fatalf("decode raft batch: %v", err)
		}
		if len(items) != len(messages) {
			t.Fatalf("batch item count = %d, want %d", len(items), len(messages))
		}
		for i, item := range items {
			if item.slotID != uint64(messages[i].SlotID) {
				t.Fatalf("item[%d].slotID = %d, want %d", i, item.slotID, messages[i].SlotID)
			}
			var decoded raftpb.Message
			if err := decoded.Unmarshal(item.data); err != nil {
				t.Fatalf("unmarshal item[%d]: %v", i, err)
			}
			if decoded.To != messages[i].Message.To || decoded.From != messages[i].Message.From || decoded.Type != messages[i].Message.Type {
				t.Fatalf("item[%d] message = %+v, want %+v", i, decoded, messages[i].Message)
			}
		}
	case <-singleFrames:
		t.Fatal("sent single raft frame for same-target batch")
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for raft batch frame")
	}
}

func TestRaftTransport_CtxCancel(t *testing.T) {
	d := NewStaticDiscovery([]NodeConfig{})
	pool := transport.NewPool(d, 2, 5*time.Second)
	defer pool.Close()
	client := transport.NewClient(pool)
	defer client.Stop()

	rt := &raftTransport{client: client}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := rt.Send(ctx, []multiraft.Envelope{
		{SlotID: 1, Message: raftpb.Message{To: 2, From: 1}},
	})
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}

func TestRaftTransportLogsSkippedSendFailure(t *testing.T) {
	d := NewStaticDiscovery([]NodeConfig{})
	pool := transport.NewPool(d, 2, 5*time.Second)
	defer pool.Close()
	client := transport.NewClient(pool)
	defer client.Stop()

	logger := newRecordingLogger("cluster")
	rt := &raftTransport{client: client, logger: logger.Named("transport")}

	err := rt.Send(context.Background(), []multiraft.Envelope{
		{SlotID: 1, Message: raftpb.Message{To: 9, From: 1}},
	})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	entry := requireRecordedLogEntry(t, logger, "WARN", "cluster.transport", "cluster.transport.raft_send.skipped")
	if got := entry.msg; got != "skip raft transport send after client error" {
		t.Fatalf("msg = %q", got)
	}
	if got := requireRecordedField[uint64](t, entry, "nodeID"); got != 1 {
		t.Fatalf("nodeID = %d", got)
	}
	if got := requireRecordedField[uint64](t, entry, "targetNodeID"); got != 9 {
		t.Fatalf("targetNodeID = %d", got)
	}
	if got := requireRecordedField[uint64](t, entry, "slotID"); got != 1 {
		t.Fatalf("slotID = %d", got)
	}
	if got := requireRecordedField[error](t, entry, "error"); got != transport.ErrNodeNotFound {
		t.Fatalf("error = %v", got)
	}
	if _, ok := entry.field("event"); !ok {
		t.Fatal("event field missing")
	}
	if _, ok := entry.field("module"); ok {
		t.Fatal("module should come from logger name, not field")
	}
}
