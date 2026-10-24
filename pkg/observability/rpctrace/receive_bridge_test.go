package rpctrace_test

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

// Failure matrix: receive evidence is optional, identity-bound and endpoint-
// specific; historical times are ordered and local; a response contributes one
// atomic three-span group; prefix data never moves the existing RPC window;
// publication and Clone detach data; invalid or late evidence changes no result.
// The narrow public interface keeps this test file compilable before the new
// methods exist. The JSON test independently catches an old Sample discarding
// the new optional field, rather than relying only on a missing method failure.
type receiveBridgeRecorder interface {
	RecordReceivePrefix(time.Time, time.Time) bool
	RecordResponseReceive(time.Time, time.Time, time.Time, time.Time, rpctrace.Result) bool
}

type receiveBridgePrefix struct {
	Duration         time.Duration `json:"duration"`
	ObserverDuration time.Duration `json:"observer_duration"`
}

func requireReceiveBridge(t *testing.T, trace *rpctrace.Trace) receiveBridgeRecorder {
	t.Helper()
	recorder, ok := any(trace).(receiveBridgeRecorder)
	if !ok {
		t.Fatal("Trace does not implement the public receive-evidence contract")
	}
	return recorder
}

func newReceiveBridgeTrace(t *testing.T, side rpctrace.Side, bind bool) (*rpctrace.Recorder, *rpctrace.Trace) {
	t.Helper()
	recorder, err := rpctrace.NewRecorder(1)
	if err != nil {
		t.Fatal(err)
	}
	trace := recorder.Start(context.Background(), side, 91)
	if trace == nil {
		t.Fatal("trace admission failed")
	}
	if bind && !trace.Bind([16]byte{1, 2, 3}, 7) {
		t.Fatal("connection identity binding failed")
	}
	t.Cleanup(func() { trace.Finish(rpctrace.ResultStopped) })
	return recorder, trace
}

func receiveBridgePrefixFromJSON(t *testing.T, sample *rpctrace.Sample) (*receiveBridgePrefix, bool) {
	t.Helper()
	encoded, err := json.Marshal(sample)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	raw, present := fields["receive_prefix"]
	if !present {
		return nil, false
	}
	var prefix *receiveBridgePrefix
	if err := json.Unmarshal(raw, &prefix); err != nil {
		t.Fatal(err)
	}
	return prefix, true
}

func receiveBridgePoints() [4]time.Time {
	// Real observations provide these monotonic points. No artificial wait is
	// needed: equal adjacent instants are valid, measured zero-length intervals.
	return [4]time.Time{time.Now(), time.Now(), time.Now(), time.Now()}
}

func recordReceiveBridgeResponse(recorder receiveBridgeRecorder, points [4]time.Time, result rpctrace.Result) bool {
	return recorder.RecordResponseReceive(points[0], points[1], points[2], points[3], result)
}

func assertReceiveBridgeResponse(t *testing.T, sample *rpctrace.Sample, points [4]time.Time, result rpctrace.Result) {
	t.Helper()
	stages := [3]rpctrace.Stage{"response_receive_observer", "response_dispatch", "response_resume"}
	if len(sample.Spans) != len(stages) {
		t.Fatalf("response evidence must be one complete group, got %d spans", len(sample.Spans))
	}
	for i, stage := range stages {
		span := sample.Spans[i]
		if span.Stage != stage || span.Result != result || span.Incomplete {
			t.Fatalf("receive interval %d has the wrong stage or completion: %+v", i, span)
		}
		if span.Offset != points[i].Sub(sample.At) || span.Duration != points[i+1].Sub(points[i]) {
			t.Fatalf("receive interval %d changed its caller-supplied local boundaries: %+v", i, span)
		}
		if span.Offset < 0 || span.Duration < 0 || span.Offset > sample.Duration || span.Duration > sample.Duration-span.Offset {
			t.Fatalf("receive interval %d escaped the original RPC window", i)
		}
	}
}

