package chatlifecycle

import "math"

const (
	driverCPUFailedTurnCapacity = 16
	// This one-millisecond quality limit only classifies optional observation
	// uncertainty. It is never a dispatch deadline, clock or native gate.
	driverCPUClockBracketLimitNanos int64 = 1_000_000
)

// DriverCPUClockSample retains numeric OS-clock bounds without raw errors.
type DriverCPUClockSample struct {
	MonotonicBeforeNanos int64 `json:"monotonic_before_nanos"`
	ProcessCPUNanos      int64 `json:"process_cpu_nanos"`
	MonotonicAfterNanos  int64 `json:"monotonic_after_nanos"`
	// ReadMask uses bits1/2/4 for before/CPU/after read success. All original
	// numeric values remain present even when a read or pair is invalid.
	ReadMask uint8 `json:"read_mask"`
}

// DriverCPUDeadlineMiss bounds process-wide CPU in one actual owner turn.
// Process CPU includes other goroutines and is never actor-only attribution.
type DriverCPUDeadlineMiss struct {
	Turn       uint64               `json:"turn"`
	Operation  string               `json:"operation"`
	Code       FailureCode          `json:"code"`
	OwnerEntry DriverCPUClockSample `json:"owner_entry"`
	Miss       DriverCPUClockSample `json:"miss"`
	// State and Reason classify diagnostic clock quality, never native work.
	State  string `json:"state"`
	Reason string `json:"reason"`
	// Unknown records retain zero here with State=unknown; zero is then not
	// evidence of zero CPU. Known values include all process threads.
	ProcessCPUUpperBoundNanos int64 `json:"process_cpu_upper_bound_nanos"`
}

// DriverCPUDiagnosticsSnapshot is a detached private post-join observation.
type DriverCPUDiagnosticsSnapshot struct {
	Generation uint64 `json:"generation"`
	// DroppedTurns counts the first dispatch miss of each unretained Turn,
	// rather than every rejected SEND in that Turn.
	DroppedTurns uint64                  `json:"dropped_turns"`
	Records      []DriverCPUDeadlineMiss `json:"records"`
}

// engineDriverCPURecorder is allocated only for an explicit diagnostic probe.
// The command owner alone samples and mutates its fixed record storage. The
// lifecycle owner resets it only after the previous generation has joined.
type engineDriverCPURecorder struct {
	probe        func() DriverCPUClockSample
	records      [driverCPUFailedTurnCapacity]DriverCPUDeadlineMiss
	count        int
	droppedTurns uint64
}

// beginDriverCPUOwnerTurn starts the optional CPU envelope after the current
// advance command enters its owner, not while its caller waits for admission.
// Retention is owner-only, so this cap check needs no mutex or probe goroutine.
func (e *Engine) beginDriverCPUOwnerTurn(turn *driverTurn) {
	d := e.driverCPURecorder
	if d == nil || turn == nil || d.count == driverCPUFailedTurnCapacity {
		return
	}
	turn.cpuOwnerEntry = d.probe()
}

// recordDriverCPUDeadlineMiss runs after the existing native code37/code38
// decision. It samples at most once per Turn and never reads SessionClock.
// A full recorder still counts distinct failed Turns without probing again.
func (e *Engine) recordDriverCPUDeadlineMiss(code FailureCode) {
	d := e.driverCPURecorder
	turn := e.driverTurn
	if d == nil || turn == nil || turn.cpuMissSeen ||
		(code != FailureCodeOfferedDispatchSampledDeadline && code != FailureCodeOfferedDispatchFreshDeadline) {
		return
	}
	turn.cpuMissSeen = true
	if d.count == driverCPUFailedTurnCapacity {
		if d.droppedTurns < math.MaxUint64 {
			d.droppedTurns++
		}
		return
	}
	record := DriverCPUDeadlineMiss{
		Turn:       turn.record.Turn,
		Operation:  turn.record.Operation,
		Code:       code,
		OwnerEntry: turn.cpuOwnerEntry,
		Miss:       d.probe(),
		State:      "unknown",
	}
	record.Reason = driverCPUClockPairReason(record.OwnerEntry, record.Miss)
	if record.Reason == "none" {
		record.State = "known"
		// The CPU reads enclose the selected owner interval but also include
		// other process threads, goroutines, GC and observation overhead. This
		// is not actor-only CPU, nor proof of OS or VM scheduling contention.
		record.ProcessCPUUpperBoundNanos = record.Miss.ProcessCPUNanos - record.OwnerEntry.ProcessCPUNanos
	}
	d.records[d.count] = record
	d.count++
}

// driverCPUClockPairReason preserves all raw readings; only a complete ordered
// narrow pair permits a derived process-CPU bound. Equal endpoints and CPU
// deltas larger than elapsed wall time are valid for process-wide CPU clocks.
func driverCPUClockPairReason(entry, miss DriverCPUClockSample) string {
	if entry.ReadMask != 7 || miss.ReadMask != 7 {
		return "read_failed"
	}
	if entry.MonotonicBeforeNanos < 0 || entry.ProcessCPUNanos < 0 || entry.MonotonicAfterNanos < 0 ||
		miss.MonotonicBeforeNanos < 0 || miss.ProcessCPUNanos < 0 || miss.MonotonicAfterNanos < 0 {
		return "negative_clock"
	}
	if entry.MonotonicAfterNanos < entry.MonotonicBeforeNanos || miss.MonotonicAfterNanos < miss.MonotonicBeforeNanos ||
		miss.MonotonicBeforeNanos < entry.MonotonicAfterNanos {
		return "monotonic_regressed"
	}
	if miss.ProcessCPUNanos < entry.ProcessCPUNanos {
		return "process_cpu_regressed"
	}
	if entry.MonotonicAfterNanos-entry.MonotonicBeforeNanos > driverCPUClockBracketLimitNanos ||
		miss.MonotonicAfterNanos-miss.MonotonicBeforeNanos > driverCPUClockBracketLimitNanos {
		return "bracket_wide"
	}
	return "none"
}

// DriverCPUDiagnosticsAfterStop exposes only a detached numeric copy after
// Stop has joined the admitted owner. It performs no diagnostic IO and never
// adds optional fields to the native worker snapshot, evidence or protocol.
func (e *Engine) DriverCPUDiagnosticsAfterStop() *DriverCPUDiagnosticsSnapshot {
	if e == nil {
		return nil
	}
	e.lifecycleMu.Lock()
	defer e.lifecycleMu.Unlock()
	if e.driverCPURecorder == nil || e.running || e.stopping || e.generation == 0 {
		return nil
	}
	d := e.driverCPURecorder
	return &DriverCPUDiagnosticsSnapshot{
		Generation:   e.generation,
		DroppedTurns: d.droppedTurns,
		Records:      append([]DriverCPUDeadlineMiss{}, d.records[:d.count]...),
	}
}
