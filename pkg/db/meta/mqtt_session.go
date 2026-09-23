package meta

import (
	"context"
	"encoding/hex"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

// MQTTMaxInflight bounds durable per-session window state and allocator work.
const MQTTMaxInflight uint16 = 1024

// MQTTDefaultWindowLimit is the initial bound when no explicit limit is stored.
const MQTTDefaultWindowLimit uint16 = 64

// MQTTSessionState identifies durable session lifetime, not socket presence.
type MQTTSessionState uint8

const (
	MQTTSessionActive  MQTTSessionState = 1
	MQTTSessionOffline MQTTSessionState = 2
	// MQTTSessionEnded retains the UID binding and generation fences after cleanup.
	MQTTSessionEnded MQTTSessionState = 3
)

// MQTTSessionEndReason is a durable application reason, not an MQTT wire code.
type MQTTSessionEndReason uint8

const (
	MQTTSessionExpired    MQTTSessionEndReason = 1
	MQTTSessionQuota      MQTTSessionEndReason = 2
	MQTTSessionRevoked    MQTTSessionEndReason = 3
	MQTTSessionCleanStart MQTTSessionEndReason = 4
	MQTTSessionExplicit   MQTTSessionEndReason = 5
	MQTTSessionSourceLost MQTTSessionEndReason = 6
)

// MQTTSession is one broker-scoped ClientID binding. It contains neither tokens
// nor protocol packets. Slot authority and takeover proof belong to the caller;
// persisting this row does not by itself authorize an owner to send or mutate.
type MQTTSession struct {
	Namespace string `json:"broker_namespace"`
	ClientID  string `json:"client_id"`
	// UID stays bound across expiry and Clean Start; ordinary writes cannot rebind it.
	UID string `json:"uid"`
	// Generation identifies the durable subscription/cursor/inflight lifetime.
	Generation uint64 `json:"generation"`
	// Revision is the independent CAS version of this row.
	Revision uint64 `json:"revision"`
	// OwnerGeneration fences every connection incarnation, including resumed sessions.
	OwnerGeneration uint64 `json:"owner_generation"`
	OwnerNodeID     uint64 `json:"owner_node_id"`
	// OwnerBootID prevents a restarted node from inheriting its prior connections.
	OwnerBootID  string `json:"owner_boot_id"`
	ConnectionID uint64 `json:"connection_id"`
	// LeaseUntilMS records the authority's execution lease; zero when not active.
	LeaseUntilMS int64            `json:"lease_until_ms"`
	State        MQTTSessionState `json:"state"`
	// SessionExpirySec is negotiated policy; zero ends the session on disconnect.
	SessionExpirySec uint32 `json:"session_expiry_sec"`
	// OfflineExpiresAtMS starts at disconnection, never at message publication.
	OfflineExpiresAtMS int64  `json:"offline_expires_at_ms"`
	DeviceFlag         uint8  `json:"device_flag"`
	ReceiveMaximum     uint16 `json:"receive_maximum"`
	MaxPacketBytes     uint32 `json:"max_packet_bytes"`
	// NextPacketID allocates the bounded window; it is not a business identifier.
	NextPacketID uint16 `json:"next_packet_id"`
	// NextDeliveryOrder preserves resend order independently of wrapping packet IDs.
	NextDeliveryOrder uint64 `json:"next_delivery_order"`
	// OutboundInflight is maintained only by atomic window mutations.
	OutboundInflight uint16 `json:"outbound_inflight"`
	// WindowLimit zero keeps the legacy/default limit. Receive Maximum also applies
	// to new admission; reconnect may retain more old rows than the new peer limit.
	WindowLimit     uint16 `json:"window_limit"`
	PendingMessages uint64 `json:"pending_messages"`
	PendingBytes    uint64 `json:"pending_bytes"`
	QuotaMessages   uint64 `json:"quota_messages"`
	QuotaBytes      uint64 `json:"quota_bytes"`
	// LastLifecycleDigest is an optional exact-retry receipt; ordinary CAS preserves it.
	LastLifecycleDigest string `json:"last_lifecycle_digest"`
	// WillGeneration references current Armed/Waiting state, not detached obligations.
	WillGeneration    uint64               `json:"will_generation"`
	TerminationReason MQTTSessionEndReason `json:"termination_reason"`
	UpdatedAtMS       int64                `json:"updated_at_ms"`
}

// MQTTSessionCASStatus describes a deterministic conditional write outcome.
type MQTTSessionCASStatus uint8

const (
	MQTTSessionCASApplied   MQTTSessionCASStatus = 1
	MQTTSessionCASUnchanged MQTTSessionCASStatus = 2
	MQTTSessionCASConflict  MQTTSessionCASStatus = 3
)

// MQTTSessionCASResult is meaningful only after the enclosing batch commits.
type MQTTSessionCASResult struct {
	Status          MQTTSessionCASStatus
	CurrentRevision uint64
}

// MQTTSessionDeadlineCursor contains the complete index tuple, including ties.
type MQTTSessionDeadlineCursor struct {
	DeadlineMS int64
	Namespace  string
	ClientID   string
}

func validateMQTTIdentity(value string, maxBytes int) error {
	if len(value) == 0 || len(value) > maxBytes || !utf8.ValidString(value) || strings.ContainsRune(value, 0) || strings.TrimSpace(value) == "" {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

// ValidateMQTTSession validates stored invariants, without consulting time or
// applying product auth/lease policy. Such decisions must remain deterministic
// in the Slot FSM and carry the caller's previously established authority proof.
func ValidateMQTTSession(r MQTTSession) error {
	if r.LastLifecycleDigest != "" {
		if len(r.LastLifecycleDigest) != 64 {
			return dberrors.ErrInvalidArgument
		}
		if _, err := hex.DecodeString(r.LastLifecycleDigest); err != nil {
			return dberrors.ErrInvalidArgument
		}
	}
	for _, value := range []string{r.Namespace, r.ClientID, r.UID} {
		if err := validateMQTTIdentity(value, 1024); err != nil {
			return err
		}
	}
	if err := validateMQTTIdentity(r.OwnerBootID, 128); err != nil {
		return err
	}
	if r.Generation == 0 || r.Revision == 0 || r.OwnerGeneration == 0 || r.OwnerNodeID == 0 || r.ConnectionID == 0 ||
		r.DeviceFlag > 2 || r.ReceiveMaximum == 0 || r.MaxPacketBytes == 0 || r.NextPacketID == 0 || r.NextDeliveryOrder == 0 ||
		r.OutboundInflight > MQTTMaxInflight || r.WindowLimit > MQTTMaxInflight || uint64(r.OutboundInflight) > r.PendingMessages ||
		r.QuotaMessages == 0 || r.QuotaBytes == 0 || r.UpdatedAtMS <= 0 || r.LeaseUntilMS < 0 || r.OfflineExpiresAtMS < 0 {
		return dberrors.ErrInvalidArgument
	}
	switch r.State {
	case MQTTSessionActive:
		if r.LeaseUntilMS <= 0 || r.OfflineExpiresAtMS != 0 || r.TerminationReason != 0 {
			return dberrors.ErrInvalidArgument
		}
	case MQTTSessionOffline:
		if r.LeaseUntilMS != 0 || r.OfflineExpiresAtMS <= 0 || r.SessionExpirySec == 0 || r.TerminationReason != 0 {
			return dberrors.ErrInvalidArgument
		}
	case MQTTSessionEnded:
		if r.LeaseUntilMS != 0 || r.OfflineExpiresAtMS != 0 || r.TerminationReason < MQTTSessionExpired || r.TerminationReason > MQTTSessionSourceLost {
			return dberrors.ErrInvalidArgument
		}
	default:
		return dberrors.ErrInvalidArgument
	}
	if r.State != MQTTSessionEnded && (r.PendingMessages > r.QuotaMessages || r.PendingBytes > r.QuotaBytes) {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

// CompareAndSwapMQTTSession stages one revision-conditional replacement. Exact
// retry succeeds only while the complete resulting row is still current. It does
// not delete old-generation children or discharge their Will/replay obligations.
func (b *Batch) CompareAndSwapMQTTSession(slot HashSlot, expected uint64, row MQTTSession) (*MQTTSessionCASResult, error) {
	if err := b.ensureOpen(); err != nil {
		return nil, err
	}
	if err := ValidateMQTTSession(row); err != nil {
		return nil, err
	}
	if expected == math.MaxUint64 || row.Revision != expected+1 {
		return nil, dberrors.ErrInvalidArgument
	}
	result := &MQTTSessionCASResult{}
	pk := mqttSessionPrimaryKey(row.Namespace, row.ClientID)
	b.addOp(slot, func(_ context.Context, state *batchCommitState, batch *engine.Batch) error {
		*result = MQTTSessionCASResult{Status: MQTTSessionCASConflict}
		old, found, err := loadUpdateRow(mqttSessionTable, state, slot, pk)
		if err != nil {
			return err
		}
		if found {
			result.CurrentRevision = old.Revision
			if old == row {
				result.Status = MQTTSessionCASUnchanged
				return nil
			}
			if old.Revision != expected || !validMQTTSessionTransition(old, row) || !validMQTTSessionGenericWillChange(old, row) {
				return nil
			}
		} else if row.WillGeneration != 0 || row.LastLifecycleDigest != "" || expected != 0 || row.Generation != 1 || row.OwnerGeneration != 1 || row.State != MQTTSessionActive || row.PendingMessages != 0 || row.PendingBytes != 0 || row.OutboundInflight != 0 {
			return nil
		}
		if err := stageUpdateRow(mqttSessionTable, state, batch, slot, row); err != nil {
			return err
		}
		*result = MQTTSessionCASResult{Status: MQTTSessionCASApplied, CurrentRevision: row.Revision}
		return nil
	})
	return result, nil
}

func validMQTTSessionTransition(old, next MQTTSession) bool {
	if old.UID != next.UID || next.Generation < old.Generation || next.Generation-old.Generation > 1 ||
		next.OwnerGeneration < old.OwnerGeneration || next.OwnerGeneration-old.OwnerGeneration > 1 {
		return false
	}
	newOwner := next.OwnerGeneration != old.OwnerGeneration
	if !newOwner && (next.OwnerNodeID != old.OwnerNodeID || next.OwnerBootID != old.OwnerBootID || next.ConnectionID != old.ConnectionID) {
		return false
	}
	if newOwner && next.State != MQTTSessionActive {
		return false
	}
	if next.Generation != old.Generation {
		return newOwner && next.State == MQTTSessionActive && next.PendingMessages == 0 && next.PendingBytes == 0 && next.OutboundInflight == 0
	}
	// Delivery commands own counters and allocators within a session lifetime.
	if next.PendingMessages != old.PendingMessages || next.PendingBytes != old.PendingBytes || next.OutboundInflight != old.OutboundInflight ||
		next.NextPacketID != old.NextPacketID || next.NextDeliveryOrder != old.NextDeliveryOrder || (old.State == MQTTSessionEnded && next.State != MQTTSessionEnded) {
		return false
	}
	if old.State == MQTTSessionOffline && next.State == MQTTSessionActive && !newOwner {
		return false
	}
	return true
}

// GetMQTTSession reads node storage. Cluster callers must first establish the
// correct Slot leader and a fresh durable-apply read barrier.
func (s *Shard) GetMQTTSession(ctx context.Context, namespace, clientID string) (MQTTSession, bool, error) {
	if err := validateMQTTIdentity(namespace, 1024); err != nil {
		return MQTTSession{}, false, err
	}
	if err := validateMQTTIdentity(clientID, 1024); err != nil {
		return MQTTSession{}, false, err
	}
	return mqttSessionTable.Get(ctx, s, mqttSessionPrimaryKey(namespace, clientID))
}

// ListMQTTSessionDeadlines pages active leases and offline expiry candidates.
// The caller revalidates authority/revision before acting and may stop at a
// future deadline. Ended bindings remain addressable but have no deadline entry.
func (s *Shard) ListMQTTSessionDeadlines(ctx context.Context, after MQTTSessionDeadlineCursor, limit int) ([]MQTTSession, MQTTSessionDeadlineCursor, bool, error) {
	if limit < 1 || limit > 256 {
		return nil, after, false, dberrors.ErrInvalidArgument
	}
	var cursor KeyParts
	if after != (MQTTSessionDeadlineCursor{}) {
		if after.DeadlineMS <= 0 || validateMQTTIdentity(after.Namespace, 1024) != nil || validateMQTTIdentity(after.ClientID, 1024) != nil {
			return nil, after, false, dberrors.ErrInvalidArgument
		}
		cursor = KeyParts{Int64Ordered(after.DeadlineMS), String(after.Namespace), String(after.ClientID)}
	}
	rows, next, done, err := mqttSessionTable.ScanIndex(ctx, s, 2, nil, cursor, limit)
	if err != nil {
		return nil, after, false, err
	}
	if len(next) == 3 {
		after = MQTTSessionDeadlineCursor{DeadlineMS: next[0].I64, Namespace: next[1].S, ClientID: next[2].S}
	}
	return rows, after, done, nil
}

func mqttSessionPrimaryKey(namespace, clientID string) KeyParts {
	return KeyParts{String(namespace), String(clientID)}
}

func mqttSessionDeadline(row MQTTSession) int64 {
	switch row.State {
	case MQTTSessionActive:
		return row.LeaseUntilMS
	case MQTTSessionOffline:
		return row.OfflineExpiresAtMS
	default:
		return 0
	}
}

// validMQTTSessionGenericWillChange prevents split Session/Will lifecycle writes.
func validMQTTSessionGenericWillChange(old, next MQTTSession) bool {
	if old.WillGeneration != next.WillGeneration || old.LastLifecycleDigest != next.LastLifecycleDigest {
		return false
	}
	if old.WillGeneration == 0 {
		return true
	}
	return old.Generation == next.Generation && old.OwnerGeneration == next.OwnerGeneration && old.OwnerNodeID == next.OwnerNodeID && old.OwnerBootID == next.OwnerBootID && old.ConnectionID == next.ConnectionID && old.State == next.State && old.SessionExpirySec == next.SessionExpirySec && old.OfflineExpiresAtMS == next.OfflineExpiresAtMS
}