func TestReceiveBridgeJSONPrefixRetainedAndCloneDetached(t *testing.T) {
	var sample rpctrace.Sample
	if err := json.Unmarshal([]byte(`{"receive_prefix":{"duration":17,"observer_duration":5}}`), &sample); err != nil {
		t.Fatal(err)
	}
	prefix, present := receiveBridgePrefixFromJSON(t, &sample)
	if !present || prefix == nil || *prefix != (receiveBridgePrefix{Duration: 17, ObserverDuration: 5}) {
		t.Fatal("Sample discarded the optional receive prefix")
	}
	clone := sample.Clone()
	// Decoding into an existing nested pointer mutates its value in place. A
	// shallow Clone would therefore mutate the original public Sample as well.
	if err := json.Unmarshal([]byte(`{"receive_prefix":{"duration":23,"observer_duration":7}}`), &clone); err != nil {
		t.Fatal(err)
	}
	prefix, _ = receiveBridgePrefixFromJSON(t, &sample)
	if prefix == nil || prefix.Duration != 17 || prefix.ObserverDuration != 5 {
		t.Fatal("Sample.Clone aliases receive-prefix storage")
	}
	var legacy rpctrace.Sample
	if _, present := receiveBridgePrefixFromJSON(t, &legacy); present {
		t.Fatal("legacy absence became measured zero receive evidence")
	}
}

func TestReceiveBridgeServerPrefixPreservesOriginalWindowAndSpanLimit(t *testing.T) {
	frameReady := time.Now().Add(-time.Millisecond)
	observerDone := frameReady.Add(2 * time.Nanosecond)
	startLower := time.Now()
	recorder, trace := newReceiveBridgeTrace(t, rpctrace.SideServer, true)
	startUpper := time.Now()
	bridge := requireReceiveBridge(t, trace)
	for i := 0; i < rpctrace.MaxSpans; i++ {
		trace.Begin(rpctrace.StageHandler).End(rpctrace.ResultOK)
	}
	if !bridge.RecordReceivePrefix(frameReady, observerDone) {
		t.Fatal("valid server prefix was rejected at the existing span limit")
	}
	finishLower := time.Now()
	sample := trace.Finish(rpctrace.ResultOK)
	finishUpper := time.Now()
	if sample.At.Before(startLower) || sample.At.After(startUpper) {
		t.Fatal("prefix moved the original server Trace.At")
	}
	end := sample.At.Add(sample.Duration)
	if end.Before(finishLower) || end.After(finishUpper) {
		t.Fatal("prefix changed the original server Trace.Duration")
	}
	prefix, present := receiveBridgePrefixFromJSON(t, sample)
	if !present || prefix == nil || prefix.Duration != sample.At.Sub(frameReady) || prefix.ObserverDuration != 2*time.Nanosecond {
		t.Fatal("server prefix did not preserve its local boundaries")
	}
	if len(sample.Spans) != rpctrace.MaxSpans || sample.DroppedSpans != 0 || recorder.Active() != 0 {
		t.Fatal("fixed prefix consumed span capacity or recorder credit")
	}
}

func TestReceiveBridgeClientRecordsOrderedCompleteIntervals(t *testing.T) {
	_, trace := newReceiveBridgeTrace(t, rpctrace.SideClient, true)
	points := receiveBridgePoints()
	if !recordReceiveBridgeResponse(requireReceiveBridge(t, trace), points, rpctrace.ResultOK) {
		t.Fatal("valid client receive evidence rejected")
	}
	// A later business outcome does not change what a completed receive measured.
	sample := trace.Finish(rpctrace.ResultTimeout)
	assertReceiveBridgeResponse(t, sample, points, rpctrace.ResultOK)
	if sample.Result != rpctrace.ResultTimeout || sample.DroppedSpans != 0 {
		t.Fatal("observation changed the enclosing RPC outcome")
	}
	if _, present := receiveBridgePrefixFromJSON(t, sample); present {
		t.Fatal("client response produced a server receive prefix")
	}
}

