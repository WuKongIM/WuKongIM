package workload

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	benchwkproto "github.com/WuKongIM/WuKongIM/internal/bench/wkproto"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestAutoRecvAckWaitDrainedRequiresStableTransportAndMatchingZero(t *testing.T) {
	raw := newDrainProofClient()
	raw.queue = benchwkproto.QueueSnapshot{
		InnerRecvDepth: 1, InnerRecvCapacity: 4,
		RecvCapacity: 4, SendackCapacity: 4, ErrorCapacity: 4,
		RecvDepth: 1, AdapterDepth: 1, AdapterCapacity: 12,
		PublicationCapacity: 4,
	}
	wrapped := WrapPersonClientsForConcurrentReads(map[string]PersonClient{"u1": raw})["u1"]
	handle := StartAutoRecvAckHandleWithOptions(map[string]PersonClient{"u1": wrapped}, AutoRecvAckOptions{BufferRecvFrames: false})
	defer func() {
		handle.Cancel()
		handle.Wait()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan struct {
		snapshot model.ReceiveDrainSnapshot
		err      error
	}, 1)
	go func() {
		snapshot, err := handle.WaitDrained(ctx)
		done <- struct {
			snapshot model.ReceiveDrainSnapshot
			err      error
		}{snapshot: snapshot, err: err}
	}()

	select {
	case result := <-done:
		t.Fatalf("WaitDrained returned before queue convergence: snapshot=%+v err=%v", result.snapshot, result.err)
	case <-time.After(20 * time.Millisecond):
	}
	raw.setQueue(benchwkproto.QueueSnapshot{
		InnerRecvCapacity: 4,
		RecvCapacity:      4, SendackCapacity: 4, ErrorCapacity: 4,
		AdapterCapacity: 12, PublicationCapacity: 4,
	})

	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("WaitDrained() error = %v, snapshot = %+v", result.err, result.snapshot)
		}
		if !result.snapshot.TerminalProofComplete() {
			t.Fatalf("terminal receive proof incomplete: %+v", result.snapshot)
		}
	case <-time.After(time.Second):
		t.Fatal("WaitDrained did not observe stable zero queues")
	}
}

