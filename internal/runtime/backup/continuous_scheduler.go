package backup

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
)

// Status returns a sorted detached snapshot of every observed Slot.
func (e *CaptureEngine) Status() []backupcontract.SlotCaptureStatus {
	if e == nil {
		return nil
	}
	e.statusMu.RLock()
	statuses := make([]backupcontract.SlotCaptureStatus, 0, len(e.status))
	for _, status := range e.status {
		statuses = append(statuses, backupcontract.CloneSlotCaptureStatus(status))
	}
	e.statusMu.RUnlock()
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].HashSlot < statuses[j].HashSlot })
	return statuses
}

// Wake records one non-blocking in-memory hint without touching durable boundaries.
func (e *CaptureEngine) Wake(hashSlot uint16) bool {
	if e == nil || hashSlot >= e.options.HashSlotCount {
		return false
	}
	select {
	case e.wake <- hashSlot:
		return true
	default:
		return false
	}
}

// Run performs an initial full-Slot reconciliation, then consumes hints and
// periodic polls through a bounded, per-Slot-coalesced worker queue.
func (e *CaptureEngine) Run(ctx context.Context) error {
	if e == nil || ctx == nil {
		return ErrInvalidCapture
	}
	jobs := make(chan uint16, int(e.options.HashSlotCount))
	pending := make([]bool, int(e.options.HashSlotCount))
	rerun := make([]bool, int(e.options.HashSlotCount))
	var pendingMu sync.Mutex
	var workers sync.WaitGroup

	for index := 0; index < e.options.WorkerCount; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case hashSlot := <-jobs:
					_, _ = e.ReconcileSlot(ctx, hashSlot)
					e.notifyPendingChanged()
					pendingMu.Lock()
					shouldRerun := rerun[hashSlot] && ctx.Err() == nil
					rerun[hashSlot] = false
					if !shouldRerun {
						pending[hashSlot] = false
					}
					pendingMu.Unlock()
					if shouldRerun {
						select {
						case jobs <- hashSlot:
						case <-ctx.Done():
							pendingMu.Lock()
							pending[hashSlot] = false
							pendingMu.Unlock()
							return
						}
					}
				}
			}
		}()
	}
	enqueue := func(hashSlot uint16) bool {
		pendingMu.Lock()
		if pending[hashSlot] {
			rerun[hashSlot] = true
			pendingMu.Unlock()
			return true
		}
		pending[hashSlot] = true
		pendingMu.Unlock()
		select {
		case jobs <- hashSlot:
			return true
		case <-ctx.Done():
			pendingMu.Lock()
			pending[hashSlot] = false
			pendingMu.Unlock()
			return false
		}
	}
	for hashSlot := uint16(0); hashSlot < e.options.HashSlotCount; hashSlot++ {
		if !enqueue(hashSlot) {
			workers.Wait()
			return nil
		}
	}

	ticker := time.NewTicker(e.options.ReconcileInterval)
	defer ticker.Stop()
	deadlineTimer := time.NewTimer(time.Hour)
	if !deadlineTimer.Stop() {
		<-deadlineTimer.C
	}
	defer deadlineTimer.Stop()
	var deadlineC <-chan time.Time
	resetDeadline := func() {
		if !deadlineTimer.Stop() && deadlineC != nil {
			select {
			case <-deadlineTimer.C:
			default:
			}
		}
		due, wait, found := e.pendingSchedule(e.options.Clock.Now())
		if len(due) > 0 {
			wait = min(time.Second, e.options.ReconcileInterval)
			found = true
		}
		if !found {
			deadlineC = nil
			return
		}
		deadlineTimer.Reset(wait)
		deadlineC = deadlineTimer.C
	}
	resetDeadline()
	for {
		select {
		case <-ctx.Done():
			workers.Wait()
			return nil
		case hashSlot := <-e.wake:
			enqueue(hashSlot)
		case <-e.pendingChanged:
			resetDeadline()
		case <-deadlineC:
			due, _, _ := e.pendingSchedule(e.options.Clock.Now())
			deadlineC = nil
			for _, hashSlot := range due {
				enqueue(hashSlot)
			}
			resetDeadline()
		case <-ticker.C:
			for hashSlot := uint16(0); hashSlot < e.options.HashSlotCount; hashSlot++ {
				if !enqueue(hashSlot) {
					workers.Wait()
					return nil
				}
			}
		}
	}
}

func (e *CaptureEngine) recordStatus(hashSlot uint16, state backupcontract.CaptureState, frontier backupcontract.SlotFrontier, watermarks SourceWatermarks, failureCategory string) {
	leaseCurrent := frontier.Lease.Sequence > 0 && state != backupcontract.CaptureStateFenced
	status := backupcontract.SlotCaptureStatus{
		HashSlot: hashSlot, State: state, Frontier: backupcontract.CloneSlotFrontier(frontier),
		MetadataSourceWatermark: watermarks.Metadata.Position,
		MessageSourceWatermark:  watermarks.Messages.Position,
		MetadataLag:             watermarkLag(watermarks.Metadata.Position, frontier.Metadata.SourceHighWatermark),
		MessageLag:              watermarkLag(watermarks.Messages.Position, frontier.Messages.SourceHighWatermark),
		ObservedAtUnixMillis:    e.options.Clock.Now().UnixMilli(),
		FailureCategory:         failureCategory,
		LeaseCurrent:            leaseCurrent,
	}
	e.statusMu.Lock()
	e.status[hashSlot] = status
	ownedSlots := 0
	for _, candidate := range e.status {
		if candidate.LeaseCurrent {
			ownedSlots++
		}
	}
	e.statusMu.Unlock()
	if e.options.Observer != nil {
		e.options.Observer.SetBackupCaptureOwnedSlots(ownedSlots)
	}
}

func (e *CaptureEngine) recordStreamCaptureError(hashSlot uint16, frontier backupcontract.SlotFrontier, watermarks SourceWatermarks, category string, err error) {
	state := backupcontract.CaptureStateFailed
	if errors.Is(err, ErrCaptureMemoryPressure) {
		state = backupcontract.CaptureStateDegraded
		category = "capture_memory"
	}
	e.recordStatus(hashSlot, state, frontier, watermarks, category)
}

func watermarkLag(source, frontier uint64) uint64 {
	if source <= frontier {
		return 0
	}
	return source - frontier
}
