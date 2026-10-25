package rpctrace_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

func TestDispatchRequiresAdmittedDeclaredServerTrace(t *testing.T) {
	var absent *rpctrace.Trace
	var disabled *rpctrace.DispatchRecorder
	if absent.StartDispatch() != nil || disabled.Mark(rpctrace.DispatchEnqueueEnter) {
		t.Fatal("disabled dispatch acquired observation work")
	}
	r, _ := rpctrace.NewRecorder(1)
	client := r.Start(context.Background(), rpctrace.SideClient, 14)
	if client.StartDispatch() != nil || client.Finish(rpctrace.ResultOK).Dispatch != nil {
		t.Fatal("client invented server dispatch evidence")
	}
	server := r.Start(context.Background(), rpctrace.SideServer, 14)
	if server.StartDispatch() != nil {
		t.Fatal("unknown operation enabled dispatch clocks")
	}
	if r.Start(context.Background(), rpctrace.SideServer, 14).StartDispatch() != nil {
		t.Fatal("recorder drop enabled dispatch")
	}
	server.RecordOperation(11)
	if server.StartDispatch() != nil {
		t.Fatal("unbound server invented joinable dispatch evidence")
	}
	server.Bind([16]byte{1}, 1)
	dispatch := server.StartDispatch()
	if dispatch == nil || server.StartDispatch() != dispatch {
		t.Fatal("dispatch is not one bounded owner")
	}
	if !dispatch.Mark(rpctrace.DispatchEnqueueEnter) || dispatch.Mark(rpctrace.DispatchEnqueueEnter) || dispatch.Mark(rpctrace.DispatchPointCount) {
		t.Fatal("marks are not a closed first-observation set")
	}
	sample := server.Finish(rpctrace.ResultCanceled)
	if sample.Dispatch == nil || sample.Dispatch.KnownMask != 1<<rpctrace.DispatchEnqueueEnter || sample.Dispatch.QueueDeadlineKnown {
		t.Fatal("missing boundaries were represented as measured zero")
	}
	if server.StartDispatch() != nil || dispatch.Mark(rpctrace.DispatchHandlerEnter) {
		t.Fatal("late work changed the finished owner")
	}
}

func TestDispatchFreezeCloneAndPartialOrder(t *testing.T) {
	r, _ := rpctrace.NewRecorder(1)
	trace := r.Start(context.Background(), rpctrace.SideServer, 14)
	trace.Bind([16]byte{1}, 1)
	trace.RecordOperation(9)
	dispatch := trace.StartDispatch()
	deadline := time.Now().Add(time.Hour)
	if !dispatch.SetQueueDeadline(deadline) || dispatch.SetQueueDeadline(deadline.Add(time.Hour)) {
		t.Fatal("planned deadline was not captured once")
	}
	// A worker may enter before Invoke returns to its submitting goroutine.
	dispatch.Mark(rpctrace.DispatchWorkerEnter)
	dispatch.Mark(rpctrace.DispatchExecutorAccepted)
	if !dispatch.End(rpctrace.DispatchPathWorkerRejected) || dispatch.End(rpctrace.DispatchPathHandler) {
		t.Fatal("terminal responsibility was overwritten")
	}
	for i := 0; i < rpctrace.MaxSpans; i++ {
		trace.Begin(rpctrace.StageHandler).End(rpctrace.ResultOK)
	}
	sample := trace.Finish(rpctrace.ResultTimeout)
	if len(sample.Spans) != rpctrace.MaxSpans || sample.DroppedSpans != 0 || sample.Dispatch == nil {
		t.Fatal("dispatch consumed the existing span budget")
	}
	d := sample.Dispatch
	if d.KnownMask != 1<<rpctrace.DispatchWorkerEnter|1<<rpctrace.DispatchExecutorAccepted || d.Offsets[rpctrace.DispatchExecutorAccepted] < d.Offsets[rpctrace.DispatchWorkerEnter] || d.TerminalPath != rpctrace.DispatchPathWorkerRejected || !d.QueueDeadlineKnown || d.QueueDeadline <= sample.Duration {
		t.Fatalf("partial order/planned deadline lost: %+v", d)
	}
	clone := sample.Clone()
	clone.Dispatch.Offsets[rpctrace.DispatchWorkerEnter] = time.Hour
	clone.Dispatch.KnownMask = 0
	if d.KnownMask == 0 || d.Offsets[rpctrace.DispatchWorkerEnter] == time.Hour {
		t.Fatal("clone exposed mutable dispatch backing")
	}
	if dispatch.Mark(rpctrace.DispatchErrorDeliver) || dispatch.SetQueueDeadline(time.Now()) || dispatch.End(rpctrace.DispatchPathContextExpiry) {
		t.Fatal("finished dispatch accepted a late completion")
	}
	data, err := json.Marshal(sample)
	if err != nil || !strings.Contains(string(data), `"dispatch":`) || !strings.Contains(string(data), `"terminal_path":"worker_rejected"`) {
		t.Fatalf("dispatch JSON missing: %s / %v", data, err)
	}
	unknown, _ := json.Marshal(rpctrace.Sample{})
	if strings.Contains(string(unknown), `"dispatch"`) {
		t.Fatal("absent dispatch became known")
	}
	zero := rpctrace.Sample{Dispatch: &rpctrace.DispatchSample{KnownMask: 1 << rpctrace.DispatchEnqueueEnter}}
	zeroJSON, _ := json.Marshal(zero)
	if !strings.Contains(string(zeroJSON), `"known_mask":1`) || zero.Dispatch.Offsets[rpctrace.DispatchEnqueueEnter] != 0 {
		t.Fatal("known zero offset cannot be distinguished from an absent mark")
	}
}

func TestDispatchConcurrentFinishNeverChangesPublishedSample(t *testing.T) {
	r, _ := rpctrace.NewRecorder(1)
	for n := 0; n < 100; n++ {
		trace := r.Start(context.Background(), rpctrace.SideServer, 14)
		trace.Bind([16]byte{1}, uint64(n+1))
		trace.RecordOperation(11)
		dispatch := trace.StartDispatch()
		var work sync.WaitGroup
		work.Add(1)
		go func() {
			defer work.Done()
			for point := rpctrace.DispatchPoint(0); point < rpctrace.DispatchPointCount; point++ {
				dispatch.Mark(point)
			}
			dispatch.End(rpctrace.DispatchPathContextExpiry)
		}()
		sample := trace.Finish(rpctrace.ResultCanceled)
		before := *sample.Dispatch
		work.Wait()
		if *sample.Dispatch != before {
			t.Fatal("late cancellation mutated frozen evidence")
		}
		for point, offset := range sample.Dispatch.Offsets {
			if sample.Dispatch.KnownMask&(1<<point) != 0 && (offset < 0 || offset > sample.Duration) {
				t.Fatal("published mark lies outside this sample's local clock")
			}
		}
	}
}
