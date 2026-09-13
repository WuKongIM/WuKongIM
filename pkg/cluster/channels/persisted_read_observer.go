package channels

import "time"

// PersistedReadObserver separates serving-node admission from storage occupancy.
// Callbacks must be cheap; kinds are heads/recents and carry no Channel identity.
type PersistedReadObserver interface {
	ObservePersistedReadAdmission(kind string, accepted bool, inUse, limit int)
	ObservePersistedReadCompletion(kind, result string, items int, duration time.Duration)
}

// beginPersistedRead retains the existing nonblocking, shared batch bound.
func (s *Service) beginPersistedRead(kind string) (time.Time, bool) {
	accepted := false
	select {
	case s.persistedReads <- struct{}{}:
		accepted = true
	default:
	}
	if o, ok := s.observer.(PersistedReadObserver); ok {
		o.ObservePersistedReadAdmission(kind, accepted, len(s.persistedReads), cap(s.persistedReads))
	}
	return time.Now(), accepted
}

// endPersistedRead accounts for every admitted terminal path before releasing its slot.
func (s *Service) endPersistedRead(kind, result string, items int, started time.Time) {
	if o, ok := s.observer.(PersistedReadObserver); ok {
		o.ObservePersistedReadCompletion(kind, result, items, time.Since(started))
	}
	<-s.persistedReads
}
