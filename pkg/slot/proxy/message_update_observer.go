package proxy

import "time"

// MessageUpdateReadObserver measures serving physical-Slot groups for all edit
// readers (including history). Barrier includes quorum and durable-apply wait;
// storage includes snapshot reads, assembly and final authority revalidation.
// Implementations must be concurrency-safe and must not block serving workers.
type MessageUpdateReadObserver interface {
	// MessageUpdateReadObservationEnabled is sampled once before serving starts.
	MessageUpdateReadObservationEnabled() bool
	ObserveMessageUpdateReadStage(stage, result string, duration time.Duration)
}

func (s *Store) startMessageUpdateStage() time.Time {
	if s.messageUpdateObserver == nil {
		return time.Time{}
	}
	return time.Now()
}
func (s *Store) finishMessageUpdateStage(stage string, start time.Time, err error) {
	if s.messageUpdateObserver == nil {
		return
	}
	result := "ok"
	if err != nil {
		result = "error"
	}
	s.messageUpdateObserver.ObserveMessageUpdateReadStage(stage, result, time.Since(start))
}
