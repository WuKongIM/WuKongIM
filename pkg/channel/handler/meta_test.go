package handler

import (
	"errors"
	"testing"

	core "github.com/WuKongIM/WuKongIM/pkg/channel"
)

func TestApplyMetaRejectsConflictingReplay(t *testing.T) {
	id := core.ChannelID{ID: "c1", Type: 1}
	svc, _, _ := newAppendService(t, id)

	err := svc.ApplyMeta(core.Meta{
		ID:          id,
		Epoch:       7,
		LeaderEpoch: 9,
		Leader:      2,
		Replicas:    []core.NodeID{1},
		ISR:         []core.NodeID{1},
		MinISR:      1,
		Status:      core.StatusActive,
		Features:    core.Features{MessageSeqFormat: core.MessageSeqFormatU64},
	})
	if !errors.Is(err, core.ErrConflictingMeta) {
		t.Fatalf("expected ErrConflictingMeta, got %v", err)
	}
}

func TestStatusReturnsErrStaleMetaWhenCacheMisses(t *testing.T) {
	svc, err := New(Config{
		Runtime:    &fakeRuntime{channels: map[core.ChannelKey]*fakeChannelHandle{}},
		Store:      openTestEngine(t),
		MessageIDs: &fakeMessageIDGenerator{},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = svc.Status(core.ChannelID{ID: "missing", Type: 1})
	if !errors.Is(err, core.ErrStaleMeta) {
		t.Fatalf("expected ErrStaleMeta, got %v", err)
	}
}

func TestStatusReturnsRuntimeAndMetaValues(t *testing.T) {
	id := core.ChannelID{ID: "c1", Type: 1}
	svc, rt, _ := newAppendService(t, id)
	rt.channels[KeyFromChannelID(id)].status.HW = 12

	status, err := svc.Status(id)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.Key != KeyFromChannelID(id) {
		t.Fatalf("Key = %q, want %q", status.Key, KeyFromChannelID(id))
	}
	if status.ID != id {
		t.Fatalf("ID = %+v, want %+v", status.ID, id)
	}
	if status.Status != core.StatusActive {
		t.Fatalf("Status = %d, want %d", status.Status, core.StatusActive)
	}
	if status.Leader != 1 || status.LeaderEpoch != 9 {
		t.Fatalf("leader status = %+v", status)
	}
	if status.HW != 12 || status.CommittedSeq != 12 {
		t.Fatalf("commit status = %+v", status)
	}
}

func TestStatusReturnsNotReadyWhenCommitHWIsProvisional(t *testing.T) {
	id := core.ChannelID{ID: "c1", Type: 1}
	svc, rt, _ := newAppendService(t, id)
	handle := rt.channels[KeyFromChannelID(id)]
	handle.status.HW = 12
	handle.status.CheckpointHW = 3
	handle.status.CommitReady = false

	_, err := svc.Status(id)
	if !errors.Is(err, core.ErrNotReady) {
		t.Fatalf("expected ErrNotReady, got %v", err)
	}
}
