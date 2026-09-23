package meta

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

// MQTTLifecycleEvent identifies a deterministic Session/Will decision.
type MQTTLifecycleEvent uint8

const (
	MQTTLifecycleInstallWill        MQTTLifecycleEvent = 1
	MQTTLifecycleConnect            MQTTLifecycleEvent = 2
	MQTTLifecycleNormalDisconnect   MQTTLifecycleEvent = 3
	MQTTLifecycleDisconnectWithWill MQTTLifecycleEvent = 4
	MQTTLifecycleEnd                MQTTLifecycleEvent = 5
	MQTTLifecycleWillDue            MQTTLifecycleEvent = 6
)

// MQTTLifecycleMutation fences the previous owner and supplies a proposed next
// Session. WillGeneration and LastLifecycleDigest are derived by the transaction.
// Callers must establish authentication and old-owner isolation before Connect.
type MQTTLifecycleMutation struct {
	ExpectedRevision   uint64             `json:"expected_revision"`
	ExpectedGeneration uint64             `json:"expected_generation"`
	OwnerGeneration    uint64             `json:"owner_generation"`
	OwnerNodeID        uint64             `json:"owner_node_id"`
	OwnerBootID        string             `json:"owner_boot_id"`
	ConnectionID       uint64             `json:"connection_id"`
	Event              MQTTLifecycleEvent `json:"event"`
	CleanStart         bool               `json:"clean_start"`
	Session            MQTTSession        `json:"session"`
	Will               *MQTTWill          `json:"will,omitempty"`
}

// MQTTLifecycleResult describes one committed atomic decision, not socket authority.
type MQTTLifecycleResult struct {
	Status          MQTTSessionCASStatus
	CurrentRevision uint64
	WillGeneration  uint64
}

