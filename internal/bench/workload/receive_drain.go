package workload

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	benchwkproto "github.com/WuKongIM/WuKongIM/internal/bench/wkproto"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
)

const (
	receiveDrainMinimumObservationInterval = 25 * time.Millisecond
	receiveDrainMaximumObservationInterval = 250 * time.Millisecond
	// receiveDrainObservationBudget bounds aggregate client snapshots during a
	// drain. At the supported 2,500-client local baseline this yields one full
	// cut every 250ms instead of continuously contending with socket readers.
	receiveDrainObservationBudget = 10_000
)

type receiveDrainClient interface {
	startAutoRecvAckWithOptions(context.Context, AutoRecvAckOptions) <-chan struct{}
	beginReceiveDrain()
	receiveDrainSnapshot() model.ReceiveDrainSnapshot
}

type receiveQueueSnapshotter interface {
	QueueSnapshot() benchwkproto.QueueSnapshot
}

// Snapshot returns one bounded live view of all receive drains owned by the
// handle. Stable proof may be rebuilt only from two matching healthy zero cuts
// separated by the handle's bounded observation interval.
func (h *AutoRecvAckHandle) Snapshot() model.ReceiveDrainSnapshot {
	if h == nil {
		return model.ReceiveDrainSnapshot{Required: true}
	}
	return h.qualifyReceiveDrainSnapshot(h.aggregateReceiveDrainSnapshot())
}

// WaitDrained switches matching clients to terminal receive mode and waits for
// two separated, complete, failure-free zero-work cuts. It never closes a
// connection or stops the background socket readers.
func (h *AutoRecvAckHandle) WaitDrained(ctx context.Context) (model.ReceiveDrainSnapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if h == nil {
		snapshot := model.ReceiveDrainSnapshot{Required: true}
		return snapshot, fmt.Errorf("receive drain unavailable")
	}
	h.enableReceiveDrainProof()

	observationInterval := receiveDrainObservationInterval(len(h.clients))
	for {
		snapshot := h.qualifyReceiveDrainSnapshot(h.aggregateReceiveDrainSnapshot())
		if !snapshot.EvidenceComplete {
			return snapshot, fmt.Errorf("receive drain evidence incomplete")
		}
		if !snapshot.FailureFree() {
			return snapshot, fmt.Errorf(
				"receive drain failed: read_failures=%d recvack_failures=%d",
				snapshot.ReadFailures,
				snapshot.RecvACKFailures,
			)
		}
		if snapshot.TerminalProofComplete() {
			return snapshot, nil
		}

		timer := time.NewTimer(observationInterval)
		select {
		case <-timer.C:
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			latest := h.qualifyReceiveDrainSnapshot(h.aggregateReceiveDrainSnapshot())
			if latest.TerminalProofComplete() {
				return latest, nil
			}
			return latest, ctx.Err()
		}
	}
}

func (h *AutoRecvAckHandle) qualifyReceiveDrainSnapshot(snapshot model.ReceiveDrainSnapshot) model.ReceiveDrainSnapshot {
	snapshot.DrainComplete = false
	snapshot.StableZeroObservations = 0

	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.receiveDrainProofEnabled {
		h.resetReceiveDrainZeroProgressLocked()
		h.last = snapshot
		return snapshot
	}
	if h.receiveDrainProofInvalid {
		snapshot.EvidenceComplete = false
		h.last = snapshot
		return snapshot
	}
	if !snapshot.EvidenceComplete {
		h.invalidateReceiveDrainProofLocked()
		h.last = snapshot
		return snapshot
	}
	if !snapshot.FailureFree() {
		h.invalidateReceiveDrainProofLocked()
		h.last = snapshot
		return snapshot
	}
	if !snapshot.ZeroCutComplete() {
		h.resetReceiveDrainZeroProgressLocked()
		h.last = snapshot
		return snapshot
	}

	now := time.Now()
	if h.receiveDrainNow != nil {
		now = h.receiveDrainNow()
	}
	identity := normalizedReceiveDrainZeroCut(snapshot)
	if h.receiveDrainStableZero == 0 || h.receiveDrainLastZero != identity {
		h.receiveDrainLastZero = identity
		h.receiveDrainLastZeroAt = now
		h.receiveDrainStableZero = 1
	} else if h.receiveDrainStableZero < model.ReceiveDrainStableZeroObservations &&
		!now.Before(h.receiveDrainLastZeroAt.Add(receiveDrainObservationInterval(len(h.clients)))) {
		h.receiveDrainStableZero++
	}
	snapshot.StableZeroObservations = h.receiveDrainStableZero
	snapshot.DrainComplete = h.receiveDrainStableZero >= model.ReceiveDrainStableZeroObservations
	h.last = snapshot
	return snapshot
}

