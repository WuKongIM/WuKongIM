package cluster

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/transport"
	raftstorage "github.com/WuKongIM/WuKongIM/pkg/raftlog"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
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

func TestRaftSnapshotChunkCodecRoundTrips(t *testing.T) {
	header := raftpb.Message{
		Type: raftpb.MsgSnap,
		From: 1,
		To:   2,
		Term: 9,
		Snapshot: &raftpb.Snapshot{
			Metadata: raftpb.SnapshotMetadata{
				Index: 42,
				Term:  7,
				ConfState: raftpb.ConfState{
					Voters:   []uint64{1, 2},
					Learners: []uint64{3},
				},
			},
		},
	}
	headerData, err := header.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	chunk := raftSnapshotChunk{
		slotID:  11,
		chunkID: 99,
		from:    1,
		to:      2,
		index:   42,
		term:    7,
		total:   13,
		offset:  4,
		message: headerData,
		data:    []byte("snapshot"),
	}

	body := encodeRaftSnapshotChunkBody(chunk)
	decoded, err := decodeRaftSnapshotChunkBody(body)
	if err != nil {
		t.Fatalf("decodeRaftSnapshotChunkBody() error = %v", err)
	}

	if decoded.slotID != chunk.slotID || decoded.chunkID != chunk.chunkID ||
		decoded.from != chunk.from || decoded.to != chunk.to ||
		decoded.index != chunk.index || decoded.term != chunk.term ||
		decoded.total != chunk.total || decoded.offset != chunk.offset {
		t.Fatalf("decoded metadata = %+v, want %+v", decoded, chunk)
	}
	if !bytes.Equal(decoded.message, chunk.message) {
		t.Fatalf("decoded message bytes = %v, want %v", decoded.message, chunk.message)
	}
	if !bytes.Equal(decoded.data, chunk.data) {
		t.Fatalf("decoded data = %q, want %q", decoded.data, chunk.data)
	}
}