// ValidateMQTTLifecycleMutation bounds input without reading current authority.
func ValidateMQTTLifecycleMutation(m MQTTLifecycleMutation) error {
	if m.ExpectedRevision == math.MaxUint64 || m.Session.Revision != m.ExpectedRevision+1 || m.Session.WillGeneration != 0 || m.Session.LastLifecycleDigest != "" || m.Event < MQTTLifecycleInstallWill || m.Event > MQTTLifecycleWillDue || m.CleanStart && m.Event != MQTTLifecycleConnect {
		return dberrors.ErrInvalidArgument
	}
	if err := ValidateMQTTSession(m.Session); err != nil {
		return err
	}
	if m.ExpectedRevision == 0 {
		if m.Event != MQTTLifecycleConnect || m.ExpectedGeneration != 0 || m.OwnerGeneration != 0 || m.OwnerNodeID != 0 || m.OwnerBootID != "" || m.ConnectionID != 0 {
			return dberrors.ErrInvalidArgument
		}
	} else if m.ExpectedGeneration == 0 || m.OwnerGeneration == 0 || m.OwnerNodeID == 0 || m.ConnectionID == 0 || validateMQTTIdentity(m.OwnerBootID, 128) != nil {
		return dberrors.ErrInvalidArgument
	}
	if m.Will != nil {
		if m.Event != MQTTLifecycleConnect && m.Event != MQTTLifecycleInstallWill {
			return dberrors.ErrInvalidArgument
		}
		if err := ValidateMQTTWill(*m.Will); err != nil {
			return err
		}
	} else if m.Event == MQTTLifecycleInstallWill {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

// ApplyMQTTLifecycle resolves the previous Will, optionally installs a new one,
// and replaces the Session in one Slot batch. All conditional checks run before
// staging any row. The receipt proves exact retry across an owner change.
func (b *Batch) ApplyMQTTLifecycle(slot HashSlot, m MQTTLifecycleMutation) (*MQTTLifecycleResult, error) {
	if err := b.ensureOpen(); err != nil {
		return nil, err
	}
	if err := ValidateMQTTLifecycleMutation(m); err != nil {
		return nil, err
	}
	if m.Will != nil {
		owned := *m.Will
		owned.Payload = bytes.Clone(owned.Payload)
		owned.PublicationMetadata = bytes.Clone(owned.PublicationMetadata)
		m.Will = &owned
	}
	canonical, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(append([]byte("mqtt-lifecycle-v1\x00"), canonical...))
	digest := hex.EncodeToString(sum[:])
	result := &MQTTLifecycleResult{}
	b.addOp(slot, func(_ context.Context, state *batchCommitState, batch *engine.Batch) error {
		*result = MQTTLifecycleResult{Status: MQTTSessionCASConflict}
		old, found, err := loadUpdateRow(mqttSessionTable, state, slot, mqttSessionPrimaryKey(m.Session.Namespace, m.Session.ClientID))
		if err != nil {
			return err
		}
		if found {
			*result = mqttLifecycleResult(MQTTSessionCASConflict, old)
			if old.Revision == m.ExpectedRevision+1 && old.LastLifecycleDigest == digest {
				*result = mqttLifecycleResult(MQTTSessionCASUnchanged, old)
				return nil
			}
			if old.Revision != m.ExpectedRevision || old.Generation != m.ExpectedGeneration || old.OwnerGeneration != m.OwnerGeneration || old.OwnerNodeID != m.OwnerNodeID || old.OwnerBootID != m.OwnerBootID || old.ConnectionID != m.ConnectionID {
				return nil
			}
		} else if m.ExpectedRevision != 0 {
			return nil
		}
		next := m.Session
		if !validMQTTLifecycleSession(old, found, m) {
			return nil
		}
		var resolved *MQTTWill
		if found && old.WillGeneration != 0 {
			live, err := loadMQTTReferencedWill(state, slot, old)
			if err != nil {
				return err
			}
			var ok bool
			resolved, ok = resolveMQTTLifecycleWill(old, next, m.Event, live)
			if !ok {
				return nil
			}
			if resolved.Stage == MQTTWillWaiting {
				next.WillGeneration = old.WillGeneration
			}
		} else if m.Event == MQTTLifecycleWillDue {
			return nil
		}
		if m.Will != nil {
			w := *m.Will
			if !validMQTTLifecycleNewWill(next, w) {
				return nil
			}
			_, exists, err := loadUpdateRow(mqttWillTable, state, slot, mqttWillPrimaryKey(w.Key))
			if err != nil {
				return err
			}
			if exists {
				return nil
			}
			next.WillGeneration = w.Key.WillGeneration
		}
		next.LastLifecycleDigest = digest
		if err := ValidateMQTTSession(next); err != nil {
			return err
		}
		if resolved != nil {
			if err := stageUpdateRow(mqttWillTable, state, batch, slot, *resolved); err != nil {
				return err
			}
		}
		if m.Will != nil {
			if err := stageUpdateRow(mqttWillTable, state, batch, slot, *m.Will); err != nil {
				return err
			}
		}
		if err := stageUpdateRow(mqttSessionTable, state, batch, slot, next); err != nil {
			return err
		}
		*result = mqttLifecycleResult(MQTTSessionCASApplied, next)
		return nil
	})
	return result, nil
}

func mqttLifecycleResult(status MQTTSessionCASStatus, s MQTTSession) MQTTLifecycleResult {
	return MQTTLifecycleResult{Status: status, CurrentRevision: s.Revision, WillGeneration: s.WillGeneration}
}

// validMQTTLifecycleSession limits each event to its allowed Session fields.
// Expired active leases first require a close decision, so a late reconnect
// cannot treat a long-dead owner's Will as a fresh cancellable configuration.
func validMQTTLifecycleSession(old MQTTSession, found bool, m MQTTLifecycleMutation) bool {
	n := m.Session
	at := n.UpdatedAtMS
	if !found {
		return m.Event == MQTTLifecycleConnect && n.Generation == 1 && n.OwnerGeneration == 1 && n.State == MQTTSessionActive && n.LeaseUntilMS > at && n.PendingMessages == 0 && n.PendingBytes == 0 && n.OutboundInflight == 0 && n.NextPacketID == 1 && n.NextDeliveryOrder == 1
	}
	if at < old.UpdatedAtMS || !validMQTTSessionTransition(old, n) {
		return false
	}
	expected := old
	expected.Revision = n.Revision
	expected.UpdatedAtMS = at
	expected.WillGeneration = 0
	expected.LastLifecycleDigest = ""
	switch m.Event {
	case MQTTLifecycleInstallWill:
		return old.State == MQTTSessionActive && old.LeaseUntilMS > at && old.WillGeneration == 0 && n == expected
	case MQTTLifecycleConnect:
		if n.State != MQTTSessionActive || old.OwnerGeneration == math.MaxUint64 || n.OwnerGeneration != old.OwnerGeneration+1 || n.LeaseUntilMS <= at || old.State == MQTTSessionActive && old.LeaseUntilMS <= at {
			return false
		}
		fresh := m.CleanStart || old.State == MQTTSessionEnded || old.State == MQTTSessionOffline && old.OfflineExpiresAtMS <= at || old.State == MQTTSessionActive && old.SessionExpirySec == 0
		if fresh {
			return old.Generation != math.MaxUint64 && n.Generation == old.Generation+1 && n.NextPacketID == 1 && n.NextDeliveryOrder == 1
		}
		return n.Generation == old.Generation
	case MQTTLifecycleNormalDisconnect, MQTTLifecycleDisconnectWithWill:
		if old.State != MQTTSessionActive || old.SessionExpirySec == 0 && n.SessionExpirySec != 0 {
			return false
		}
		expected.SessionExpirySec = n.SessionExpirySec
		expected.LeaseUntilMS = 0
		if n.SessionExpirySec == 0 {
			expected.State = MQTTSessionEnded
			expected.OfflineExpiresAtMS = 0
			expected.TerminationReason = MQTTSessionExpired
		} else {
			expiry, ok := mqttLifecycleDeadline(at, n.SessionExpirySec)
			if !ok {
				return false
			}
			expected.State = MQTTSessionOffline
			expected.OfflineExpiresAtMS = expiry
			expected.TerminationReason = 0
		}
		return n == expected
	case MQTTLifecycleEnd:
		if old.State == MQTTSessionEnded {
			return false
		}
		if n.TerminationReason == MQTTSessionExpired && (old.State != MQTTSessionOffline || at < old.OfflineExpiresAtMS) {
			return false
		}
		expected.State = MQTTSessionEnded
		expected.LeaseUntilMS = 0
		expected.OfflineExpiresAtMS = 0
		expected.TerminationReason = n.TerminationReason
		return n == expected
	case MQTTLifecycleWillDue:
		if old.State != MQTTSessionOffline || old.WillGeneration == 0 {
			return false
		}
		if at >= old.OfflineExpiresAtMS {
			expected.State = MQTTSessionEnded
			expected.OfflineExpiresAtMS = 0
			expected.TerminationReason = MQTTSessionExpired
		}
		return n == expected
	}
	return false
}

func validMQTTLifecycleNewWill(s MQTTSession, w MQTTWill) bool {
	return s.State == MQTTSessionActive && w.Key.Namespace == s.Namespace && w.Key.ClientID == s.ClientID && w.Key.SessionGeneration == s.Generation && w.Key.WillGeneration == s.Revision && w.UID == s.UID && w.OwnerGeneration == s.OwnerGeneration && w.OwnerNodeID == s.OwnerNodeID && w.OwnerBootID == s.OwnerBootID && w.ConnectionID == s.ConnectionID && w.Revision == 1 && w.DecisionRevision == s.Revision && w.Stage == MQTTWillArmed && w.UpdatedAtMS == s.UpdatedAtMS
}

func mqttLifecycleDeadline(at int64, seconds uint32) (int64, bool) {
	delta := int64(seconds) * 1000
	if at <= 0 || at > math.MaxInt64-delta {
		return 0, false
	}
	return at + delta, true
}

// loadMQTTReferencedWill treats broken references as corruption, never as absence
// of a publication duty. Detached Ready/execution rows do not use this reference.
func loadMQTTReferencedWill(state *batchCommitState, slot HashSlot, s MQTTSession) (MQTTWill, error) {
	key := MQTTWillKey{Namespace: s.Namespace, ClientID: s.ClientID, SessionGeneration: s.Generation, WillGeneration: s.WillGeneration}
	w, found, err := loadUpdateRow(mqttWillTable, state, slot, mqttWillPrimaryKey(key))
	if err != nil {
		return w, err
	}
	if !found || w.UID != s.UID || w.OwnerGeneration != s.OwnerGeneration || w.OwnerNodeID != s.OwnerNodeID || w.OwnerBootID != s.OwnerBootID || w.ConnectionID != s.ConnectionID || w.DecisionRevision > s.Revision || s.State == MQTTSessionActive && w.Stage != MQTTWillArmed || s.State == MQTTSessionOffline && w.Stage != MQTTWillWaiting || s.State == MQTTSessionEnded {
		return MQTTWill{}, dberrors.ErrCorruptValue
	}
	return w, nil
}

// resolveMQTTLifecycleWill computes one old-Will update without staging writes.
// The same function resolves quota termination inside backlog accounting.
func resolveMQTTLifecycleWill(old, next MQTTSession, event MQTTLifecycleEvent, w MQTTWill) (*MQTTWill, bool) {
	if w.Revision == math.MaxUint64 || next.UpdatedAtMS < w.UpdatedAtMS {
		return nil, false
	}
	r := w
	r.Revision++
	r.DecisionRevision = next.Revision
	r.UpdatedAtMS = next.UpdatedAtMS
	at := next.UpdatedAtMS
	switch event {
	case MQTTLifecycleNormalDisconnect:
		if w.Stage != MQTTWillArmed {
			return nil, false
		}
		r.Stage = MQTTWillCancelled
		r.CancelReason = MQTTWillNormalDisconnect
	case MQTTLifecycleDisconnectWithWill:
		if w.Stage != MQTTWillArmed {
			return nil, false
		}
		r.DisconnectedAtMS = at
		r.DueAtMS = at
		if next.State != MQTTSessionEnded {
			due, ok := mqttLifecycleDeadline(at, w.DelaySeconds)
			if !ok {
				return nil, false
			}
			r.DueAtMS = min(due, next.OfflineExpiresAtMS)
		}
		r.Stage = MQTTWillReady
		if r.DueAtMS > at {
			r.Stage = MQTTWillWaiting
		}
	case MQTTLifecycleConnect:
		sameSession := next.Generation == old.Generation
		if sameSession && w.DelaySeconds > 0 && (w.Stage == MQTTWillArmed || w.Stage == MQTTWillWaiting && at < w.DueAtMS) {
			r.Stage = MQTTWillCancelled
			r.CancelReason = MQTTWillSessionResumed
		} else {
			mqttLifecycleWillReady(&r, at)
		}
	case MQTTLifecycleEnd:
		mqttLifecycleWillReady(&r, at)
	case MQTTLifecycleWillDue:
		if w.Stage != MQTTWillWaiting || at < w.DueAtMS {
			return nil, false
		}
		r.Stage = MQTTWillReady
	default:
		return nil, false
	}
	if ValidateMQTTWill(r) != nil || !validMQTTWillTransition(w, r) {
		return nil, false
	}
	return &r, true
}

func mqttLifecycleWillReady(w *MQTTWill, at int64) {
	if w.Stage == MQTTWillArmed {
		w.DisconnectedAtMS = at
		w.DueAtMS = at
	} else {
		w.DueAtMS = min(w.DueAtMS, at)
	}
	w.Stage = MQTTWillReady
}
