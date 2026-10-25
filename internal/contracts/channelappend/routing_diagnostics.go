package channelappend

import (
	"errors"
	"time"
)

// RoutingFailureDiagnostics is an immutable single-item Router invocation's
// accumulated stages across its existing attempts. It contains no identities.
// Stage sums do not describe every instruction or partition total wall time.
type RoutingFailureDiagnostics struct {
	// Resolve covers authority resolver calls, excluding target normalization.
	Resolve time.Duration
	// GroupAdmission covers the shared group-slot acquisition, including pressure
	// publication. The separate nonblocking outbound guard is not included.
	GroupAdmission time.Duration
	// AppendAdmission covers origin-only append permission callbacks and their
	// surrounding terminal checks; absent callbacks contribute measured zero.
	AppendAdmission time.Duration
	// LocalSubmit covers local admission. LocalWait covers the returned Future's
	// wait; neither is a pure filesystem synchronization measurement.
	LocalSubmit time.Duration
	LocalWait   time.Duration
	// Forward covers the complete forwarding adapter attempt, its timeout and
	// normalization/invalidation. It is not a pure network/RPC duration.
	Forward time.Duration
	// RetryDelay covers the existing retry pause and its wake/cleanup work.
	RetryDelay time.Duration
	// Attempts counts actual resolver calls. LocalAttempts and RemoteAttempts
	// count actual SubmitLocal and forwarding-adapter calls, respectively.
	Attempts       int
	LocalAttempts  int
	RemoteAttempts int
}

// RoutingFailureDiagnosticsFromError returns a copied observation when present.
// False means unobserved, including the currently uninstrumented multi-item path.
func RoutingFailureDiagnosticsFromError(err error) (RoutingFailureDiagnostics, bool) {
	var carrier interface {
		RoutingFailureDiagnostics() RoutingFailureDiagnostics
	}
	if !errors.As(err, &carrier) || carrier == nil {
		return RoutingFailureDiagnostics{}, false
	}
	return carrier.RoutingFailureDiagnostics(), true
}
