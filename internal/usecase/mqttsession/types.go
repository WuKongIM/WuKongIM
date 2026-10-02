// Package mqttsession coordinates durable Session decisions and local execution.
package mqttsession

import (
	"context"
	"errors"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	"github.com/WuKongIM/WuKongIM/internal/contracts/protocolmeta"
	owner "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

var (
	ErrInvalid  = errors.New("mqttsession: invalid lifecycle request")
	ErrBinding  = errors.New("mqttsession: client identifier belongs to another user")
	ErrConflict = errors.New("mqttsession: concurrent lifecycle decision")
	ErrEvidence = errors.New("mqttsession: invalid authority evidence")
	ErrFenced   = errors.New("mqttsession: durable owner fenced")
	ErrClock    = errors.New("mqttsession: lifecycle clock unavailable or regressed")
	// ErrWillDenied distinguishes current publish-policy denial from unavailable authority.
	ErrWillDenied = errors.New("mqttsession: Will publication not authorized")
)

// Metadata uses shared durable contracts and is implemented by the foreground-
// gated cluster Node. Product reads must establish current Slot authority.
type Metadata interface {
	ReadMQTT(context.Context, meta.MQTTRead) (meta.MQTTReadResult, error)
	ApplyMQTTLifecycle(context.Context, meta.MQTTLifecycleMutation) (meta.MQTTLifecycleResult, error)
	CompareAndSwapMQTTSession(context.Context, uint64, meta.MQTTSession) (meta.MQTTSessionCASResult, error)
}

// TokenVerifier reuses current device credentials without WK conflict actions.
type TokenVerifier interface {
	VerifyToken(context.Context, string, protocolmeta.DeviceFlag, string) (protocolmeta.DeviceLevel, error)
}

// Isolation proves exact execution/transport isolation, never merely absence,
// a stored state, clock expiry, RPC failure or a different process boot.
type Isolation interface {
	Quiesce(context.Context, contract.Owner) error
}

// WillAuthorizer checks current publish permission before accepting a template.
// Execution must authorize again; this check does not grant a future publication.
type WillAuthorizer interface {
	AuthorizeWill(context.Context, string, WillTarget) error
}

// Options owns no worker or connection queue. The app supplies bounded runtimes
// and the same monotonic clock to this usecase and its local Owners registry.
type Options struct {
	// Store provides fresh Slot reads and committed conditional writes.
	Store Metadata
	// Owners bounds local reservations and admitted execution scopes.
	Owners *owner.Owners
	// Isolation proves that the complete previous owner has stopped.
	Isolation Isolation
	// Tokens verifies current durable device credentials, without device kicks.
	Tokens TokenVerifier
	// Wills authorizes a mapped destination before its template is committed.
	Wills WillAuthorizer
	// Now shares the Owners registry's local monotonic time base.
	Now func() time.Time
	// LeaseDuration starts before proposal submission, at most one minute, and
	// must fit the configured Owners MaxLease when app composes these modules.
	LeaseDuration time.Duration
	// CleanupTimeout bounds detached cleanup of a failed local reservation.
	CleanupTimeout time.Duration
	// SessionExpiryLimitSec cannot exceed the approved 24-hour offline lifetime.
	SessionExpiryLimitSec uint32
	// Quotas are fixed per durable lifetime and preserved on a resume.
	QuotaMessages, QuotaBytes uint64
	// WindowLimit bounds new admissions while preserving older outstanding rows.
	WindowLimit uint16
}

type App struct{ opts Options }

// WillTarget is a previously mapped IM destination, not a wire packet.
type WillTarget struct {
	Topic, TargetID string
	TargetType      uint8
}

// Will owns bounded immutable template content, without an allocated server key.
type Will struct {
	WillTarget
	QoS                          uint8
	DelaySeconds                 uint32
	ClientMsgNo                  string
	Payload, PublicationMetadata []byte
}

// ConnectCommand carries ephemeral credentials only for this call. They never
// enter the Session row, returned connection or local owner reservation.
type ConnectCommand struct {
	Key              contract.Key
	UID, Token       string
	DeviceFlag       protocolmeta.DeviceFlag
	CleanStart       bool
	SessionExpirySec uint32
	ReceiveMaximum   uint16
	MaxPacketBytes   uint32
	Will             *Will
	CloseTransport   owner.CloseTransport
}

// Lease is owner-local evidence derived before one committed write. Until keeps
// its monotonic component and must never be serialized into remote fencing proof.
type Lease struct {
	Revision uint64
	Until    time.Time
}

// Connection describes one committed and locally activated execution owner.
type Connection struct {
	Owner            contract.Owner
	UID              string
	DeviceFlag       protocolmeta.DeviceFlag
	SessionPresent   bool
	SessionExpirySec uint32
	WillGeneration   uint64
	Lease            Lease
}

// DisconnectCommand must run outside every admitted scope of this owner.
// Normal suppresses Will; transport loss and explicit Will requests use false.
type DisconnectCommand struct {
	Owner            contract.Owner
	Normal           bool
	SessionExpirySec *uint32
	// ObservedAt is an optional trusted owner-local monotonic observation from
	// entry admission. Queued cleanup preserves it; it is never client input or
	// serialized authority evidence. Zero captures the current usecase time.
	ObservedAt time.Time
}

func New(opts Options) (*App, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Store == nil || opts.Owners == nil || opts.Isolation == nil || opts.Tokens == nil || opts.Wills == nil || opts.LeaseDuration < time.Millisecond || opts.LeaseDuration > time.Minute || opts.LeaseDuration%time.Millisecond != 0 || opts.CleanupTimeout <= 0 || opts.CleanupTimeout > 5*time.Second || opts.SessionExpiryLimitSec == 0 || opts.SessionExpiryLimitSec > 86400 || opts.QuotaMessages == 0 || opts.QuotaBytes == 0 || opts.WindowLimit == 0 || opts.WindowLimit > meta.MQTTMaxInflight {
		return nil, ErrInvalid
	}
	a := &App{opts: opts}
	if _, err := a.now(); err != nil {
		return nil, err
	}
	return a, nil
}
