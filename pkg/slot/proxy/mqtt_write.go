package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
)

// CompareAndSwapMQTTSession persists an owner-fenced Session mutation. Storage
// CAS is not proof that the previous connection has stopped executing.
func (s *Store) CompareAndSwapMQTTSession(ctx context.Context, expected uint64, row metadb.MQTTSession) (out metadb.MQTTSessionCASResult, err error) {
	cmd, err := metafsm.EncodeMQTTSessionCASCommand(expected, row)
	if err != nil {
		return out, err
	}
	key, err := MQTTSessionRoutingKey(row.Namespace, row.ClientID)
	if err != nil {
		return out, err
	}
	if err = s.proposeMQTT(ctx, key, cmd, &out); err == nil {
		err = validateMQTTCASResult(out.Status, out.CurrentRevision, expected)
	}
	if err != nil {
		return metadb.MQTTSessionCASResult{}, err
	}
	return out, nil
}

// ApplyMQTTLifecycle commits the Session and its Will decision together.
// Authentication and previous-owner isolation must be proved by the caller.
func (s *Store) ApplyMQTTLifecycle(ctx context.Context, m metadb.MQTTLifecycleMutation) (out metadb.MQTTLifecycleResult, err error) {
	cmd, err := metafsm.EncodeMQTTLifecycleCommand(m)
	if err != nil {
		return out, err
	}
	key, err := MQTTSessionRoutingKey(m.Session.Namespace, m.Session.ClientID)
	if err != nil {
		return out, err
	}
	if err = s.proposeMQTT(ctx, key, cmd, &out); err == nil {
		err = validateMQTTCASResult(out.Status, out.CurrentRevision, m.ExpectedRevision)
	}
	if err == nil && (out.WillGeneration > out.CurrentRevision || out.Status != metadb.MQTTSessionCASConflict && m.Will != nil && out.WillGeneration != m.Will.Key.WillGeneration) {
		err = metadb.ErrCorruptValue
	}
	if err != nil {
		return metadb.MQTTLifecycleResult{}, err
	}
	return out, nil
}

// MutateMQTTSubscription shares the Session Slot and its revision/owner fence.
func (s *Store) MutateMQTTSubscription(ctx context.Context, m metadb.MQTTSubscriptionMutation) (out metadb.MQTTSessionCASResult, err error) {
	cmd, err := metafsm.EncodeMQTTSubscriptionCommand(m)
	if err != nil {
		return out, err
	}
	key, err := MQTTSessionRoutingKey(m.Subscription.Namespace, m.Subscription.ClientID)
	if err != nil {
		return out, err
	}
	if err = s.proposeMQTT(ctx, key, cmd, &out); err == nil {
		err = validateMQTTCASResult(out.Status, out.CurrentRevision, m.ExpectedRevision)
	}
	if err != nil {
		return metadb.MQTTSessionCASResult{}, err
	}
	return out, nil
}

// MutateMQTTDeliveryCursor atomically persists accounting and quota termination.
func (s *Store) MutateMQTTDeliveryCursor(ctx context.Context, m metadb.MQTTDeliveryCursorMutation) (out metadb.MQTTDeliveryCursorResult, err error) {
	cmd, err := metafsm.EncodeMQTTDeliveryCursorCommand(m)
	if err != nil {
		return out, err
	}
	key, err := MQTTSessionRoutingKey(m.Key.Namespace, m.Key.ClientID)
	if err != nil {
		return out, err
	}
	if err = s.proposeMQTT(ctx, key, cmd, &out); err == nil {
		err = validateMQTTCASResult(out.Status, out.CurrentRevision, m.ExpectedRevision)
	}
	if err == nil {
		switch out.SessionState {
		case 0:
			if out.Status != metadb.MQTTSessionCASConflict || out.CurrentRevision != 0 || out.TerminationReason != 0 {
				err = metadb.ErrCorruptValue
			}
		case metadb.MQTTSessionActive, metadb.MQTTSessionOffline:
			if out.CurrentRevision == 0 || out.TerminationReason != 0 {
				err = metadb.ErrCorruptValue
			}
		case metadb.MQTTSessionEnded:
			if out.CurrentRevision == 0 || out.TerminationReason < metadb.MQTTSessionExpired || out.TerminationReason > metadb.MQTTSessionSourceLost {
				err = metadb.ErrCorruptValue
			}
		default:
			err = metadb.ErrCorruptValue
		}
	}
	if err != nil {
		return metadb.MQTTDeliveryCursorResult{}, err
	}
	return out, nil
}