func TestReceiveBridgeRejectsNilWrongSideUnboundAndClosed(t *testing.T) {
	frame := time.Now().Add(-time.Millisecond)
	points := receiveBridgePoints()
	var disabled *rpctrace.Trace
	nilBridge := requireReceiveBridge(t, disabled)
	if nilBridge.RecordReceivePrefix(frame, frame) || recordReceiveBridgeResponse(nilBridge, points, rpctrace.ResultOK) {
		t.Fatal("nil trace accepted evidence")
	}
	for _, tc := range []struct {
		name   string
		side   rpctrace.Side
		bind   bool
		closed bool
	}{
		{name: "unbound_server", side: rpctrace.SideServer},
		{name: "unbound_client", side: rpctrace.SideClient},
		{name: "closed_server", side: rpctrace.SideServer, bind: true, closed: true},
		{name: "closed_client", side: rpctrace.SideClient, bind: true, closed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, trace := newReceiveBridgeTrace(t, tc.side, tc.bind)
			bridge := requireReceiveBridge(t, trace)
			if tc.closed {
				trace.Finish(rpctrace.ResultOK)
			}
			if bridge.RecordReceivePrefix(frame, frame) || recordReceiveBridgeResponse(bridge, receiveBridgePoints(), rpctrace.ResultOK) {
				t.Fatal("unbound or closed trace accepted evidence")
			}
		})
	}
	_, server := newReceiveBridgeTrace(t, rpctrace.SideServer, true)
	if recordReceiveBridgeResponse(requireReceiveBridge(t, server), receiveBridgePoints(), rpctrace.ResultOK) {
		t.Fatal("server accepted client receive stages")
	}
	_, client := newReceiveBridgeTrace(t, rpctrace.SideClient, true)
	if requireReceiveBridge(t, client).RecordReceivePrefix(frame, frame) {
		t.Fatal("client accepted a server receive prefix")
	}
}

func TestReceiveBridgeRejectsInvalidPrefixWithoutPoisoningValidEvidence(t *testing.T) {
	for _, name := range []string{"zero_frame", "zero_observer", "inverted", "frame_after_start", "observer_after_start"} {
		t.Run(name, func(t *testing.T) {
			frame := time.Now().Add(-time.Millisecond)
			_, trace := newReceiveBridgeTrace(t, rpctrace.SideServer, true)
			bridge := requireReceiveBridge(t, trace)
			badFrame, badObserver := frame, frame.Add(time.Nanosecond)
			switch name {
			case "zero_frame":
				badFrame = time.Time{}
			case "zero_observer":
				badObserver = time.Time{}
			case "inverted":
				badObserver = frame.Add(-time.Nanosecond)
			case "frame_after_start":
				badFrame = time.Now().Add(time.Hour)
				badObserver = badFrame
			case "observer_after_start":
				badObserver = time.Now().Add(time.Hour)
			}
			if bridge.RecordReceivePrefix(badFrame, badObserver) {
				t.Fatal("invalid prefix was accepted")
			}
			if !bridge.RecordReceivePrefix(frame, frame) {
				t.Fatal("invalid prefix poisoned later valid measured-zero evidence")
			}
			prefix, present := receiveBridgePrefixFromJSON(t, trace.Finish(rpctrace.ResultOK))
			if !present || prefix == nil || prefix.ObserverDuration != 0 || prefix.Duration <= 0 {
				t.Fatal("valid zero observer duration was changed or omitted")
			}
		})
	}
}

func TestReceiveBridgeRejectsInvalidResponseWithoutPartialPublication(t *testing.T) {
	for _, name := range []string{"zero_frame", "before_trace", "observer_before_frame", "notify_before_observer", "resume_before_notify", "future_resume"} {
		t.Run(name, func(t *testing.T) {
			beforeStart := time.Now().Add(-time.Millisecond)
			_, trace := newReceiveBridgeTrace(t, rpctrace.SideClient, true)
			bridge := requireReceiveBridge(t, trace)
			bad := receiveBridgePoints()
			switch name {
			case "zero_frame":
				bad[0] = time.Time{}
			case "before_trace":
				bad[0] = beforeStart
			case "observer_before_frame":
				bad[1] = bad[0].Add(-time.Nanosecond)
			case "notify_before_observer":
				bad[2] = bad[1].Add(-time.Nanosecond)
			case "resume_before_notify":
				bad[3] = bad[2].Add(-time.Nanosecond)
			case "future_resume":
				bad[3] = time.Now().Add(time.Hour)
			}
			if recordReceiveBridgeResponse(bridge, bad, rpctrace.ResultOK) {
				t.Fatal("invalid response timing was accepted")
			}
			at := time.Now()
			valid := [4]time.Time{at, at, at, at}
			if !recordReceiveBridgeResponse(bridge, valid, rpctrace.ResultOK) {
				t.Fatal("invalid response timing poisoned a later valid candidate")
			}
			assertReceiveBridgeResponse(t, trace.Finish(rpctrace.ResultOK), valid, rpctrace.ResultOK)
		})
	}
}

