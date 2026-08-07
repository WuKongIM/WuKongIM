package chatlifecycle

import (
	"context"
	"time"
)

// RuntimeSafetyCause is the closed fatal cause emitted by the immutable
// formal Lease envelope.
type RuntimeSafetyCause string

const (
	RuntimeSafetyOK              RuntimeSafetyCause = ""
	RuntimeSafetyBudgetStop      RuntimeSafetyCause = "budget_stop"
	RuntimeSafetyLeaseExpiryRisk RuntimeSafetyCause = "lease_expiry_risk"
)

// RuntimeSafetySnapshot is a bounded cost/expiry projection retained in the
// formal report. NetworkTransmitBytes is a conservative all-interface upper bound.
type RuntimeSafetySnapshot struct {
	// Cause is empty while the immutable Lease envelope remains safe.
	Cause RuntimeSafetyCause
	// AccruedCostMicros is the conservative scenario cost in millionths of CNY.
	AccruedCostMicros int64
	// NetworkTransmitBytes is the monotonic non-loopback load-host transmit total.
	NetworkTransmitBytes uint64
	// LeaseRemaining is the duration until provider cleanup expiry at observation time.
	LeaseRemaining time.Duration
}

// RuntimeSafetyGuard evaluates immutable Lease and quoted-cost evidence on
// every observation round without cloud mutation or credential access.
type RuntimeSafetyGuard interface {
	Observe(context.Context, time.Time, uint64) (RuntimeSafetySnapshot, error)
}
