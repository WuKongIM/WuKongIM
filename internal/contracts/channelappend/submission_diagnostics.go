package channelappend

import (
	"errors"
	"time"
)

// SubmissionFailureDiagnostics is one original submission's immutable timing
// snapshot before its completion callback. It contains no request identities.
type SubmissionFailureDiagnostics struct {
	// Queue covers accepted ownership until a worker selects the original job,
	// including waits for overlapping Channel predecessors.
	Queue time.Duration
	// Route covers the joined sender call, including sibling work in its batch.
	// It is not a measurement of RPC, storage or CPU time alone.
	Route time.Duration
	// PreCallback covers sender return until this job's pre-callback snapshot,
	// including earlier callbacks in the batch but excluding diagnostic attachment
	// and this job's callback execution.
	PreCallback time.Duration
}

// SubmissionFailureDiagnosticsFromError returns a copied timing snapshot when
// an admission owner supplied one. False distinguishes absent from measured zero.
func SubmissionFailureDiagnosticsFromError(err error) (SubmissionFailureDiagnostics, bool) {
	var carrier interface {
		SubmissionFailureDiagnostics() SubmissionFailureDiagnostics
	}
	if !errors.As(err, &carrier) || carrier == nil {
		return SubmissionFailureDiagnostics{}, false
	}
	return carrier.SubmissionFailureDiagnostics(), true
}
