package message

import "errors"

// PermissionAdmissionRejection carries the identity-free reservation snapshot
// measured when an authoritative permission ingress call was rejected.
type PermissionAdmissionRejection struct {
	// Reasons is a bitmask: owned calls=2, owned bytes=4. Bit 1 is reserved
	// for historical active-cohort rejections, before bounded caller queuing.
	Reasons uint8
	// ActiveCohorts includes collecting and executing cohorts.
	ActiveCohorts int
	// OwnedCalls and OwnedBytes include retained canceled work until join.
	OwnedCalls int
	OwnedBytes int
	// RequestBytes is the rejected call's conservative reservation cost.
	RequestBytes int
}

// PermissionAdmissionRejectionFromError returns a copied observation; false
// means unavailable and must not be interpreted as zero resource occupancy.
func PermissionAdmissionRejectionFromError(err error) (PermissionAdmissionRejection, bool) {
	var carrier interface {
		PermissionAdmissionRejection() PermissionAdmissionRejection
	}
	if !errors.As(err, &carrier) || carrier == nil {
		return PermissionAdmissionRejection{}, false
	}
	return carrier.PermissionAdmissionRejection(), true
}

// WithPermissionAdmissionRejection attaches an observed value while preserving
// the original error text and error identities. A nil cause remains nil.
func WithPermissionAdmissionRejection(err error, snapshot PermissionAdmissionRejection) error {
	if err == nil {
		return nil
	}
	return &permissionAdmissionError{cause: err, snapshot: snapshot}
}

type permissionAdmissionError struct {
	cause    error
	snapshot PermissionAdmissionRejection
}

func (e *permissionAdmissionError) Error() string { return e.cause.Error() }
func (e *permissionAdmissionError) Unwrap() error { return e.cause }
func (e *permissionAdmissionError) PermissionAdmissionRejection() PermissionAdmissionRejection {
	return e.snapshot
}
