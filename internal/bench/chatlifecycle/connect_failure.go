package chatlifecycle

import (
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

// connectFailureDiagnostic is the data-only seam supplied by the tooling client.
// No original error text, address, identity or credential is retained.
type connectFailureDiagnostic interface {
	ConnectPhase() string
	ConnectKind() string
	ConnackReason() (frame.ReasonCode, bool)
	ConnectFingerprint() [16]byte
}

// connectFailureEvidence keeps the existing five-field recorder shape. Values
// for rejection are closed protocol reasons; other CONNECT values are kinds.
type connectFailureEvidence struct {
	code        FailureCode
	value       uint64
	fingerprint [16]byte
}

func closedConnectFailure(err error) (observation connectFailureEvidence) {
	legacy := connectFailureEvidence{code: FailureCodeSessionConnectFailed}
	observation = legacy
	// A diagnostic implementation must not turn an existing failure into a panic.
	defer func() {
		if recover() != nil {
			observation = legacy
		}
	}()
	var diagnostic connectFailureDiagnostic
	if !errors.As(err, &diagnostic) {
		return observation
	}
	switch diagnostic.ConnectPhase() {
	case "local":
		observation.code = FailureCodeSessionConnectLocal
	case "dial":
		observation.code = FailureCodeSessionConnectDial
	case "connect_write":
		observation.code = FailureCodeSessionConnectWrite
	case "connack_read":
		observation.code = FailureCodeSessionConnackRead
	case "frame":
		observation.code = FailureCodeSessionConnackFrame
	case "rejected":
		observation.code = FailureCodeSessionConnackRejected
	case "crypto":
		observation.code = FailureCodeSessionConnectCrypto
	default:
		return legacy
	}
	if observation.code == FailureCodeSessionConnackRejected {
		reason, known := diagnostic.ConnackReason()
		if known && reason == frame.ReasonSuccess {
			return legacy
		}
		if known && reason <= frame.ReasonSystemBusy {
			observation.value = uint64(reason)
		}
	} else {
		observation.value = connectFailureKindValue(diagnostic.ConnectKind())
	}
	observation.fingerprint = diagnostic.ConnectFingerprint()
	return observation
}

// connectFailureKindValue is append-only. Zero explicitly means unknown.
func connectFailureKindValue(kind string) uint64 {
	switch kind {
	case "canceled":
		return 1
	case "deadline":
		return 2
	case "timeout":
		return 3
	case "eof":
		return 4
	case "network":
		return 5
	case "closed":
		return 6
	case "fence":
		return 7
	case "protocol":
		return 8
	case "rejected":
		return 9
	case "crypto":
		return 10
	default:
		return 0
	}
}

func newConnectLoginSyncOperationError(reason string, cause error) error {
	failure := &loginSyncOperationError{stage: LoginSyncStageConnect, reason: reason}
	if reason == LoginSyncReasonTransport {
		failure.connect = closedConnectFailure(cause)
	}
	return failure
}
