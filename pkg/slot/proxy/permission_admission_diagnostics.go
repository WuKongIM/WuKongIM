package proxy

import "errors"

// PermissionAdmissionRejection is an immutable, identity-free snapshot of a
// rejected ingress cohort reservation. It does not describe receiver admission.
type PermissionAdmissionRejection struct {
	// Reasons is a bitmask: active cohorts=1, owned calls=2, owned bytes=4.
	Reasons uint8
	// ActiveCohorts includes both collecting and executing cohorts.
	ActiveCohorts int
	// OwnedCalls and OwnedBytes include retained canceled callers until join.
	OwnedCalls int
	OwnedBytes int
	// RequestBytes is this rejected call's conservative reservation cost.
	RequestBytes int
}

// PermissionAdmissionRejectionFromError returns the copied snapshot only when
// admission observed it; false must not be interpreted as zero occupancy.
func PermissionAdmissionRejectionFromError(err error) (PermissionAdmissionRejection, bool) {
	var carrier interface {
		PermissionAdmissionRejection() PermissionAdmissionRejection
	}
	if !errors.As(err, &carrier) || carrier == nil {
		return PermissionAdmissionRejection{}, false
	}
	return carrier.PermissionAdmissionRejection(), true
}

// permissionAdmissionRejectionError retains the observed value independently of
// the Store's later ownership changes, while preserving the existing busy cause.
type permissionAdmissionRejectionError struct {
	snapshot PermissionAdmissionRejection
}

func (*permissionAdmissionRejectionError) Error() string { return ErrPermissionBusy.Error() }
func (*permissionAdmissionRejectionError) Unwrap() error { return ErrPermissionBusy }
func (e *permissionAdmissionRejectionError) PermissionAdmissionRejection() PermissionAdmissionRejection {
	return e.snapshot
}