func (h *AutoRecvAckHandle) enableReceiveDrainProof() {
	h.mu.Lock()
	h.receiveDrainProofEnabled = false
	h.resetReceiveDrainZeroProgressLocked()
	h.mu.Unlock()
	for _, client := range h.clients {
		client.beginReceiveDrain()
	}
	h.mu.Lock()
	h.receiveDrainProofEnabled = true
	h.resetReceiveDrainZeroProgressLocked()
	h.mu.Unlock()
}

func (h *AutoRecvAckHandle) invalidateReceiveDrainProofLocked() {
	h.receiveDrainProofInvalid = true
	h.resetReceiveDrainZeroProgressLocked()
}

func (h *AutoRecvAckHandle) resetReceiveDrainZeroProgressLocked() {
	h.receiveDrainLastZero = model.ReceiveDrainSnapshot{}
	h.receiveDrainLastZeroAt = time.Time{}
	h.receiveDrainStableZero = 0
}

func normalizedReceiveDrainZeroCut(snapshot model.ReceiveDrainSnapshot) model.ReceiveDrainSnapshot {
	snapshot.DrainComplete = false
	snapshot.StableZeroObservations = 0
	return snapshot
}

// DrainAndStop establishes the live two-cut terminal receive proof before it
// cancels and joins this generation's readers. It always stops and joins, even
// when the caller's drain deadline expires. The stopped snapshot is marked
// incomplete when either side of the stop boundary lacked queue evidence.
func (h *AutoRecvAckHandle) DrainAndStop(ctx context.Context) (drained model.ReceiveDrainSnapshot, stopped model.ReceiveDrainSnapshot, err error) {
	return h.DrainAndStopWithFence(ctx, nil)
}

// DrainAndStopWithFence establishes a live two-cut proof, joins the producer
// ingress, then establishes a fresh proof with the bounded queues still
// installed. This order avoids half-closing a session with known receive work
// while making every frame published through the fence visible to the final
// proof. It then stops and joins receive readers before comparing the stopped
// cut.
func (h *AutoRecvAckHandle) DrainAndStopWithFence(ctx context.Context, fence func() error) (drained model.ReceiveDrainSnapshot, stopped model.ReceiveDrainSnapshot, err error) {
	drained, err = h.WaitDrained(ctx)
	var fenceErr error
	if fence != nil {
		fenceErr = fence()
	}
	if h == nil {
		return drained, model.ReceiveDrainSnapshot{Required: true}, errors.Join(err, fenceErr)
	}
	if err == nil && fenceErr == nil && fence != nil {
		drained, err = h.WaitDrained(ctx)
	}
	h.Cancel()
	h.Wait()
	stopped = h.aggregateReceiveDrainSnapshot()
	if fenceErr != nil || !receiveDrainStopBoundaryStable(drained, stopped) {
		stopped.EvidenceComplete = false
		stopped.DrainComplete = false
		stopped.StableZeroObservations = 0
		if err == nil {
			err = fmt.Errorf("receive drain changed across stop boundary")
		}
	}
	h.storeReceiveDrainSnapshot(stopped)
	return drained, stopped, errors.Join(err, fenceErr)
}

