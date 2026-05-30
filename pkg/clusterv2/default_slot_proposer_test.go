package clusterv2

import (
	"context"
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
	requireRecordedAppendStage(t, observer.events, "meta_create_slot_propose_submit", "ok")
	requireRecordedAppendStage(t, observer.events, "meta_create_slot_propose_wait", "ok")
}

type recordingSlotRuntime struct {
	proposeCalls int
	slotID       multiraft.SlotID
	future       multiraft.Future
	err          error
}

func (r *recordingSlotRuntime) Propose(_ context.Context, slotID multiraft.SlotID, _ []byte) (multiraft.Future, error) {
	r.proposeCalls++
	r.slotID = slotID
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
