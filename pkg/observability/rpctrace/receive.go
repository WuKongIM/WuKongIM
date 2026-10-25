package rpctrace

import "time"

// ReceivePrefix measures a verified inbound frame before the original server
// trace starts. ObserverDuration is a subset of Duration, not an added phase.
// Initial receive identity validation precedes the measured boundary; the
// server's existing dispatch-time revalidation remains inside Duration.
type ReceivePrefix struct {
	Duration         time.Duration `json:"duration"`
	ObserverDuration time.Duration `json:"observer_duration"`
}

// RecordReceivePrefix retains one same-clock prefix without moving Trace.At or
// changing existing span offsets. Missing or rejected evidence stays unknown.
func (t *Trace) RecordReceivePrefix(frameReady, observerDone time.Time) bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || t.unbound || t.side != SideServer || t.hasReceivePrefix ||
		frameReady.IsZero() || observerDone.IsZero() || observerDone.Before(frameReady) || t.at.Before(observerDone) {
		return false
	}
	t.receivePrefix = ReceivePrefix{Duration: t.at.Sub(frameReady), ObserverDuration: observerDone.Sub(frameReady)}
	t.hasReceivePrefix = true
	return true
}

// RecordResponseReceive publishes a completed response's three local intervals
// atomically within the original span budget. They overlap response_wait and
// do not measure wire latency. All supplied times must come from this process.
func (t *Trace) RecordResponseReceive(frameReady, observerDone, notified, resumed time.Time, result Result) bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || t.unbound || t.side != SideClient || t.hasResponseReceive ||
		frameReady.IsZero() || observerDone.IsZero() || notified.IsZero() || resumed.IsZero() ||
		frameReady.Before(t.at) || observerDone.Before(frameReady) || notified.Before(observerDone) || resumed.Before(notified) || time.Now().Before(resumed) {
		return false
	}
	if t.count+3 > MaxSpans {
		t.dropped += 3
		return false
	}
	points := [4]time.Time{frameReady, observerDone, notified, resumed}
	stages := [3]Stage{StageResponseReceiveObserver, StageResponseDispatch, StageResponseResume}
	for i, stage := range stages {
		t.spans[t.count+i] = SpanSample{Stage: stage, Offset: points[i].Sub(t.at), Duration: points[i+1].Sub(points[i]), Result: normalizeResult(result)}
	}
	t.count += 3
	t.hasResponseReceive = true
	return true
}