func receiveDrainStopBoundaryStable(drained, stopped model.ReceiveDrainSnapshot) bool {
	return drained.TerminalProofComplete() && stopped.EvidenceComplete &&
		stopped.Required == drained.Required &&
		stopped.ClientCount == drained.ClientCount &&
		stopped.QueueSnapshotClients == drained.QueueSnapshotClients &&
		stopped.ActiveDrains == 0 && !stopped.PendingWork() &&
		stopped.RecvACKFailures == drained.RecvACKFailures &&
		stopped.RecvACKSuccesses == drained.RecvACKSuccesses &&
		stopped.ReadFailures == drained.ReadFailures &&
		stopped.ReceiveFramesObserved == drained.ReceiveFramesObserved &&
		stopped.BufferedFramesDrained == drained.BufferedFramesDrained
}

func receiveDrainObservationInterval(clientCount int) time.Duration {
	if clientCount < 1 {
		return receiveDrainMinimumObservationInterval
	}
	interval := time.Duration(clientCount) * time.Second / receiveDrainObservationBudget
	if interval < receiveDrainMinimumObservationInterval {
		return receiveDrainMinimumObservationInterval
	}
	if interval > receiveDrainMaximumObservationInterval {
		return receiveDrainMaximumObservationInterval
	}
	return interval
}

func (h *AutoRecvAckHandle) aggregateReceiveDrainSnapshot() model.ReceiveDrainSnapshot {
	snapshot := model.ReceiveDrainSnapshot{
		Required:         true,
		EvidenceComplete: len(h.clients) > 0,
		ClientCount:      uint64(len(h.clients)),
	}
	for _, client := range h.clients {
		part := client.receiveDrainSnapshot()
		if !part.EvidenceComplete {
			snapshot.EvidenceComplete = false
		}
		if !mergeReceiveDrainSnapshot(&snapshot, part) {
			snapshot.EvidenceComplete = false
		}
	}
	return snapshot
}

func mergeReceiveDrainSnapshot(dst *model.ReceiveDrainSnapshot, src model.ReceiveDrainSnapshot) bool {
	if dst == nil {
		return false
	}
	values := []struct {
		dst *uint64
		src uint64
	}{
		{&dst.ActiveDrains, src.ActiveDrains},
		{&dst.QueueSnapshotClients, src.QueueSnapshotClients},
		{&dst.InnerRecvDepth, src.InnerRecvDepth},
		{&dst.InnerRecvHandoffs, src.InnerRecvHandoffs},
		{&dst.AdapterQueueDepth, src.AdapterQueueDepth},
		{&dst.AdapterHandoffs, src.AdapterHandoffs},
		{&dst.MatchingBufferDepth, src.MatchingBufferDepth},
		{&dst.ForegroundMatchers, src.ForegroundMatchers},
		{&dst.ReadFramesInFlight, src.ReadFramesInFlight},
		{&dst.RecvACKsInFlight, src.RecvACKsInFlight},
		{&dst.PublicationsInFlight, src.PublicationsInFlight},
		{&dst.PublicationWaiters, src.PublicationWaiters},
		{&dst.RecvACKFailures, src.RecvACKFailures},
		{&dst.RecvACKSuccesses, src.RecvACKSuccesses},
		{&dst.ReadFailures, src.ReadFailures},
		{&dst.ReceiveFramesObserved, src.ReceiveFramesObserved},
		{&dst.BufferedFramesDrained, src.BufferedFramesDrained},
	}
	for _, value := range values {
		if math.MaxUint64-*value.dst < value.src {
			*value.dst = math.MaxUint64
			return false
		}
		*value.dst += value.src
	}
	return true
}

func (h *AutoRecvAckHandle) storeReceiveDrainSnapshot(snapshot model.ReceiveDrainSnapshot) {
	h.mu.Lock()
	h.last = snapshot
	h.mu.Unlock()
}

func (c *matchingPersonClient) beginReceiveDrain() {
	c.mu.Lock()
	c.terminalReceiveDrain = true
	c.drainTerminalBufferLocked()
	c.signalLocked()
	c.mu.Unlock()
}

