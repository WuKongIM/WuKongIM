package handler

import (
	"context"
	"errors"
	"testing"

	core "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
)

func TestAppendUsesRuntimeKeyAndReturnsMessageSeq(t *testing.T) {
	id := core.ChannelID{ID: "room-1", Type: 2}
	key := KeyFromChannelID(id)
	engine := openTestEngine(t)
	store := engine.ForChannel(key, id)
	handle := &fakeChannelHandle{
		id: key,
		status: core.ReplicaState{
			ChannelKey:  key,
			Role:        core.ReplicaRoleLeader,
			CommitReady: true,
		},
	}
	handle.appendFn = func(_ context.Context, records []core.Record) (core.CommitResult, error) {
		base, err := store.Append(records)
		if err != nil {
			return core.CommitResult{}, err
		}
		handle.status.HW = base + uint64(len(records))
		return core.CommitResult{BaseOffset: base, NextCommitHW: handle.status.HW, RecordCount: len(records)}, nil
	}
	rt := &fakeRuntime{channels: map[core.ChannelKey]*fakeChannelHandle{key: handle}}

	svc, err := New(Config{
		Runtime:    rt,
		Store:      engine,
		MessageIDs: &fakeMessageIDGenerator{},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := svc.ApplyMeta(core.Meta{
		Key:         key,
		ID:          id,
		Epoch:       7,
		LeaderEpoch: 9,
		Leader:      1,
		Replicas:    []core.NodeID{1},
		ISR:         []core.NodeID{1},
		MinISR:      1,
		Status:      core.StatusActive,
		Features:    core.Features{MessageSeqFormat: core.MessageSeqFormatU64},
	}); err != nil {
		t.Fatalf("ApplyMeta() error = %v", err)
	}

	res, err := svc.Append(context.Background(), core.AppendRequest{
		ChannelID:             id,
		SupportsMessageSeqU64: true,
		Message: core.Message{
			FromUID:     "u1",
			ClientMsgNo: "m1",
			Payload:     []byte("payload"),
		},
	})
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if res.MessageSeq != 1 {
		t.Fatalf("MessageSeq = %d, want 1", res.MessageSeq)
	}
	if res.MessageID != 1 {
		t.Fatalf("MessageID = %d, want 1", res.MessageID)
	}
	if handle.idCalls != 0 {
		t.Fatalf("ID() calls = %d, want 0", handle.idCalls)
	}
	if handle.appendCalls != 1 {
		t.Fatalf("Append() calls = %d, want 1", handle.appendCalls)
	}
	if rt.lastChannelKey != key {
		t.Fatalf("runtime.Channel() key = %q, want %q", rt.lastChannelKey, key)
	}
}

func TestAppendDefaultsToQuorumCommitMode(t *testing.T) {
	id := core.ChannelID{ID: "room-2", Type: 2}
	key := KeyFromChannelID(id)
	engine := openTestEngine(t)
	store := engine.ForChannel(key, id)
	var gotMode core.CommitMode
	handle := &fakeChannelHandle{
		id: key,
		status: core.ReplicaState{
			ChannelKey:  key,
			Role:        core.ReplicaRoleLeader,
			CommitReady: true,
		},
	}
	handle.appendFn = func(ctx context.Context, records []core.Record) (core.CommitResult, error) {
		gotMode = core.CommitModeFromContext(ctx)
		base, err := store.Append(records)
		if err != nil {
			return core.CommitResult{}, err
		}
		handle.status.HW = base + uint64(len(records))
		return core.CommitResult{BaseOffset: base, NextCommitHW: handle.status.HW, RecordCount: len(records)}, nil
	}
	rt := &fakeRuntime{channels: map[core.ChannelKey]*fakeChannelHandle{key: handle}}

	svc, err := New(Config{
		Runtime:    rt,
		Store:      engine,
		MessageIDs: &fakeMessageIDGenerator{},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := svc.ApplyMeta(core.Meta{
		Key:         key,
		ID:          id,
		Epoch:       7,
		LeaderEpoch: 9,
		Leader:      1,
		Replicas:    []core.NodeID{1},
		ISR:         []core.NodeID{1},
		MinISR:      1,
		Status:      core.StatusActive,
		Features:    core.Features{MessageSeqFormat: core.MessageSeqFormatU64},
	}); err != nil {
		t.Fatalf("ApplyMeta() error = %v", err)
	}

	_, err = svc.Append(context.Background(), core.AppendRequest{
		ChannelID:             id,
		SupportsMessageSeqU64: true,
		Message: core.Message{
			FromUID:     "u1",
			ClientMsgNo: "m1",
			Payload:     []byte("payload"),
		},
	})
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if gotMode != core.CommitModeQuorum {
		t.Fatalf("commit mode = %v, want quorum", gotMode)
	}
}

func TestAppendPropagatesLocalCommitModeToReplica(t *testing.T) {
	id := core.ChannelID{ID: "room-3", Type: 2}
	key := KeyFromChannelID(id)
	engine := openTestEngine(t)
	store := engine.ForChannel(key, id)
	var gotMode core.CommitMode
	handle := &fakeChannelHandle{
		id: key,
		status: core.ReplicaState{
			ChannelKey:  key,
			Role:        core.ReplicaRoleLeader,
			CommitReady: true,
		},
	}
	handle.appendFn = func(ctx context.Context, records []core.Record) (core.CommitResult, error) {
		gotMode = core.CommitModeFromContext(ctx)
		base, err := store.Append(records)
		if err != nil {
			return core.CommitResult{}, err
		}
		handle.status.HW = base + uint64(len(records))
		return core.CommitResult{BaseOffset: base, NextCommitHW: handle.status.HW, RecordCount: len(records)}, nil
	}
	rt := &fakeRuntime{channels: map[core.ChannelKey]*fakeChannelHandle{key: handle}}

	svc, err := New(Config{
		Runtime:    rt,
		Store:      engine,
		MessageIDs: &fakeMessageIDGenerator{},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := svc.ApplyMeta(core.Meta{
		Key:         key,
		ID:          id,
		Epoch:       7,
		LeaderEpoch: 9,
		Leader:      1,
		Replicas:    []core.NodeID{1},
		ISR:         []core.NodeID{1},
		MinISR:      1,
		Status:      core.StatusActive,
		Features:    core.Features{MessageSeqFormat: core.MessageSeqFormatU64},
	}); err != nil {
		t.Fatalf("ApplyMeta() error = %v", err)
	}

	_, err = svc.Append(context.Background(), core.AppendRequest{
		ChannelID:             id,
		SupportsMessageSeqU64: true,
		CommitMode:            core.CommitModeLocal,
		Message: core.Message{
			FromUID:     "u1",
			ClientMsgNo: "m1",
			Payload:     []byte("payload"),
		},
	})
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if gotMode != core.CommitModeLocal {
		t.Fatalf("commit mode = %v, want local", gotMode)
	}
}

func TestAppendReturnsExistingEntryOnIdempotentRetry(t *testing.T) {
	id := core.ChannelID{ID: "room-1", Type: 2}
	svc, rt, _ := newAppendService(t, id)

	first, err := svc.Append(context.Background(), core.AppendRequest{
		ChannelID:             id,
		SupportsMessageSeqU64: true,
		Message: core.Message{
			FromUID:     "u1",
			ClientMsgNo: "m1",
			Payload:     []byte("payload"),
		},
	})
	if err != nil {
		t.Fatalf("first Append() error = %v", err)
	}
	second, err := svc.Append(context.Background(), core.AppendRequest{
		ChannelID:             id,
		SupportsMessageSeqU64: true,
		Message: core.Message{
			FromUID:     "u1",
			ClientMsgNo: "m1",
			Payload:     []byte("payload"),
		},
	})
	if err != nil {
		t.Fatalf("second Append() error = %v", err)
	}
	if first.MessageID != second.MessageID || first.MessageSeq != second.MessageSeq {
		t.Fatalf("idempotent results differ: first=%+v second=%+v", first, second)
	}
	if rt.channels[KeyFromChannelID(id)].appendCalls != 1 {
		t.Fatalf("Append() calls = %d, want 1", rt.channels[KeyFromChannelID(id)].appendCalls)
	}
}

func TestAppendIdempotencyHitReturnsStoredMessageFromUniqueIndex(t *testing.T) {
	key := core.IdempotencyKey{
		ChannelID:   core.ChannelID{ID: "room-1", Type: 2},
		FromUID:     "u1",
		ClientMsgNo: "m1",
	}
	store := &fakeAppendLookupStore{
		hit:       core.IdempotencyEntry{MessageID: 11, MessageSeq: 7, Offset: 6},
		hitHash:   hashPayload([]byte("payload")),
		lookupOK:  true,
		message:   core.Message{MessageID: 11, MessageSeq: 7, Payload: []byte("payload"), FromUID: "u1", ClientMsgNo: "m1"},
		messageOK: true,
	}

	result, ok, err := resolveIdempotentAppendFromStore(store, key, core.Message{Payload: []byte("payload")})
	if err != nil {
		t.Fatalf("resolveIdempotentAppendFromStore() error = %v", err)
	}
	if !ok {
		t.Fatal("expected idempotency hit")
	}
	if result.MessageID != 11 || result.MessageSeq != 7 {
		t.Fatalf("result = %+v", result)
	}
	if store.lookupCalls != 1 {
		t.Fatalf("LookupIdempotency() calls = %d, want 1", store.lookupCalls)
	}
	if store.messageCalls != 1 {
		t.Fatalf("GetMessageBySeq() calls = %d, want 1", store.messageCalls)
	}
}

func TestAppendReturnsErrProtocolUpgradeRequiredForLegacyClientOnU64Channel(t *testing.T) {
	id := core.ChannelID{ID: "room-1", Type: 2}
	svc, _, _ := newAppendService(t, id)

	if err := svc.ApplyMeta(core.Meta{
		ID:          id,
		Epoch:       8,
		LeaderEpoch: 10,
		Leader:      1,
		Replicas:    []core.NodeID{1},
		ISR:         []core.NodeID{1},
		MinISR:      1,
		Status:      core.StatusActive,
		Features:    core.Features{MessageSeqFormat: core.MessageSeqFormatU64},
	}); err != nil {
		t.Fatalf("ApplyMeta() error = %v", err)
	}

	_, err := svc.Append(context.Background(), core.AppendRequest{
		ChannelID:             id,
		SupportsMessageSeqU64: false,
		Message: core.Message{
			FromUID:     "u1",
			ClientMsgNo: "m1",
			Payload:     []byte("payload"),
		},
	})
	if !errors.Is(err, core.ErrProtocolUpgradeRequired) {
		t.Fatalf("expected ErrProtocolUpgradeRequired, got %v", err)
	}
}

func TestAppendReturnsExistingEntryAtLegacySeqCeiling(t *testing.T) {
	id := core.ChannelID{ID: "room-1", Type: 2}
	svc, rt, _ := newAppendService(t, id)

	if err := svc.ApplyMeta(core.Meta{
		ID:          id,
		Epoch:       8,
		LeaderEpoch: 10,
		Leader:      1,
		Replicas:    []core.NodeID{1},
		ISR:         []core.NodeID{1},
		MinISR:      1,
		Status:      core.StatusActive,
		Features:    core.Features{MessageSeqFormat: core.MessageSeqFormatLegacyU32},
	}); err != nil {
		t.Fatalf("ApplyMeta() error = %v", err)
	}

	first, err := svc.Append(context.Background(), core.AppendRequest{
		ChannelID:             id,
		SupportsMessageSeqU64: true,
		Message: core.Message{
			FromUID:     "u1",
			ClientMsgNo: "m-legacy",
			Payload:     []byte("payload"),
		},
	})
	if err != nil {
		t.Fatalf("first Append() error = %v", err)
	}

	rt.channels[KeyFromChannelID(id)].status.HW = maxLegacyMessageSeq

	second, err := svc.Append(context.Background(), core.AppendRequest{
		ChannelID:             id,
		SupportsMessageSeqU64: true,
		Message: core.Message{
			FromUID:     "u1",
			ClientMsgNo: "m-legacy",
			Payload:     []byte("payload"),
		},
	})
	if err != nil {
		t.Fatalf("retry Append() error = %v", err)
	}
	if second.MessageID != first.MessageID || second.MessageSeq != first.MessageSeq {
		t.Fatalf("retry result = %+v, want %+v", second, first)
	}
	if rt.channels[KeyFromChannelID(id)].appendCalls != 1 {
		t.Fatalf("Append() calls = %d, want 1", rt.channels[KeyFromChannelID(id)].appendCalls)
	}
}

func newAppendService(t *testing.T, id core.ChannelID) (Service, *fakeRuntime, *store.Engine) {
	t.Helper()

	key := KeyFromChannelID(id)
	engine := openTestEngine(t)
	st := engine.ForChannel(key, id)
	handle := &fakeChannelHandle{
		id: key,
		status: core.ReplicaState{
			ChannelKey:  key,
			Role:        core.ReplicaRoleLeader,
			CommitReady: true,
		},
	}
	handle.appendFn = func(_ context.Context, records []core.Record) (core.CommitResult, error) {
		base, err := st.Append(records)
		if err != nil {
			return core.CommitResult{}, err
		}
		handle.status.HW = base + uint64(len(records))
		return core.CommitResult{BaseOffset: base, NextCommitHW: handle.status.HW, RecordCount: len(records)}, nil
	}
	rt := &fakeRuntime{channels: map[core.ChannelKey]*fakeChannelHandle{key: handle}}

	svc, err := New(Config{
		Runtime:    rt,
		Store:      engine,
		MessageIDs: &fakeMessageIDGenerator{},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := svc.ApplyMeta(core.Meta{
		Key:         key,
		ID:          id,
		Epoch:       7,
		LeaderEpoch: 9,
		Leader:      1,
		Replicas:    []core.NodeID{1},
		ISR:         []core.NodeID{1},
		MinISR:      1,
		Status:      core.StatusActive,
		Features:    core.Features{MessageSeqFormat: core.MessageSeqFormatU64},
	}); err != nil {
		t.Fatalf("ApplyMeta() error = %v", err)
	}
	return svc, rt, engine
}

type fakeAppendLookupStore struct {
	hit          core.IdempotencyEntry
	hitHash      uint64
	lookupOK     bool
	lookupCalls  int
	lookupErr    error
	message      core.Message
	messageOK    bool
	messageCalls int
	messageErr   error
}

func (f *fakeAppendLookupStore) LookupIdempotency(key core.IdempotencyKey) (core.IdempotencyEntry, uint64, bool, error) {
	f.lookupCalls++
	return f.hit, f.hitHash, f.lookupOK, f.lookupErr
}

func (f *fakeAppendLookupStore) GetMessageBySeq(seq uint64) (core.Message, bool, error) {
	f.messageCalls++
	return f.message, f.messageOK, f.messageErr
}

func openTestEngine(tb testing.TB) *store.Engine {
	tb.Helper()
	engine, err := store.Open(tb.TempDir())
	if err != nil {
		tb.Fatalf("Open() error = %v", err)
	}
	tb.Cleanup(func() {
		if err := engine.Close(); err != nil {
			tb.Fatalf("Close() error = %v", err)
		}
	})
	return engine
}

type fakeRuntime struct {
	channels       map[core.ChannelKey]*fakeChannelHandle
	lastChannelKey core.ChannelKey
}

func (r *fakeRuntime) EnsureChannel(meta core.Meta) error      { return nil }
func (r *fakeRuntime) RemoveChannel(key core.ChannelKey) error { return nil }
func (r *fakeRuntime) ApplyMeta(meta core.Meta) error          { return nil }
func (r *fakeRuntime) Close() error                            { return nil }
func (r *fakeRuntime) Channel(key core.ChannelKey) (core.HandlerChannel, bool) {
	r.lastChannelKey = key
	h, ok := r.channels[key]
	return h, ok
}

type fakeChannelHandle struct {
	id          core.ChannelKey
	status      core.ReplicaState
	appendCalls int
	idCalls     int
	appendFn    func(context.Context, []core.Record) (core.CommitResult, error)
}

func (h *fakeChannelHandle) ID() core.ChannelKey {
	h.idCalls++
	return h.id
}

func (h *fakeChannelHandle) Meta() core.Meta           { return core.Meta{Key: h.id} }
func (h *fakeChannelHandle) Status() core.ReplicaState { return h.status }
func (h *fakeChannelHandle) Append(ctx context.Context, records []core.Record) (core.CommitResult, error) {
	h.appendCalls++
	return h.appendFn(ctx, records)
}

type fakeMessageIDGenerator struct{ next uint64 }

func (g *fakeMessageIDGenerator) Next() uint64 {
	g.next++
	return g.next
}