func TestAutoRecvAckWaitDrainedAcceptsTerminalProofAtContextBoundary(t *testing.T) {
	clock := newManualReceiveDrainClock(time.Unix(300, 0))
	client := newMutableReceiveDrainClient()
	handle := &AutoRecvAckHandle{
		clients:         []receiveDrainClient{client},
		receiveDrainNow: clock.Now,
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan struct {
		snapshot model.ReceiveDrainSnapshot
		err      error
	}, 1)
	go func() {
		snapshot, err := handle.WaitDrained(ctx)
		result <- struct {
			snapshot model.ReceiveDrainSnapshot
			err      error
		}{snapshot: snapshot, err: err}
	}()

	clock.waitFirstRead(t)
	clock.Advance(receiveDrainObservationInterval(1))
	cancel()

	select {
	case got := <-result:
		if got.err != nil || !got.snapshot.TerminalProofComplete() {
			t.Fatalf("WaitDrained() snapshot = %+v, error = %v, want completed proof", got.snapshot, got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("WaitDrained did not return at context boundary")
	}
}

func TestAutoRecvAckWaitDrainedReturnsRecvACKFailureEvidence(t *testing.T) {
	raw := newDrainProofClient()
	raw.recvAckErr = errors.New("write failed")
	raw.pushFrame(&frame.RecvPacket{MessageID: 42, MessageSeq: 7, ClientMsgNo: "message"})
	wrapped := WrapPersonClientsForConcurrentReads(map[string]PersonClient{"u1": raw})["u1"]
	handle := StartAutoRecvAckHandleWithOptions(map[string]PersonClient{"u1": wrapped}, AutoRecvAckOptions{BufferRecvFrames: false})
	defer func() {
		handle.Cancel()
		handle.Wait()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	snapshot, err := handle.WaitDrained(ctx)
	if err == nil {
		t.Fatalf("WaitDrained() error = nil, snapshot = %+v", snapshot)
	}
	if snapshot.RecvACKFailures != 1 || snapshot.RecvACKSuccesses != 0 || snapshot.DrainComplete || snapshot.TerminalProofComplete() {
		t.Fatalf("recvack failure snapshot = %+v", snapshot)
	}
}

func TestAutoRecvAckSnapshotCountsSuccessfulProtocolRecvACKOnce(t *testing.T) {
	raw := newDrainProofClient()
	raw.pushFrame(&frame.RecvPacket{MessageID: 43, MessageSeq: 8, ClientMsgNo: "message-success"})
	wrapped := WrapPersonClientsForConcurrentReads(map[string]PersonClient{"u1": raw})["u1"]
	handle := StartAutoRecvAckHandleWithOptions(map[string]PersonClient{"u1": wrapped}, AutoRecvAckOptions{BufferRecvFrames: true})
	defer func() {
		handle.Cancel()
		handle.Wait()
	}()

	select {
	case <-raw.recvAcked:
	case <-time.After(time.Second):
		t.Fatal("background RECVACK did not complete")
	}
	if snapshot := handle.Snapshot(); snapshot.RecvACKSuccesses != 1 {
		t.Fatalf("successful RECVACKs = %d, want 1", snapshot.RecvACKSuccesses)
	}

	if err := wrapped.RecvAck(context.Background(), 43, 8); err != nil {
		t.Fatalf("explicit RecvAck() after auto-ack error = %v", err)
	}
	if snapshot := handle.Snapshot(); snapshot.RecvACKSuccesses != 1 {
		t.Fatalf("successful RECVACKs after explicit no-op = %d, want 1", snapshot.RecvACKSuccesses)
	}
}

func TestRecvACKSuccessCounterFailsClosedOnClientOverflow(t *testing.T) {
	raw := newDrainProofClient()
	wrapped := WrapPersonClientsForConcurrentReads(map[string]PersonClient{"u1": raw})["u1"].(*matchingPersonClient)
	wrapped.recvACKSuccesses = ^uint64(0)
	handle := &AutoRecvAckHandle{clients: []receiveDrainClient{wrapped}}

	if err := wrapped.RecvAck(context.Background(), 44, 9); err != nil {
		t.Fatalf("RecvAck() error = %v", err)
	}
	snapshot := handle.Snapshot()
	if snapshot.RecvACKSuccesses != ^uint64(0) || snapshot.EvidenceComplete {
		t.Fatalf("client-overflow snapshot = %+v, want saturated incomplete evidence", snapshot)
	}
}

func TestAutoRecvAckWaitDrainedRejectsInconsistentQueueCapacityEvidence(t *testing.T) {
	raw := newDrainProofClient()
	raw.queue.AdapterCapacity = 11
	wrapped := WrapPersonClientsForConcurrentReads(map[string]PersonClient{"u1": raw})["u1"]
	handle := StartAutoRecvAckHandleWithOptions(map[string]PersonClient{"u1": wrapped}, AutoRecvAckOptions{BufferRecvFrames: false})
	defer func() {
		handle.Cancel()
		handle.Wait()
	}()

	snapshot, err := handle.WaitDrained(context.Background())
	if err == nil || snapshot.EvidenceComplete || snapshot.TerminalProofComplete() {
		t.Fatalf("WaitDrained() snapshot = %+v, error = %v, want incomplete queue evidence", snapshot, err)
	}
}

func TestAutoRecvAckWaitDrainedRequiresTransportHandoffZero(t *testing.T) {
	raw := newDrainProofClient()
	raw.queue.AdapterHandoffs = 1
	wrapped := WrapPersonClientsForConcurrentReads(map[string]PersonClient{"u1": raw})["u1"]
	handle := StartAutoRecvAckHandleWithOptions(map[string]PersonClient{"u1": wrapped}, AutoRecvAckOptions{BufferRecvFrames: false})
	defer func() {
		handle.Cancel()
		handle.Wait()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := handle.WaitDrained(ctx)
		done <- err
	}()

	select {
	case err := <-done:
		t.Fatalf("WaitDrained returned with adapter handoff: %v", err)
	case <-time.After(40 * time.Millisecond):
	}
	queue := raw.QueueSnapshot()
	queue.AdapterHandoffs = 0
	raw.setQueue(queue)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WaitDrained() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("WaitDrained did not complete after handoff release")
	}
}

func TestAutoRecvAckSnapshotInvalidatesProofAfterLateReceive(t *testing.T) {
	raw := newDrainProofClient()
	wrapped := WrapPersonClientsForConcurrentReads(map[string]PersonClient{"u1": raw})["u1"]
	handle := StartAutoRecvAckHandleWithOptions(map[string]PersonClient{"u1": wrapped}, AutoRecvAckOptions{BufferRecvFrames: false})
	defer func() {
		handle.Cancel()
		handle.Wait()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stable, err := handle.WaitDrained(ctx)
	if err != nil || !stable.TerminalProofComplete() {
		t.Fatalf("WaitDrained() snapshot = %+v, error = %v", stable, err)
	}
	raw.pushFrame(&frame.RecvPacket{MessageID: 43, MessageSeq: 8, ClientMsgNo: "late-message"})
	select {
	case <-raw.recvAcked:
	case <-ctx.Done():
		t.Fatal("late receive was not acknowledged")
	}

	latest := handle.Snapshot()
	if latest.TerminalProofComplete() || latest.ReceiveFramesObserved <= stable.ReceiveFramesObserved {
		t.Fatalf("late receive did not invalidate stable proof: before=%+v after=%+v", stable, latest)
	}
}

func TestAutoRecvAckSnapshotRebuildsProofOnlyAfterSeparatedMatchingZeroCuts(t *testing.T) {
	clock := newManualReceiveDrainClock(time.Unix(100, 0))
	client := newMutableReceiveDrainClient()
	handle := &AutoRecvAckHandle{
		clients:         []receiveDrainClient{client},
		receiveDrainNow: clock.Now,
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan struct {
		snapshot model.ReceiveDrainSnapshot
		err      error
	}, 1)
	go func() {
		snapshot, err := handle.WaitDrained(ctx)
		done <- struct {
			snapshot model.ReceiveDrainSnapshot
			err      error
		}{snapshot: snapshot, err: err}
	}()
	clock.waitFirstRead(t)
	interval := receiveDrainObservationInterval(1)
	clock.Advance(interval)
	initial := <-done
	if initial.err != nil || !initial.snapshot.TerminalProofComplete() {
		t.Fatalf("WaitDrained() snapshot = %+v, error = %v", initial.snapshot, initial.err)
	}

	late := completeZeroReceiveDrainPart()
	late.InnerRecvDepth = 1
	late.ReceiveFramesObserved = 1
	client.SetSnapshot(late)
	if snapshot := handle.Snapshot(); snapshot.TerminalProofComplete() {
		t.Fatalf("late pending cut retained proof: %+v", snapshot)
	}

	afterLate := completeZeroReceiveDrainPart()
	afterLate.ReceiveFramesObserved = 1
	client.SetSnapshot(afterLate)
	firstZero := handle.Snapshot()
	if firstZero.TerminalProofComplete() || firstZero.StableZeroObservations != 1 {
		t.Fatalf("first recovered zero cut = %+v, want one incomplete observation", firstZero)
	}
	if immediate := handle.Snapshot(); immediate.TerminalProofComplete() || immediate.StableZeroObservations != 1 {
		t.Fatalf("immediate status call forged proof: %+v", immediate)
	}
	clock.Advance(interval - time.Nanosecond)
	if early := handle.Snapshot(); early.TerminalProofComplete() || early.StableZeroObservations != 1 {
		t.Fatalf("early zero cut forged proof: %+v", early)
	}
	clock.Advance(time.Nanosecond)
	rebuilt := handle.Snapshot()
	if !rebuilt.TerminalProofComplete() {
		t.Fatalf("separated matching zero cuts did not rebuild proof: %+v", rebuilt)
	}
}

func TestAutoRecvAckSnapshotRebuildsProofAfterRecvACKSuccessProgress(t *testing.T) {
	clock := newManualReceiveDrainClock(time.Unix(150, 0))
	client := newMutableReceiveDrainClient()
	handle := &AutoRecvAckHandle{
		clients:         []receiveDrainClient{client},
		receiveDrainNow: clock.Now,
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan struct {
		snapshot model.ReceiveDrainSnapshot
		err      error
	}, 1)
	go func() {
		snapshot, err := handle.WaitDrained(ctx)
		done <- struct {
			snapshot model.ReceiveDrainSnapshot
			err      error
		}{snapshot: snapshot, err: err}
	}()
	clock.waitFirstRead(t)
	interval := receiveDrainObservationInterval(1)
	clock.Advance(interval)
	initial := <-done
	if initial.err != nil || !initial.snapshot.TerminalProofComplete() {
		t.Fatalf("WaitDrained() snapshot = %+v, error = %v", initial.snapshot, initial.err)
	}

	progressed := completeZeroReceiveDrainPart()
	progressed.RecvACKSuccesses = 1
	client.SetSnapshot(progressed)
	first := handle.Snapshot()
	if first.TerminalProofComplete() || first.StableZeroObservations != 1 {
		t.Fatalf("new RECVACK progress retained stable proof: %+v", first)
	}
	clock.Advance(interval)
	second := handle.Snapshot()
	if !second.TerminalProofComplete() || second.RecvACKSuccesses != 1 {
		t.Fatalf("separated unchanged RECVACK cut did not rebuild proof: %+v", second)
	}
}

func TestAutoRecvAckSnapshotAggregatesRecvACKSuccessesAndFailsClosedOnOverflow(t *testing.T) {
	first := newMutableReceiveDrainClient()
	second := newMutableReceiveDrainClient()
	firstSnapshot := completeZeroReceiveDrainPart()
	firstSnapshot.RecvACKSuccesses = 2
	first.SetSnapshot(firstSnapshot)
	secondSnapshot := completeZeroReceiveDrainPart()
	secondSnapshot.RecvACKSuccesses = 3
	second.SetSnapshot(secondSnapshot)
	handle := &AutoRecvAckHandle{clients: []receiveDrainClient{first, second}}

	snapshot := handle.Snapshot()
	if !snapshot.EvidenceComplete || snapshot.RecvACKSuccesses != 5 {
		t.Fatalf("aggregated RECVACK successes = %+v, want complete total 5", snapshot)
	}

	firstSnapshot.RecvACKSuccesses = ^uint64(0)
	first.SetSnapshot(firstSnapshot)
	secondSnapshot.RecvACKSuccesses = 1
	second.SetSnapshot(secondSnapshot)
	overflow := handle.Snapshot()
	if overflow.EvidenceComplete || overflow.RecvACKSuccesses != ^uint64(0) {
		t.Fatalf("overflowed RECVACK successes = %+v, want saturated incomplete evidence", overflow)
	}
}

func TestAutoRecvAckSnapshotNeverRecoversAfterMissingOrFailedEvidence(t *testing.T) {
	for _, test := range []struct {
		name   string
		broken model.ReceiveDrainSnapshot
	}{
		{name: "missing", broken: model.ReceiveDrainSnapshot{Required: true, ClientCount: 1, ActiveDrains: 1}},
		{name: "read failure", broken: func() model.ReceiveDrainSnapshot {
			snapshot := completeZeroReceiveDrainPart()
			snapshot.ReadFailures = 1
			return snapshot
		}()},
	} {
		t.Run(test.name, func(t *testing.T) {
			clock := newManualReceiveDrainClock(time.Unix(200, 0))
			client := newMutableReceiveDrainClient()
			handle := &AutoRecvAckHandle{
				clients:                  []receiveDrainClient{client},
				receiveDrainNow:          clock.Now,
				receiveDrainProofEnabled: true,
			}
			if first := handle.Snapshot(); first.StableZeroObservations != 1 {
				t.Fatalf("first zero cut = %+v", first)
			}
			client.SetSnapshot(test.broken)
			if broken := handle.Snapshot(); broken.TerminalProofComplete() {
				t.Fatalf("broken evidence proved terminal: %+v", broken)
			}
			client.SetSnapshot(completeZeroReceiveDrainPart())
			clock.Advance(10 * receiveDrainObservationInterval(1))
			if recovered := handle.Snapshot(); recovered.EvidenceComplete || recovered.TerminalProofComplete() {
				t.Fatalf("permanent evidence failure recovered: %+v", recovered)
			}
		})
	}
}

type manualReceiveDrainClock struct {
	mu        sync.Mutex
	now       time.Time
	firstRead chan struct{}
	readOnce  sync.Once
}

func newManualReceiveDrainClock(now time.Time) *manualReceiveDrainClock {
	return &manualReceiveDrainClock{now: now, firstRead: make(chan struct{})}
}

func (c *manualReceiveDrainClock) Now() time.Time {
	c.mu.Lock()
	now := c.now
	c.mu.Unlock()
	c.readOnce.Do(func() { close(c.firstRead) })
	return now
}

func (c *manualReceiveDrainClock) waitFirstRead(t *testing.T) {
	t.Helper()
	select {
	case <-c.firstRead:
	case <-time.After(time.Second):
		t.Fatal("receive drain clock was not read")
	}
}

func (c *manualReceiveDrainClock) Advance(delta time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(delta)
	c.mu.Unlock()
}

type mutableReceiveDrainClient struct {
	mu       sync.Mutex
	snapshot model.ReceiveDrainSnapshot
}

func newMutableReceiveDrainClient() *mutableReceiveDrainClient {
	return &mutableReceiveDrainClient{
		snapshot: completeZeroReceiveDrainPart(),
	}
}

func completeZeroReceiveDrainPart() model.ReceiveDrainSnapshot {
	return model.ReceiveDrainSnapshot{
		Required:             true,
		EvidenceComplete:     true,
		ClientCount:          1,
		ActiveDrains:         1,
		QueueSnapshotClients: 1,
	}
}

func (*mutableReceiveDrainClient) startAutoRecvAckWithOptions(context.Context, AutoRecvAckOptions) <-chan struct{} {
	return nil
}

func (*mutableReceiveDrainClient) beginReceiveDrain() {}

func (c *mutableReceiveDrainClient) receiveDrainSnapshot() model.ReceiveDrainSnapshot {
	c.mu.Lock()
	snapshot := c.snapshot
	c.mu.Unlock()
	return snapshot
}

func (c *mutableReceiveDrainClient) SetSnapshot(snapshot model.ReceiveDrainSnapshot) {
	c.mu.Lock()
	c.snapshot = snapshot
	c.mu.Unlock()
}

func TestAutoRecvAckWaitDrainedPreservesNonIdleReadFailure(t *testing.T) {
	raw := newDrainProofClient()
	wrapped := WrapPersonClientsForConcurrentReads(map[string]PersonClient{"u1": raw})["u1"]
	handle := StartAutoRecvAckHandleWithOptions(map[string]PersonClient{"u1": wrapped}, AutoRecvAckOptions{BufferRecvFrames: false})
	defer handle.Cancel()

	raw.pushReadError(errors.New("remote reader failed"))
	handle.Wait()
	snapshot, err := handle.WaitDrained(context.Background())
	if err == nil || snapshot.ReadFailures != 1 || snapshot.TerminalProofComplete() {
		t.Fatalf("WaitDrained() snapshot = %+v, error = %v, want permanent read failure", snapshot, err)
	}
}

func TestAutoRecvAckDrainAndStopReturnsLiveProofThenJoinedSnapshot(t *testing.T) {
	raw := newDrainProofClient()
	wrapped := WrapPersonClientsForConcurrentReads(map[string]PersonClient{"u1": raw})["u1"]
	handle := StartAutoRecvAckHandleWithOptions(map[string]PersonClient{"u1": wrapped}, AutoRecvAckOptions{BufferRecvFrames: false})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	drained, stopped, err := handle.DrainAndStop(ctx)

	if err != nil || !drained.TerminalProofComplete() {
		t.Fatalf("DrainAndStop() drained = %+v, error = %v", drained, err)
	}
	if !stopped.EvidenceComplete || stopped.ActiveDrains != 0 || stopped.ReadFailures != 0 || stopped.RecvACKFailures != 0 {
		t.Fatalf("DrainAndStop() stopped = %+v, want joined failure-free snapshot", stopped)
	}
	select {
	case <-handle.done:
	default:
		t.Fatal("DrainAndStop returned before receive goroutines joined")
	}
}

func TestAutoRecvAckDrainAndStopWaitsForRecvACKBeforePlannedCancel(t *testing.T) {
	raw := newBlockingRecvACKDrainClient()
	wrapped := WrapPersonClientsForConcurrentReads(map[string]PersonClient{"u1": raw})["u1"]
	handle := StartAutoRecvAckHandleWithOptions(map[string]PersonClient{"u1": wrapped}, AutoRecvAckOptions{BufferRecvFrames: false})
	raw.pushFrame(&frame.RecvPacket{MessageID: 71, MessageSeq: 4})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan struct {
		drained model.ReceiveDrainSnapshot
		stopped model.ReceiveDrainSnapshot
		err     error
	}, 1)
	go func() {
		drained, stopped, err := handle.DrainAndStop(ctx)
		done <- struct {
			drained model.ReceiveDrainSnapshot
			stopped model.ReceiveDrainSnapshot
			err     error
		}{drained: drained, stopped: stopped, err: err}
	}()

	select {
	case <-raw.ackStarted:
	case <-time.After(time.Second):
		t.Fatal("RECVACK did not start")
	}
	select {
	case result := <-done:
		t.Fatalf("DrainAndStop returned before RECVACK completed: %+v", result)
	case <-time.After(40 * time.Millisecond):
	}
	close(raw.ackRelease)
	select {
	case result := <-done:
		if result.err != nil || !result.drained.TerminalProofComplete() || result.stopped.RecvACKFailures != 0 {
			t.Fatalf("DrainAndStop result = %+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("DrainAndStop did not finish after RECVACK")
	}
}

func TestAutoRecvAckDrainAndStopTimeoutCancelsAndJoinsWithoutSyntheticFailure(t *testing.T) {
	raw := newBlockingRecvACKDrainClient()
	wrapped := WrapPersonClientsForConcurrentReads(map[string]PersonClient{"u1": raw})["u1"]
	handle := StartAutoRecvAckHandleWithOptions(map[string]PersonClient{"u1": wrapped}, AutoRecvAckOptions{BufferRecvFrames: false})
	raw.pushFrame(&frame.RecvPacket{MessageID: 72, MessageSeq: 5})
	select {
	case <-raw.ackStarted:
	case <-time.After(time.Second):
		t.Fatal("RECVACK did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	drained, stopped, err := handle.DrainAndStop(ctx)

	if !errors.Is(err, context.DeadlineExceeded) || drained.DrainComplete {
		t.Fatalf("DrainAndStop() drained = %+v, error = %v, want timeout", drained, err)
	}
	if stopped.ActiveDrains != 0 || stopped.RecvACKFailures != 0 || stopped.ReadFailures != 0 {
		t.Fatalf("planned cancellation created failure or orphan: %+v", stopped)
	}
	select {
	case <-handle.done:
	default:
		t.Fatal("timed-out DrainAndStop returned before join")
	}
}

func TestForegroundRecvACKDeadlineRemainsPermanentFailure(t *testing.T) {
	raw := newBlockingRecvACKDrainClient()
	raw.pushFrame(&frame.RecvPacket{MessageID: 74, MessageSeq: 7})
	wrapped := WrapPersonClientsForConcurrentReads(map[string]PersonClient{"u1": raw})["u1"].(*matchingPersonClient)
	wrapped.autoRecvAck = true
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	_, err := wrapped.readFrameMatching(ctx, func(frame.Frame) bool { return true })

	if err == nil {
		t.Fatal("readFrameMatching() error = nil, want RECVACK deadline")
	}
	if snapshot := wrapped.receiveDrainSnapshot(); snapshot.RecvACKFailures != 1 {
		t.Fatalf("foreground RECVACK deadline snapshot = %+v, want permanent failure", snapshot)
	}
}

func TestAutoRecvAckDrainAndStopPermanentlyCarriesIncompletePreStopEvidence(t *testing.T) {
	raw := &flippingEvidenceDrainClient{drainProofClient: newDrainProofClient()}
	wrapped := WrapPersonClientsForConcurrentReads(map[string]PersonClient{"u1": raw})["u1"]
	handle := StartAutoRecvAckHandleWithOptions(map[string]PersonClient{"u1": wrapped}, AutoRecvAckOptions{BufferRecvFrames: false})

	drained, stopped, err := handle.DrainAndStop(context.Background())

	if err == nil || drained.EvidenceComplete || stopped.EvidenceComplete {
		t.Fatalf("DrainAndStop() drained = %+v stopped = %+v error = %v, want permanent incomplete evidence", drained, stopped, err)
	}
}

func TestAutoRecvAckDrainAndStopRejectsDeliveryCrossingStopBoundary(t *testing.T) {
	raw := &cancelDeliveryDrainClient{drainProofClient: newDrainProofClient()}
	wrapped := WrapPersonClientsForConcurrentReads(map[string]PersonClient{"u1": raw})["u1"]
	handle := StartAutoRecvAckHandleWithOptions(map[string]PersonClient{"u1": wrapped}, AutoRecvAckOptions{BufferRecvFrames: false})

	drained, stopped, err := handle.DrainAndStop(context.Background())

	if err == nil || !drained.TerminalProofComplete() || stopped.EvidenceComplete {
		t.Fatalf("DrainAndStop() drained = %+v stopped = %+v error = %v, want invalidated stop boundary", drained, stopped, err)
	}
	if stopped.ReceiveFramesObserved != drained.ReceiveFramesObserved+1 {
		t.Fatalf("stop-boundary receive progress = before:%d after:%d, want one late frame", drained.ReceiveFramesObserved, stopped.ReceiveFramesObserved)
	}
}

func TestAutoRecvAckDrainAndStopRejectsRecvACKSuccessCrossingStopBoundary(t *testing.T) {
	client := newMutableReceiveDrainClient()
	done := make(chan struct{})
	handle := &AutoRecvAckHandle{
		clients: []receiveDrainClient{client},
		done:    done,
		cancel: func() {
			after := completeZeroReceiveDrainPart()
			after.ActiveDrains = 0
			after.RecvACKSuccesses = 1
			client.SetSnapshot(after)
			close(done)
		},
	}

	drained, stopped, err := handle.DrainAndStop(context.Background())

	if err == nil || !drained.TerminalProofComplete() || stopped.EvidenceComplete {
		t.Fatalf("DrainAndStop() drained = %+v stopped = %+v error = %v, want changed RECVACK progress rejected", drained, stopped, err)
	}
	if stopped.RecvACKSuccesses != drained.RecvACKSuccesses+1 {
		t.Fatalf("stop-boundary RECVACK progress = before:%d after:%d, want one late success", drained.RecvACKSuccesses, stopped.RecvACKSuccesses)
	}
}

func TestAutoRecvAckDrainAndStopFenceIncludesPreFenceDeliveryInFreshProof(t *testing.T) {
	raw := newDrainProofClient()
	wrapped := WrapPersonClientsForConcurrentReads(map[string]PersonClient{"u1": raw})["u1"]
	handle := StartAutoRecvAckHandleWithOptions(map[string]PersonClient{"u1": wrapped}, AutoRecvAckOptions{BufferRecvFrames: false})

	drained, stopped, err := handle.DrainAndStopWithFence(context.Background(), func() error {
		raw.frames <- &frame.RecvPacket{MessageID: 75, MessageSeq: 8}
		select {
		case <-raw.recvAcked:
			return nil
		case <-time.After(time.Second):
			return errors.New("receive reader did not observe fenced delivery")
		}
	})

	if err != nil || !drained.TerminalProofComplete() || !stopped.EvidenceComplete {
		t.Fatalf("DrainAndStopWithFence() drained = %+v stopped = %+v error = %v, want stable post-fence proof", drained, stopped, err)
	}
	if drained.ReceiveFramesObserved != 1 || stopped.ReceiveFramesObserved != 1 {
		t.Fatalf("fenced receive progress = drained:%d stopped:%d, want exact included frame", drained.ReceiveFramesObserved, stopped.ReceiveFramesObserved)
	}
}

type cancelDeliveryDrainClient struct {
	*drainProofClient
	delivered atomic.Bool
}

func (c *cancelDeliveryDrainClient) ReadFrame(ctx context.Context) (frame.Frame, error) {
	<-ctx.Done()
	if c.delivered.CompareAndSwap(false, true) {
		return &frame.RecvPacket{MessageID: 73, MessageSeq: 6}, nil
	}
	return nil, ctx.Err()
}

func (*cancelDeliveryDrainClient) RecvAck(context.Context, int64, uint64) error {
	return nil
}

type flippingEvidenceDrainClient struct {
	*drainProofClient
	observations atomic.Uint64
}

func (c *flippingEvidenceDrainClient) QueueSnapshot() benchwkproto.QueueSnapshot {
	if c.observations.Add(1) == 1 {
		return benchwkproto.QueueSnapshot{}
	}
	return c.drainProofClient.QueueSnapshot()
}

type blockingRecvACKDrainClient struct {
	*drainProofClient
	ackStarted chan struct{}
	ackRelease chan struct{}
	startOnce  sync.Once
}

func newBlockingRecvACKDrainClient() *blockingRecvACKDrainClient {
	return &blockingRecvACKDrainClient{
		drainProofClient: newDrainProofClient(),
		ackStarted:       make(chan struct{}),
		ackRelease:       make(chan struct{}),
	}
}

func (c *blockingRecvACKDrainClient) RecvAck(ctx context.Context, _ int64, _ uint64) error {
	c.startOnce.Do(func() { close(c.ackStarted) })
	select {
	case <-c.ackRelease:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestAutoRecvAckWaitDrainedBoundsSnapshotRateForLargeClientSet(t *testing.T) {
	const clientCount = 2500
	var observations atomic.Uint64
	clients := make([]receiveDrainClient, clientCount)
	for idx := range clients {
		clients[idx] = &countingPendingDrainClient{observations: &observations}
	}
	handle := &AutoRecvAckHandle{clients: clients}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	snapshot, err := handle.WaitDrained(ctx)

	if !errors.Is(err, context.DeadlineExceeded) || snapshot.InnerRecvDepth != clientCount {
		t.Fatalf("WaitDrained() snapshot = %+v, error = %v, want pending timeout", snapshot, err)
	}
	if got := observations.Load(); got > 3*clientCount {
		t.Fatalf("receive drain observations = %d, want at most three bounded full scans", got)
	}
}

func TestAutoRecvAckWaitDrainedCompletesLargeEmptyClientSetWithinOneSecond(t *testing.T) {
	const clientCount = 2500
	var observations atomic.Uint64
	clients := make([]receiveDrainClient, clientCount)
	for idx := range clients {
		clients[idx] = &countingZeroDrainClient{observations: &observations}
	}
	handle := &AutoRecvAckHandle{clients: clients}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started := time.Now()

	snapshot, err := handle.WaitDrained(ctx)

	if err != nil || !snapshot.TerminalProofComplete() {
		t.Fatalf("WaitDrained() snapshot = %+v, error = %v", snapshot, err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("large empty receive drain elapsed = %s, want <1s", elapsed)
	}
	if got := observations.Load(); got > 2*clientCount {
		t.Fatalf("receive drain observations = %d, want at most two full scans", got)
	}
}

func TestAutoRecvAckRegistersMatchingOwnershipBeforeReleasingAdapterLease(t *testing.T) {
	raw := newLeasedDrainProofClient()
	wrapped := WrapPersonClientsForConcurrentReads(map[string]PersonClient{"u1": raw})["u1"].(*matchingPersonClient)
	raw.onRelease = func() bool {
		wrapped.mu.Lock()
		defer wrapped.mu.Unlock()
		return wrapped.readFramesInFlight == 1
	}
	handle := StartAutoRecvAckHandleWithOptions(map[string]PersonClient{"u1": wrapped}, AutoRecvAckOptions{BufferRecvFrames: false})
	defer func() {
		handle.Cancel()
		handle.Wait()
	}()
	raw.pushFrame(&frame.RecvPacket{MessageID: 19, MessageSeq: 3})

	select {
	case registered := <-raw.released:
		if !registered {
			t.Fatal("adapter lease released before matching in-flight ownership")
		}
	case <-time.After(time.Second):
		t.Fatal("adapter lease was not released")
	}
}

func TestAutoRecvAckSnapshotSamplesQueueBeforeDownstreamMatchingState(t *testing.T) {
	raw := &queueHandoffDrainClient{drainProofClient: newDrainProofClient()}
	wrapped := WrapPersonClientsForConcurrentReads(map[string]PersonClient{"u1": raw})["u1"].(*matchingPersonClient)
	wrapped.autoRecvAck = true
	wrapped.autoRecvAckDone = make(chan struct{})
	raw.onSnapshot = func() {
		wrapped.mu.Lock()
		wrapped.readFramesInFlight = 1
		wrapped.mu.Unlock()
	}
	handle := &AutoRecvAckHandle{clients: []receiveDrainClient{wrapped}}

	snapshot := handle.Snapshot()

	if snapshot.ReadFramesInFlight != 1 || !snapshot.PendingWork() {
		t.Fatalf("Snapshot() = %+v, want downstream handoff ownership", snapshot)
	}
}

type queueHandoffDrainClient struct {
	*drainProofClient
	onSnapshot func()
	once       sync.Once
}

func (c *queueHandoffDrainClient) QueueSnapshot() benchwkproto.QueueSnapshot {
	c.once.Do(c.onSnapshot)
	return c.drainProofClient.QueueSnapshot()
}

type leasedDrainProofClient struct {
	*drainProofClient
	onRelease func() bool
	released  chan bool
}

func newLeasedDrainProofClient() *leasedDrainProofClient {
	return &leasedDrainProofClient{
		drainProofClient: newDrainProofClient(),
		released:         make(chan bool, 1),
	}
}

func (c *leasedDrainProofClient) ReadFrameWithLease(ctx context.Context) (frame.Frame, benchwkproto.FrameLease, error) {
	f, err := c.ReadFrame(ctx)
	if err != nil {
		return nil, nil, err
	}
	return f, &testFrameLease{release: func() {
		c.released <- c.onRelease != nil && c.onRelease()
	}}, nil
}

type testFrameLease struct {
	once    sync.Once
	release func()
}

func (l *testFrameLease) Release() {
	l.once.Do(l.release)
}

type countingPendingDrainClient struct {
	observations *atomic.Uint64
}

type countingZeroDrainClient struct {
	observations *atomic.Uint64
}

func (*countingZeroDrainClient) startAutoRecvAckWithOptions(context.Context, AutoRecvAckOptions) <-chan struct{} {
	return nil
}

func (*countingZeroDrainClient) beginReceiveDrain() {}

func (c *countingZeroDrainClient) receiveDrainSnapshot() model.ReceiveDrainSnapshot {
	c.observations.Add(1)
	return model.ReceiveDrainSnapshot{
		Required:             true,
		EvidenceComplete:     true,
		ClientCount:          1,
		ActiveDrains:         1,
		QueueSnapshotClients: 1,
	}
}

func (*countingPendingDrainClient) startAutoRecvAckWithOptions(context.Context, AutoRecvAckOptions) <-chan struct{} {
	return nil
}

func (*countingPendingDrainClient) beginReceiveDrain() {}

func (c *countingPendingDrainClient) receiveDrainSnapshot() model.ReceiveDrainSnapshot {
	c.observations.Add(1)
	return model.ReceiveDrainSnapshot{
		Required:             true,
		EvidenceComplete:     true,
		ClientCount:          1,
		ActiveDrains:         1,
		QueueSnapshotClients: 1,
		InnerRecvDepth:       1,
	}
}

type drainProofClient struct {
	mu         sync.Mutex
	queue      benchwkproto.QueueSnapshot
	frames     chan frame.Frame
	readErrors chan error
	recvAcked  chan struct{}
	recvAckErr error
}

func newDrainProofClient() *drainProofClient {
	return &drainProofClient{
		frames:     make(chan frame.Frame, 4),
		readErrors: make(chan error, 1),
		recvAcked:  make(chan struct{}, 4),
		queue: benchwkproto.QueueSnapshot{
			InnerRecvCapacity: 4,
			RecvCapacity:      4, SendackCapacity: 4, ErrorCapacity: 4,
			AdapterCapacity: 12, PublicationCapacity: 4,
		},
	}
}

func (c *drainProofClient) Connect(context.Context, string, string) error { return nil }
func (c *drainProofClient) Send(context.Context, *frame.SendPacket) error { return nil }
func (c *drainProofClient) Close() error                                  { return nil }

func (c *drainProofClient) ReadFrame(ctx context.Context) (frame.Frame, error) {
	select {
	case got := <-c.frames:
		return got, nil
	case err := <-c.readErrors:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *drainProofClient) RecvAck(context.Context, int64, uint64) error {
	c.mu.Lock()
	err := c.recvAckErr
	c.mu.Unlock()
	select {
	case c.recvAcked <- struct{}{}:
	default:
	}
	return err
}

func (c *drainProofClient) QueueSnapshot() benchwkproto.QueueSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.queue
}

func (c *drainProofClient) setQueue(snapshot benchwkproto.QueueSnapshot) {
	c.mu.Lock()
	c.queue = snapshot
	c.mu.Unlock()
}

func (c *drainProofClient) pushFrame(got frame.Frame) {
	select {
	case c.frames <- got:
	default:
		panic(io.ErrShortBuffer)
	}
}

func (c *drainProofClient) pushReadError(err error) {
	select {
	case c.readErrors <- err:
	default:
		panic(io.ErrShortBuffer)
	}
}