func (c *matchingPersonClient) receiveDrainSnapshot() model.ReceiveDrainSnapshot {
	queueSource, queueAvailable := c.client.(receiveQueueSnapshotter)
	var queue benchwkproto.QueueSnapshot
	queueValid := false
	if queueAvailable {
		queue = queueSource.QueueSnapshot()
		queueValid = validReceiveQueueSnapshot(queue)
	}

	c.mu.Lock()
	recvACKSuccessOverflow := c.recvACKSuccessOverflow
	snapshot := model.ReceiveDrainSnapshot{
		Required:              true,
		ClientCount:           1,
		MatchingBufferDepth:   uint64(len(c.buffer)),
		ForegroundMatchers:    nonnegativeUint64(c.foregroundMatchers),
		ReadFramesInFlight:    c.readFramesInFlight,
		RecvACKsInFlight:      c.recvACKsInFlight,
		RecvACKFailures:       c.recvACKFailures,
		RecvACKSuccesses:      c.recvACKSuccesses,
		ReadFailures:          c.readFailures,
		ReceiveFramesObserved: c.receiveFramesObserved,
		BufferedFramesDrained: c.bufferedFramesDrained,
	}
	if c.autoRecvAck && c.autoRecvAckDone != nil {
		snapshot.ActiveDrains = 1
	}
	c.mu.Unlock()

	if !queueValid {
		return snapshot
	}
	snapshot.EvidenceComplete = !recvACKSuccessOverflow
	snapshot.QueueSnapshotClients = 1
	snapshot.InnerRecvDepth = uint64(queue.InnerRecvDepth)
	snapshot.InnerRecvHandoffs = uint64(queue.InnerRecvHandoffs)
	snapshot.AdapterQueueDepth = uint64(queue.AdapterDepth)
	snapshot.AdapterHandoffs = uint64(queue.AdapterHandoffs)
	snapshot.PublicationsInFlight = uint64(queue.PublicationCurrent)
	snapshot.PublicationWaiters = uint64(queue.PublicationBlocked)
	return snapshot
}

func validReceiveQueueSnapshot(snapshot benchwkproto.QueueSnapshot) bool {
	if snapshot.InnerRecvCapacity <= 0 || snapshot.AdapterCapacity <= 0 ||
		snapshot.RecvCapacity <= 0 || snapshot.SendackCapacity <= 0 ||
		snapshot.ErrorCapacity <= 0 || snapshot.PublicationCapacity <= 0 {
		return false
	}
	if snapshot.InnerRecvDepth < 0 || snapshot.InnerRecvDepth > snapshot.InnerRecvCapacity ||
		snapshot.InnerRecvHandoffs < 0 ||
		snapshot.AdapterDepth < 0 || snapshot.AdapterDepth > snapshot.AdapterCapacity ||
		snapshot.AdapterHandoffs < 0 ||
		snapshot.RecvDepth < 0 || snapshot.RecvDepth > snapshot.RecvCapacity ||
		snapshot.SendackDepth < 0 || snapshot.SendackDepth > snapshot.SendackCapacity ||
		snapshot.ErrorDepth < 0 || snapshot.ErrorDepth > snapshot.ErrorCapacity ||
		snapshot.PublicationCurrent < 0 || snapshot.PublicationCurrent > snapshot.PublicationCapacity ||
		snapshot.PublicationBlocked < 0 {
		return false
	}
	remainingCapacity := snapshot.AdapterCapacity
	for _, capacity := range []int{snapshot.RecvCapacity, snapshot.SendackCapacity, snapshot.ErrorCapacity} {
		if capacity > remainingCapacity {
			return false
		}
		remainingCapacity -= capacity
	}
	return remainingCapacity == 0 &&
		snapshot.AdapterDepth == snapshot.RecvDepth+snapshot.SendackDepth+snapshot.ErrorDepth
}

func nonnegativeUint64(value int) uint64 {
	if value <= 0 {
		return 0
	}
	return uint64(value)
}