func TestReceiveBridgeFirstValidRecordWinsAndFinishedSampleIsImmutable(t *testing.T) {
	frame := time.Now().Add(-time.Millisecond)
	_, server := newReceiveBridgeTrace(t, rpctrace.SideServer, true)
	prefixRecorder := requireReceiveBridge(t, server)
	if !prefixRecorder.RecordReceivePrefix(frame, frame.Add(2*time.Nanosecond)) || prefixRecorder.RecordReceivePrefix(frame, frame.Add(3*time.Nanosecond)) {
		t.Fatal("server prefix is not first-valid-wins")
	}
	serverSample := server.Finish(rpctrace.ResultOK)
	// Compare publication bytes because Clone intentionally normalizes an empty
	// non-nil Spans slice to nil; that representation is unrelated to immutability.
	serverBefore, err := json.Marshal(serverSample)
	if err != nil {
		t.Fatal(err)
	}
	latePrefix := prefixRecorder.RecordReceivePrefix(frame, frame)
	serverAfter, err := json.Marshal(serverSample)
	if err != nil {
		t.Fatal(err)
	}
	if latePrefix || string(serverBefore) != string(serverAfter) {
		t.Fatal("late prefix modified the published sample")
	}
	prefix, _ := receiveBridgePrefixFromJSON(t, serverSample)
	if prefix == nil || prefix.ObserverDuration != 2*time.Nanosecond {
		t.Fatal("duplicate prefix replaced the first valid value")
	}
	_, client := newReceiveBridgeTrace(t, rpctrace.SideClient, true)
	responseRecorder := requireReceiveBridge(t, client)
	first := receiveBridgePoints()
	if !recordReceiveBridgeResponse(responseRecorder, first, rpctrace.ResultOK) || recordReceiveBridgeResponse(responseRecorder, receiveBridgePoints(), rpctrace.ResultError) {
		t.Fatal("client receive group is not first-valid-wins")
	}
	clientSample := client.Finish(rpctrace.ResultOK)
	clientBefore, err := json.Marshal(clientSample)
	if err != nil {
		t.Fatal(err)
	}
	lateResponse := recordReceiveBridgeResponse(responseRecorder, receiveBridgePoints(), rpctrace.ResultError)
	clientAfter, err := json.Marshal(clientSample)
	if err != nil {
		t.Fatal(err)
	}
	if lateResponse || string(clientBefore) != string(clientAfter) {
		t.Fatal("late response evidence modified the published sample")
	}
	assertReceiveBridgeResponse(t, clientSample, first, rpctrace.ResultOK)
	if clientSample.DroppedSpans != 0 {
		t.Fatal("duplicate or closed callback was counted as span-capacity loss")
	}
}

func TestReceiveBridgeResponseGroupHonorsExistingSpanCapacity(t *testing.T) {
	for _, occupied := range []int{0, 29, 30, 31, 32} {
		t.Run(strconv.Itoa(occupied), func(t *testing.T) {
			_, trace := newReceiveBridgeTrace(t, rpctrace.SideClient, true)
			for i := 0; i < occupied; i++ {
				trace.Begin(rpctrace.StageHandler).End(rpctrace.ResultOK)
			}
			accepted := recordReceiveBridgeResponse(requireReceiveBridge(t, trace), receiveBridgePoints(), rpctrace.ResultOK)
			sample := trace.Finish(rpctrace.ResultOK)
			wantAccepted := occupied <= rpctrace.MaxSpans-3
			wantCount, wantDropped := occupied, uint64(3)
			if wantAccepted {
				wantCount += 3
				wantDropped = 0
			}
			if accepted != wantAccepted || len(sample.Spans) != wantCount || sample.DroppedSpans != wantDropped {
				t.Fatalf("occupied=%d: accepted=%v spans=%d dropped=%d", occupied, accepted, len(sample.Spans), sample.DroppedSpans)
			}
			for _, span := range sample.Spans[:occupied] {
				if span.Stage != rpctrace.StageHandler || span.Result != rpctrace.ResultOK || span.Incomplete {
					t.Fatal("receive publication changed an existing span")
				}
			}
		})
	}
}

