package chatlifecycle

import (
	"encoding/json"
	"time"
)

// p95LatencyGateClass is the closed local-v9 wire row. It never emits a P99
// field for the hot primary, while v1 rows retain all their original zero keys.
type p95LatencyGateClass struct {
	Operation          LatencyOperation    `json:"operation"`
	P95Limit           time.Duration       `json:"p95_limit"`
	P999Limit          time.Duration       `json:"p999_limit"`
	P95Count           uint64              `json:"p95_count"`
	P95Above           uint64              `json:"p95_above"`
	P999Count          uint64              `json:"p999_count"`
	P999Above          uint64              `json:"p999_above"`
	P95Samples         int                 `json:"p95_samples"`
	P999Samples        int                 `json:"p999_samples"`
	Breached           bool                `json:"breached"`
	BreachSinceElapsed *time.Duration      `json:"breach_since_elapsed"`
	BreachAttribution  CapacityAttribution `json:"breach_attribution"`
}

// MarshalJSON chooses only a declared row shape, never percentile interpolation.
func (r VerdictLatencyGateClass) MarshalJSON() ([]byte, error) {
	if r.P95Limit != 0 {
		return json.Marshal(p95LatencyGateClass{Operation: r.Operation, P95Limit: r.P95Limit, P999Limit: r.P999Limit, P95Count: r.P95Count, P95Above: r.P95Above, P999Count: r.P999Count, P999Above: r.P999Above, P95Samples: r.P95Samples, P999Samples: r.P999Samples, Breached: r.Breached, BreachSinceElapsed: r.BreachSinceElapsed, BreachAttribution: r.BreachAttribution})
	}
	type plain VerdictLatencyGateClass
	return json.Marshal(plain(r))
}

func (r VerdictLatencyGateClass) primaryObservation() (above, count uint64, samples int) {
	if r.P95Limit != 0 {
		return r.P95Above, r.P95Count, r.P95Samples
	}
	return r.P99Above, r.P99Count, r.P99Samples
}
