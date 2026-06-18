package clusterv2

import (
	"context"
	"encoding/binary"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/propose"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func TestDefaultSlotProposerObservesMetaCreateSubmitAndWait(t *testing.T) {
	runtime := &recordingSlotRuntime{future: recordingSlotFuture{}}
	observer := &recordingAppendStageObserver{}
	ctx := propose.WithStageObserver(context.Background(), observer)

	err := defaultSlotProposer{runtime: runtime}.Propose(ctx, 7, propose.EncodePayload(11, []byte("cmd")))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	if runtime.proposeCalls != 1 || runtime.slotID != 7 {
		t.Fatalf("runtime propose = %d slot=%d, want one call to slot 7", runtime.proposeCalls, runtime.slotID)
	}
	if len(runtime.payload) != slotProposalEnvelopeSize+len("cmd") {
		t.Fatalf("runtime payload len = %d, want %d", len(runtime.payload), slotProposalEnvelopeSize+len("cmd"))
	}
	if hashSlot := binary.BigEndian.Uint16(runtime.payload[:2]); hashSlot != 11 {
		t.Fatalf("runtime payload hashSlot = %d, want 11", hashSlot)
	}
	if createdAtMS := binary.BigEndian.Uint64(runtime.payload[2:slotProposalEnvelopeSize]); createdAtMS == 0 {
		t.Fatalf("runtime payload created_at_ms = 0, want non-zero")
	}
	if command := string(runtime.payload[slotProposalEnvelopeSize:]); command != "cmd" {
		t.Fatalf("runtime payload command = %q, want cmd", command)
	}
	multiraft.ObserveProposalStage(runtime.ctx, "meta_create_slot_raft_commit_wait", nil, time.Millisecond)
	requireRecordedAppendStage(t, observer.events, "meta_create_slot_propose_submit", "ok")
	requireRecordedAppendStage(t, observer.events, "meta_create_slot_propose_wait", "ok")
	requireRecordedAppendStage(t, observer.events, "meta_create_slot_raft_commit_wait", "ok")
}

type recordingSlotRuntime struct {
	proposeCalls int
	slotID       multiraft.SlotID
	payload      []byte
	ctx          context.Context
	future       multiraft.Future
	err          error
}

func (r *recordingSlotRuntime) Propose(ctx context.Context, slotID multiraft.SlotID, payload []byte) (multiraft.Future, error) {
	r.proposeCalls++
	r.ctx = ctx
	r.slotID = slotID
	r.payload = append([]byte(nil), payload...)
	return r.future, r.err
}

func (r *recordingSlotRuntime) Status(multiraft.SlotID) (multiraft.Status, error) {
	return multiraft.Status{Role: multiraft.RoleLeader}, nil
}

type recordingSlotFuture struct {
	err error
}

func (f recordingSlotFuture) Wait(context.Context) (multiraft.Result, error) {
	return multiraft.Result{}, f.err
}

type recordingAppendStageObserver struct {
	events []recordedAppendStage
}

func (o *recordingAppendStageObserver) ObserveChannelAppendStage(stage string, result string, _ time.Duration) {
	o.events = append(o.events, recordedAppendStage{stage: stage, result: result})
}

type recordedAppendStage struct {
	stage  string
	result string
}

func requireRecordedAppendStage(t *testing.T, events []recordedAppendStage, stage string, result string) {
	t.Helper()
	for _, event := range events {
		if event.stage == stage && event.result == result {
			return
		}
	}
	t.Fatalf("append stage %s/%s not observed in %#v", stage, result, events)
}
