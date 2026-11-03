package chatlifecycle

import "time"

// CoordinatorGrantTickFailure retains one rejected timestamp boundary without
// adding a clock, raw timestamp, identity, history or authority to its verdict.
type CoordinatorGrantTickFailure struct {
	Boundary string `json:"boundary"`
	Reason   string `json:"reason"`
	// HaveLastTick distinguishes the first scheduled tick from later cadence.
	HaveLastTick bool `json:"have_last_tick"`
	// Observed flags keep absent clocks distinct from a measured zero interval.
	AgeObserved      bool          `json:"age_observed"`
	AgeNanos         time.Duration `json:"age_nanos"`
	IntervalObserved bool          `json:"interval_observed"`
	IntervalNanos    time.Duration `json:"interval_nanos"`
}

// Bounded rejects unknown strings and contradictory clock-presence facts before
// a consumer exports the record. It does not validate or change a run verdict.
func (f CoordinatorGrantTickFailure) Bounded() CoordinatorGrantTickFailure {
	switch f.Boundary {
	case "capacity_phase_end":
		if f.Reason != "zero_clock" && f.Reason != "zero_tick" && f.Reason != "future" && f.Reason != "phase_end" {
			return CoordinatorGrantTickFailure{}
		}
	case "scheduled_grant":
	case "pending_status", "cutoff_poll", "cutoff_wait", "capacity_status_poll", "status_poll":
		if f.Reason != "zero_clock" && f.Reason != "zero_tick" && f.Reason != "future" {
			return CoordinatorGrantTickFailure{}
		}
	default:
		return CoordinatorGrantTickFailure{}
	}
	if !f.AgeObserved && f.AgeNanos != 0 || !f.IntervalObserved && f.IntervalNanos != 0 {
		return CoordinatorGrantTickFailure{}
	}
	valid := false
	switch f.Reason {
	case "zero_clock":
		valid = !f.AgeObserved
	case "zero_tick":
		valid = !f.AgeObserved && !f.IntervalObserved
	case "zero_last_tick":
		valid = !f.IntervalObserved
	case "phase_end":
		valid = f.Boundary == "capacity_phase_end" && f.AgeObserved && f.AgeNanos >= 0
	case "future":
		valid = f.AgeObserved && f.AgeNanos < 0
	case "stale":
		valid = f.AgeObserved && f.AgeNanos > coordinatorGrantCadence+coordinatorGrantDeliveryTolerance && f.IntervalObserved
	case "interval_early":
		valid = f.HaveLastTick && f.IntervalObserved && f.IntervalNanos < coordinatorGrantCadence-coordinatorGrantTickTolerance
	case "interval_late":
		valid = f.HaveLastTick && f.IntervalObserved && f.IntervalNanos > coordinatorGrantCadence+coordinatorGrantTickTolerance
	case "first_early":
		valid = !f.HaveLastTick && f.IntervalObserved && f.IntervalNanos < coordinatorGrantCadence
	case "first_late":
		valid = !f.HaveLastTick && f.IntervalObserved && f.IntervalNanos >= 2*coordinatorGrantCadence
	}
	if f.Reason == "interval_early" || f.Reason == "interval_late" || f.Reason == "first_early" || f.Reason == "first_late" {
		valid = valid && f.AgeObserved && f.AgeNanos >= 0 && f.AgeNanos <= coordinatorGrantCadence+coordinatorGrantDeliveryTolerance
	}
	if !valid {
		return CoordinatorGrantTickFailure{}
	}
	return f
}

// coordinatorGrantTickFailure only copies values already used by a failed
// guard. It never changes that guard's original tolerances or retry behavior.
func coordinatorGrantTickFailure(now, tick, last time.Time, haveLast bool, boundary string) CoordinatorGrantTickFailure {
	f := CoordinatorGrantTickFailure{Boundary: boundary, HaveLastTick: haveLast}
	if !now.IsZero() && !tick.IsZero() {
		f.AgeObserved = true
		f.AgeNanos = now.Sub(tick)
	}
	if !tick.IsZero() && !last.IsZero() {
		f.IntervalObserved = true
		f.IntervalNanos = tick.Sub(last)
	}
	switch {
	case now.IsZero():
		f.Reason = "zero_clock"
	case tick.IsZero():
		f.Reason = "zero_tick"
	case last.IsZero():
		f.Reason = "zero_last_tick"
	case tick.After(now):
		f.Reason = "future"
	case f.AgeNanos > coordinatorGrantCadence+coordinatorGrantDeliveryTolerance:
		f.Reason = "stale"
	case haveLast && f.IntervalNanos < coordinatorGrantCadence-coordinatorGrantTickTolerance:
		f.Reason = "interval_early"
	case haveLast && f.IntervalNanos > coordinatorGrantCadence+coordinatorGrantTickTolerance:
		f.Reason = "interval_late"
	case !haveLast && f.IntervalNanos < coordinatorGrantCadence:
		f.Reason = "first_early"
	case !haveLast && f.IntervalNanos >= 2*coordinatorGrantCadence:
		f.Reason = "first_late"
	default:
		return f
	}
	return f
}