func TestReceiveBridgeNormalizesIntervalOutcomeWithoutChangingRPCResult(t *testing.T) {
	_, trace := newReceiveBridgeTrace(t, rpctrace.SideClient, true)
	points := receiveBridgePoints()
	if !recordReceiveBridgeResponse(requireReceiveBridge(t, trace), points, rpctrace.Result("arbitrary-detail")) {
		t.Fatal("result normalization rejected otherwise valid timing")
	}
	sample := trace.Finish(rpctrace.ResultOK)
	assertReceiveBridgeResponse(t, sample, points, rpctrace.ResultError)
	if sample.Result != rpctrace.ResultOK {
		t.Fatal("interval result changed the enclosing business result")
	}
}

func TestReceiveBridgeConcurrentCandidatesPublishOneCompleteGroup(t *testing.T) {
	recorder, trace := newReceiveBridgeTrace(t, rpctrace.SideClient, true)
	bridge := requireReceiveBridge(t, trace)
	points := receiveBridgePoints()
	start := make(chan struct{})
	var workers sync.WaitGroup
	var accepted atomic.Int32
	for i := 0; i < 32; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			if recordReceiveBridgeResponse(bridge, points, rpctrace.ResultOK) {
				accepted.Add(1)
			}
		}()
	}
	close(start)
	workers.Wait()
	sample := trace.Finish(rpctrace.ResultOK)
	assertReceiveBridgeResponse(t, sample, points, rpctrace.ResultOK)
	if accepted.Load() != 1 || sample.DroppedSpans != 0 || recorder.Active() != 0 {
		t.Fatal("concurrent candidates duplicated evidence or leaked observation credit")
	}
}

func TestReceiveBridgeFinishRacePublishesAbsentOrWholeEvidence(t *testing.T) {
	for _, side := range []rpctrace.Side{rpctrace.SideServer, rpctrace.SideClient} {
		t.Run(string(side), func(t *testing.T) {
			frame := time.Now().Add(-time.Millisecond)
			recorder, trace := newReceiveBridgeTrace(t, side, true)
			bridge := requireReceiveBridge(t, trace)
			points := receiveBridgePoints()
			start := make(chan struct{})
			accepted := make(chan bool, 1)
			finished := make(chan *rpctrace.Sample, 1)
			go func() {
				<-start
				if side == rpctrace.SideServer {
					accepted <- bridge.RecordReceivePrefix(frame, frame.Add(2*time.Nanosecond))
				} else {
					accepted <- recordReceiveBridgeResponse(bridge, points, rpctrace.ResultOK)
				}
			}()
			go func() {
				<-start
				finished <- trace.Finish(rpctrace.ResultCanceled)
			}()
			close(start)
			sample, recorded := <-finished, <-accepted
			if sample == nil || sample.Result != rpctrace.ResultCanceled || sample.DroppedSpans != 0 || recorder.Active() != 0 || trace.Finish(rpctrace.ResultOK) != nil {
				t.Fatal("Finish race violated single publication or observation credit")
			}
			if side == rpctrace.SideServer {
				prefix, present := receiveBridgePrefixFromJSON(t, sample)
				if present != recorded || (recorded && (prefix == nil || prefix.ObserverDuration != 2*time.Nanosecond)) || len(sample.Spans) != 0 {
					t.Fatal("Finish mixed or lost the winning fixed prefix")
				}
			} else if recorded {
				assertReceiveBridgeResponse(t, sample, points, rpctrace.ResultOK)
			} else if len(sample.Spans) != 0 {
				t.Fatal("Finish published a partial response group")
			}
		})
	}
}
