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
func (s *Store) permissionStage(stage, result string, start time.Time) {
	if s.permissionObserver != nil {
		s.permissionObserver.ObserveSendPermissionStage(stage, result, time.Since(start))
	}
}
func (s *Store) permissionCount(kind string, n int) {
	if s.permissionObserver != nil {
		s.permissionObserver.ObserveSendPermissionCount(kind, n)
	}
}
