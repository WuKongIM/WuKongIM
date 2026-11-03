package proxy

import "time"

// SendPermissionObserver reports bounded transport/read work, without raw
// identities. Implementations must be concurrency-safe and nonblocking.
type SendPermissionObserver interface {
	ObserveSendPermissionCount(kind string, n int)
	ObserveSendPermissionStage(stage, result string, duration time.Duration)
	ObserveSendPermissionInflight(delta int)
}

// SetSendPermissionObserver installs instrumentation before serving starts.
func (s *Store) SetSendPermissionObserver(o SendPermissionObserver) { s.permissionObserver = o }
func (s *Store) permissionStart() time.Time {
	if s.permissionObserver != nil {
		return time.Now()
	}
	return time.Time{}
}

// permissionStage returns the same clock measurement it publishes so optional
// invocation summaries add no second clock read. A missing observer reads none.
func (s *Store) permissionStage(stage, result string, start time.Time) time.Duration {
	if s.permissionObserver != nil {
		duration := time.Since(start)
		s.permissionObserver.ObserveSendPermissionStage(stage, result, duration)
		return duration
	}
	return 0
}
func (s *Store) permissionCount(kind string, n int) {
	if s.permissionObserver != nil {
		s.permissionObserver.ObserveSendPermissionCount(kind, n)
	}
}

// SendPermissionCohortObserver optionally reports conservative retained-memory
// credits and owned calls/cohorts using only fixed dimensions.
type SendPermissionCohortObserver interface {
	ObserveSendPermissionCohortOwned(kind string, delta int)
}

func (s *Store) permissionCohortOwned(kind string, delta int) {
	if o, ok := s.permissionObserver.(SendPermissionCohortObserver); ok {
		o.ObserveSendPermissionCohortOwned(kind, delta)
	}
}