// MutateMQTTWindow persists admission before network delivery and ACK before
// advancement. A returned admission is not permission to bypass authorization.
func (s *Store) MutateMQTTWindow(ctx context.Context, m metadb.MQTTWindowMutation) (out metadb.MQTTWindowResult, err error) {
	cmd, err := metafsm.EncodeMQTTWindowCommand(m)
	if err != nil {
		return out, err
	}
	key, err := MQTTSessionRoutingKey(m.Key.Namespace, m.Key.ClientID)
	if err != nil {
		return out, err
	}
	if err = s.proposeMQTT(ctx, key, cmd, &out); err == nil {
		switch out.Status {
		case metadb.MQTTWindowApplied, metadb.MQTTWindowUnchanged:
			if out.CurrentRevision != m.ExpectedRevision+1 {
				err = metadb.ErrCorruptValue
			}
			switch m.Op {
			case metadb.MQTTWindowAdmit:
				if out.PacketID == 0 || out.DeliveryOrder == 0 {
					err = metadb.ErrCorruptValue
				}
			case metadb.MQTTWindowAck:
				if out.PacketID != m.PacketID || out.DeliveryOrder != m.DeliveryOrder {
					err = metadb.ErrCorruptValue
				}
			case metadb.MQTTWindowAdvance:
				if out.PacketID != 0 || out.DeliveryOrder != 0 {
					err = metadb.ErrCorruptValue
				}
			}
		case metadb.MQTTWindowConflict, metadb.MQTTWindowFull:
			if out.PacketID != 0 || out.DeliveryOrder != 0 || out.Status == metadb.MQTTWindowFull && out.CurrentRevision != m.ExpectedRevision {
				err = metadb.ErrCorruptValue
			}
		default:
			err = metadb.ErrCorruptValue
		}
	}
	if err != nil {
		return metadb.MQTTWindowResult{}, err
	}
	return out, nil
}

// CompareAndSwapMQTTSourceBinding writes to source/UID authority, independently
// of the Session. Cross-Slot activation and protection proofs remain caller work.
func (s *Store) CompareAndSwapMQTTSourceBinding(ctx context.Context, expected uint64, row metadb.MQTTSourceBinding) (out metadb.MQTTSourceBindingResult, err error) {
	cmd, err := metafsm.EncodeMQTTSourceBindingCommand(expected, row)
	if err != nil {
		return out, err
	}
	key, err := MQTTSourceRoutingKey(row.Key.Owner)
	if err != nil {
		return out, err
	}
	if err = s.proposeMQTT(ctx, key, cmd, &out); err == nil {
		err = validateMQTTCASResult(out.Status, out.CurrentRevision, expected)
	}
	if err != nil {
		return metadb.MQTTSourceBindingResult{}, err
	}
	return out, nil
}

// RetireMQTTSourceBinding deletes one acknowledged Removed tombstone on the
// binding owner's Slot. The caller must already hold fresh Session-Slot proof
// that every lifetime through closedThrough has ended.
func (s *Store) RetireMQTTSourceBinding(ctx context.Context, key metadb.MQTTSourceBindingKey, expected, closedThrough uint64) (out metadb.MQTTSourceBindingResult, err error) {
	cmd, err := metafsm.EncodeMQTTSourceBindingRetireCommand(key, expected, closedThrough)
	if err != nil {
		return out, err
	}
	route, err := MQTTSourceRoutingKey(key.Owner)
	if err != nil {
		return out, err
	}
	if err = s.proposeMQTT(ctx, route, cmd, &out); err == nil {
		// Applied deletes the row, so no revision survives; conflicts echo the current row.
		if out.Status != metadb.MQTTSessionCASConflict && (out.Status != metadb.MQTTSessionCASApplied || out.CurrentRevision != 0) {
			err = metadb.ErrCorruptValue
		}
	}
	if err != nil {
		return metadb.MQTTSourceBindingResult{}, err
	}
	return out, nil
}

// ClearMQTTReplayMarker removes one Channel owner's replay-discovery marker on
// the owner's Slot. Conflict means no marker or a binding row still exists.
func (s *Store) ClearMQTTReplayMarker(ctx context.Context, owner metadb.MQTTBindingOwner) (out metadb.MQTTSourceBindingResult, err error) {
	cmd, err := metafsm.EncodeMQTTReplayMarkerClearCommand(owner)
	if err != nil {
		return out, err
	}
	route, err := MQTTSourceRoutingKey(owner)
	if err != nil {
		return out, err
	}
	if err = s.proposeMQTT(ctx, route, cmd, &out); err == nil && out.Status != metadb.MQTTSessionCASApplied && out.Status != metadb.MQTTSessionCASConflict {
		err = metadb.ErrCorruptValue
	}
	if err != nil {
		return metadb.MQTTSourceBindingResult{}, err
	}
	return out, nil
}

// CompareAndSwapMQTTWill retains detached obligations on the original Session
// Slot, even when that Session no longer exists or has a newer lifetime.
func (s *Store) CompareAndSwapMQTTWill(ctx context.Context, expected uint64, row metadb.MQTTWill) (out metadb.MQTTWillResult, err error) {
	cmd, err := metafsm.EncodeMQTTWillCommand(expected, row)
	if err != nil {
		return out, err
	}
	key, err := MQTTSessionRoutingKey(row.Key.Namespace, row.Key.ClientID)
	if err != nil {
		return out, err
	}
	if err = s.proposeMQTT(ctx, key, cmd, &out); err == nil {
		err = validateMQTTCASResult(out.Status, out.CurrentRevision, expected)
	}
	if err != nil {
		return metadb.MQTTWillResult{}, err
	}
	return out, nil
}