func TestRaftTransportChunksOversizedSnapshot(t *testing.T) {
	srv := transport.NewServer()
	singleFrames := make(chan []byte, 1)
	batchFrames := make(chan []byte, 1)
	chunkFrames := make(chan []byte, 8)
	srv.Handle(msgTypeRaft, func(body []byte) {
		singleFrames <- append([]byte(nil), body...)
	})
	srv.Handle(msgTypeRaftBatch, func(body []byte) {
		batchFrames <- append([]byte(nil), body...)
	})
	srv.Handle(msgTypeRaftSnapshotChunk, func(body []byte) {
		chunkFrames <- append([]byte(nil), body...)
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

	const maxBody = 256
	rt := &raftTransport{client: client, maxBodySize: maxBody}
	snapshotData := bytes.Repeat([]byte("0123456789abcdef"), 64)
	msg := raftpb.Message{
		To:   2,
		From: 1,
		Type: raftpb.MsgSnap,
		Term: 8,
		Snapshot: &raftpb.Snapshot{
			Data: snapshotData,
			Metadata: raftpb.SnapshotMetadata{
				Index: 55,
				Term:  7,
				ConfState: raftpb.ConfState{
					Voters: []uint64{1, 2},
				},
			},
		},
	}

	if err := rt.Send(context.Background(), []multiraft.Envelope{{SlotID: 7, Message: msg}}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	var received [][]byte
	deadline := time.After(2 * time.Second)
	for {
		select {
		case body := <-chunkFrames:
			if len(body) > maxBody {
				t.Fatalf("chunk frame len = %d, want <= %d", len(body), maxBody)
			}
			received = append(received, body)
			chunk, err := decodeRaftSnapshotChunkBody(body)
			if err != nil {
				t.Fatalf("decode chunk: %v", err)
			}
			var gotTotal uint64
			for _, raw := range received {
				decoded, err := decodeRaftSnapshotChunkBody(raw)
				if err != nil {
					t.Fatalf("decode collected chunk: %v", err)
				}
				gotTotal += uint64(len(decoded.data))
			}
			if gotTotal >= chunk.total {
				if len(received) < 2 {
					t.Fatalf("received %d chunk frame, want multiple", len(received))
				}
				select {
				case <-singleFrames:
					t.Fatal("oversized snapshot was sent as a single raft frame")
				default:
				}
				select {
				case <-batchFrames:
					t.Fatal("oversized snapshot was sent inside a raft batch frame")
				default:
				}
				return
			}
		case <-singleFrames:
			t.Fatal("oversized snapshot was sent as a single raft frame")
		case <-batchFrames:
			t.Fatal("oversized snapshot was sent inside a raft batch frame")
		case <-deadline:
			t.Fatalf("timeout waiting for snapshot chunks, received %d", len(received))
		}
	}
}

func TestRaftSnapshotAssemblerReassemblesOutOfOrderChunks(t *testing.T) {
	header := raftpb.Message{
		Type: raftpb.MsgSnap,
		From: 1,
		To:   2,
		Term: 6,
		Snapshot: &raftpb.Snapshot{
			Metadata: raftpb.SnapshotMetadata{
				Index: 21,
				Term:  5,
				ConfState: raftpb.ConfState{
					Voters: []uint64{1, 2},
				},
			},
		},
	}
	headerData, err := header.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	assembler := newRaftSnapshotAssembler(time.Minute, time.Now)
	second := raftSnapshotChunk{
		slotID:  3,
		chunkID: 77,
		from:    1,
		to:      2,
		index:   21,
		term:    5,
		total:   10,
		offset:  5,
		message: headerData,
		data:    []byte("world"),
	}
	first := second
	first.offset = 0
	first.data = []byte("hello")

	if _, ok, err := assembler.add(second); err != nil || ok {
		t.Fatalf("add(second) = ok:%v err:%v, want incomplete nil", ok, err)
	}
	if _, ok, err := assembler.add(second); err != nil || ok {
		t.Fatalf("add(duplicate second) = ok:%v err:%v, want incomplete nil", ok, err)
	}
	assembled, ok, err := assembler.add(first)
	if err != nil {
		t.Fatalf("add(first) error = %v", err)
	}
	if !ok {
		t.Fatal("add(first) ok = false, want complete snapshot")
	}
	if assembled.slotID != first.slotID {
		t.Fatalf("assembled slotID = %d, want %d", assembled.slotID, first.slotID)
	}
	if assembled.message.Type != raftpb.MsgSnap || assembled.message.Snapshot == nil {
		t.Fatalf("assembled message = %+v, want MsgSnap with snapshot", assembled.message)
	}
	if got := string(assembled.message.Snapshot.Data); got != "helloworld" {
		t.Fatalf("snapshot data = %q, want helloworld", got)
	}
	if assembled.message.Snapshot.Metadata.Index != 21 || assembled.message.Snapshot.Metadata.Term != 5 {
		t.Fatalf("snapshot metadata = %+v", assembled.message.Snapshot.Metadata)
	}
}

func TestRaftSnapshotAssemblerExpiresIncompleteChunks(t *testing.T) {
	now := time.Unix(100, 0)
	assembler := newRaftSnapshotAssembler(time.Second, func() time.Time { return now })
	header := raftpb.Message{
		Type:     raftpb.MsgSnap,
		From:     1,
		To:       2,
		Snapshot: &raftpb.Snapshot{Metadata: raftpb.SnapshotMetadata{Index: 1, Term: 1}},
	}
	headerData, err := header.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	first := raftSnapshotChunk{
		slotID:  1,
		chunkID: 2,
		from:    1,
		to:      2,
		index:   1,
		term:    1,
		total:   10,
		offset:  0,
		message: headerData,
		data:    []byte("hello"),
	}
	second := first
	second.offset = 5
	second.data = []byte("world")

	if _, ok, err := assembler.add(first); err != nil || ok {
		t.Fatalf("add(first) = ok:%v err:%v, want incomplete nil", ok, err)
	}
	now = now.Add(2 * time.Second)
	if _, ok, err := assembler.add(second); err != nil || ok {
		t.Fatalf("add(second after ttl) = ok:%v err:%v, want incomplete nil", ok, err)
	}
}

func TestClusterHandleRaftSnapshotChunkMessageStepsReassembledSnapshot(t *testing.T) {
	rt, err := multiraft.New(multiraft.Options{
		NodeID:       2,
		TickInterval: 10 * time.Millisecond,
		Workers:      1,
		Transport:    noopManagedSlotTransport{},
		Raft: multiraft.RaftOptions{
			ElectionTick:  10,
			HeartbeatTick: 1,
		},
	})
	if err != nil {
		t.Fatalf("multiraft.New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := rt.Close(); err != nil {
			t.Fatalf("Runtime.Close() error = %v", err)
		}
	})

	fsm := &snapshotChunkRestoreStateMachine{restoreCh: make(chan multiraft.Snapshot, 1)}
	const slotID = 13
	if err := rt.OpenSlot(context.Background(), multiraft.SlotOptions{
		ID:           slotID,
		Storage:      raftstorage.NewMemory(),
		StateMachine: fsm,
	}); err != nil {
		t.Fatalf("OpenSlot() error = %v", err)
	}

	header := raftpb.Message{
		Type: raftpb.MsgSnap,
		From: 1,
		To:   2,
		Term: 3,
		Snapshot: &raftpb.Snapshot{
			Metadata: raftpb.SnapshotMetadata{
				Index: 9,
				Term:  2,
				ConfState: raftpb.ConfState{
					Voters: []uint64{1, 2},
				},
			},
		},
	}
	headerData, err := header.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	base := raftSnapshotChunk{
		slotID:  slotID,
		chunkID: 101,
		from:    1,
		to:      2,
		index:   9,
		term:    2,
		total:   10,
		message: headerData,
	}
	second := base
	second.offset = 5
	second.data = []byte("world")
	first := base
	first.offset = 0
	first.data = []byte("hello")

	cluster := &Cluster{runtime: rt}
	cluster.handleRaftSnapshotChunkMessage(encodeRaftSnapshotChunkBody(second))
	cluster.handleRaftSnapshotChunkMessage(encodeRaftSnapshotChunkBody(first))

	select {
	case snap := <-fsm.restoreCh:
		if snap.Index != 9 || snap.Term != 2 {
			t.Fatalf("restored snapshot metadata = %+v", snap)
		}
		if string(snap.Data) != "helloworld" {
			t.Fatalf("restored snapshot data = %q, want helloworld", snap.Data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for restored snapshot")
	}
}

type snapshotChunkRestoreStateMachine struct {
	restoreCh chan multiraft.Snapshot
}

func (s *snapshotChunkRestoreStateMachine) Apply(context.Context, multiraft.Command) ([]byte, error) {
	return nil, nil
}

func (s *snapshotChunkRestoreStateMachine) Restore(_ context.Context, snap multiraft.Snapshot) error {
	s.restoreCh <- snap
	return nil
}

func (s *snapshotChunkRestoreStateMachine) Snapshot(context.Context) (multiraft.Snapshot, error) {
	return multiraft.Snapshot{}, nil
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