func validateMQTTCASResult(status metadb.MQTTSessionCASStatus, revision, expected uint64) error {
	switch status {
	case metadb.MQTTSessionCASApplied, metadb.MQTTSessionCASUnchanged:
		if revision == expected+1 {
			return nil
		}
	case metadb.MQTTSessionCASConflict:
		return nil
	}
	return metadb.ErrCorruptValue
}

func (s *Store) proposeMQTT(ctx context.Context, key string, cmd []byte, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.cluster == nil {
		return errSlotNotFound
	}
	// No compatibility fallback: a write without its deterministic apply result
	// is ambiguous and must never be submitted through a result-less port.
	p, ok := s.cluster.(hashSlotResultProposer)
	if !ok {
		return fmt.Errorf("metastore: MQTT requires committed proposal results")
	}
	revision := s.cluster.HashSlotTableVersion()
	slot, hs := s.cluster.SlotForKey(key), s.cluster.HashSlotForKey(key)
	found := false
	for _, owned := range s.cluster.HashSlotsOf(slot) {
		if owned == hs {
			found = true
			break
		}
	}
	if !found || revision != s.cluster.HashSlotTableVersion() {
		return metadb.ErrStaleMeta
	}
	body, err := p.ProposeWithHashSlotResult(ctx, slot, hs, cmd)
	if err != nil {
		return err
	}
	if string(body) == metafsm.ApplyResultHashSlotFenced || string(body) == metafsm.ApplyResultStaleMeta {
		return metadb.ErrStaleMeta
	}
	if err := decodeMQTTJSON(body, 4096, out); err != nil {
		return fmt.Errorf("metastore: malformed MQTT apply result: %w", metadb.ErrCorruptValue)
	}
	return nil
}

func decodeMQTTJSON(body []byte, limit int, out any) error {
	if len(body) > limit {
		return metadb.ErrInvalidArgument
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 || len(body) > limit || body[0] != '{' {
		return metadb.ErrInvalidArgument
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return metadb.ErrInvalidArgument
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return metadb.ErrInvalidArgument
	}
	return nil
}

// CompareAndSwapMQTTInboxAdmission persists bounded progress at Channel authority.
// Storage verifies the current runtime generation; remote source proof is separate.
func (s *Store) CompareAndSwapMQTTInboxAdmission(ctx context.Context, expected uint64, row metadb.MQTTInboxAdmission) (out metadb.MQTTInboxAdmissionResult, err error) {
	cmd, err := metafsm.EncodeMQTTInboxAdmissionCommand(expected, row)
	if err != nil {
		return out, err
	}
	if err = s.proposeMQTT(ctx, row.ChannelID, cmd, &out); err == nil {
		err = validateMQTTCASResult(out.Status, out.CurrentRevision, expected)
	}
	if err != nil {
		return metadb.MQTTInboxAdmissionResult{}, err
	}
	return out, nil
}

// ReclaimMQTTSession routes bounded ended-lifetime cleanup to Session authority.
// Partial pages require fresh revisions. A completion witness may survive later
// Session changes, unlike an exact CAS retry receipt.
func (s *Store) ReclaimMQTTSession(ctx context.Context, m metadb.MQTTSessionReclamation) (out metadb.MQTTSessionReclamationResult, err error) {
	cmd, err := metafsm.EncodeMQTTSessionReclamationCommand(m)
	if err != nil {
		return out, err
	}
	key, err := MQTTSessionRoutingKey(m.Namespace, m.ClientID)
	if err != nil {
		return out, err
	}
	if err = s.proposeMQTT(ctx, key, cmd, &out); err == nil {
		err = validateMQTTReclamationResult(m, out)
	}
	if err != nil {
		return metadb.MQTTSessionReclamationResult{}, err
	}
	return out, nil
}
func validateMQTTReclamationResult(m metadb.MQTTSessionReclamation, r metadb.MQTTSessionReclamationResult) error {
	if r.RemovedSubscriptions < 0 || r.RemovedSubscriptions > 64 {
		return metadb.ErrCorruptValue
	}
	switch r.Status {
	case metadb.MQTTSessionCASApplied:
		if r.CurrentRevision == m.ExpectedRevision+1 && (r.Done && r.ReclaimedThroughGeneration == m.ThroughGeneration || !r.Done && r.ReclaimedThroughGeneration < m.ThroughGeneration) {
			return nil
		}
	case metadb.MQTTSessionCASUnchanged:
		if r.CurrentRevision > 0 && r.Done && r.RemovedSubscriptions == 0 && r.ReclaimedThroughGeneration >= m.ThroughGeneration {
			return nil
		}
	case metadb.MQTTSessionCASConflict:
		if !r.Done && r.RemovedSubscriptions == 0 && r.ReclaimedThroughGeneration < m.ThroughGeneration && (r.CurrentRevision != 0 || r.ReclaimedThroughGeneration == 0) {
			return nil
		}
	}
	return metadb.ErrCorruptValue
}
