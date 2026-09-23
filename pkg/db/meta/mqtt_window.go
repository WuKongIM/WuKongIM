package meta

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

// MQTTWindowOp identifies an atomic window operation.
type MQTTWindowOp uint8

const (
	MQTTWindowAdmit   MQTTWindowOp = 1
	MQTTWindowAck     MQTTWindowOp = 2
	MQTTWindowAdvance MQTTWindowOp = 3
)

// MQTTWindowStatus distinguishes flow control from a stale conditional request.
type MQTTWindowStatus uint8

const (
	MQTTWindowApplied   MQTTWindowStatus = 1
	MQTTWindowUnchanged MQTTWindowStatus = 2
	MQTTWindowConflict  MQTTWindowStatus = 3
	MQTTWindowFull      MQTTWindowStatus = 4
)

// MQTTWindowMutation carries the initiating connection fence. Publication is
// present only for admission; packet/order only for ACK; covered range and
// released accounting only for advancement. Source coverage/content durability
// and current permission must already be proved by the caller.
type MQTTWindowMutation struct {
	Key              MQTTDeliveryCursorKey   `json:"key"`
	ExpectedRevision uint64                  `json:"expected_revision"`
	OwnerGeneration  uint64                  `json:"owner_generation"`
	OwnerNodeID      uint64                  `json:"owner_node_id"`
	OwnerBootID      string                  `json:"owner_boot_id"`
	ConnectionID     uint64                  `json:"connection_id"`
	Op               MQTTWindowOp            `json:"op"`
	Publication      MQTTInflightPublication `json:"publication"`
	PacketID         uint16                  `json:"packet_id"`
	DeliveryOrder    uint64                  `json:"delivery_order"`
	Through          uint64                  `json:"through"`
	ReleasedMessages uint64                  `json:"released_messages"`
	ReleasedBytes    uint64                  `json:"released_bytes"`
	UpdatedAtMS      int64                   `json:"updated_at_ms"`
}

// MQTTWindowResult is usable only after durable commit. Applied or exact-retry
// admission returns the same packet/order; it is not itself permission to send.
type MQTTWindowResult struct {
	Status          MQTTWindowStatus
	CurrentRevision uint64
	PacketID        uint16
	DeliveryOrder   uint64
}

// ValidateMQTTWindowMutation rejects ambiguous or unbounded operation payloads.
func ValidateMQTTWindowMutation(m MQTTWindowMutation) error {
	if validateMQTTDeliveryCursorKey(m.Key) != nil || m.ExpectedRevision == 0 || m.ExpectedRevision == math.MaxUint64 ||
		m.OwnerGeneration == 0 || m.OwnerNodeID == 0 || validateMQTTIdentity(m.OwnerBootID, 128) != nil || m.ConnectionID == 0 || m.UpdatedAtMS <= 0 {
		return dberrors.ErrInvalidArgument
	}
	switch m.Op {
	case MQTTWindowAdmit:
		if validateMQTTInflightPublication(m.Publication) != nil || m.PacketID != 0 || m.DeliveryOrder != 0 || m.Through != 0 || m.ReleasedMessages != 0 || m.ReleasedBytes != 0 {
			return dberrors.ErrInvalidArgument
		}
	case MQTTWindowAck:
		if m.Publication != (MQTTInflightPublication{}) || m.PacketID == 0 || m.DeliveryOrder == 0 || m.Through != 0 || m.ReleasedMessages != 0 || m.ReleasedBytes != 0 {
			return dberrors.ErrInvalidArgument
		}
	case MQTTWindowAdvance:
		if m.Publication != (MQTTInflightPublication{}) || m.PacketID != 0 || m.DeliveryOrder != 0 || (m.ReleasedMessages == 0 && m.ReleasedBytes != 0) {
			return dberrors.ErrInvalidArgument
		}
	default:
		return dberrors.ErrInvalidArgument
	}
	return nil
}

// MutateMQTTWindow commits exchange, source progress and aggregate accounting
// together. ACK touches only adjacent outstanding rows. No timer-driven resend
// is scheduled here; a resumed runtime reads durable exchanges in send order.
func (b *Batch) MutateMQTTWindow(slot HashSlot, m MQTTWindowMutation) (*MQTTWindowResult, error) {
	if err := b.ensureOpen(); err != nil {
		return nil, err
	}
	if err := ValidateMQTTWindowMutation(m); err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(append([]byte("mqtt-window-v1\x00"), canonical...))
	digest := hex.EncodeToString(sum[:])
	result := &MQTTWindowResult{}
	b.addOp(slot, func(_ context.Context, state *batchCommitState, batch *engine.Batch) error {
		*result = MQTTWindowResult{Status: MQTTWindowConflict}
		session, found, err := loadUpdateRow(mqttSessionTable, state, slot, mqttSessionPrimaryKey(m.Key.Namespace, m.Key.ClientID))
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		result.CurrentRevision = session.Revision
		if session.Generation != m.Key.SessionGeneration || session.OwnerGeneration != m.OwnerGeneration || session.OwnerNodeID != m.OwnerNodeID ||
			session.OwnerBootID != m.OwnerBootID || session.ConnectionID != m.ConnectionID {
			return nil
		}
		cursor, found, err := loadUpdateRow(mqttDeliveryCursorTable, state, slot, mqttDeliveryCursorPrimaryKey(m.Key))
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		if cursor.Revision == m.ExpectedRevision+1 && session.Revision == cursor.Revision && cursor.LastMutationDigest == digest {
			*result = MQTTWindowResult{Status: MQTTWindowUnchanged, CurrentRevision: session.Revision, PacketID: cursor.LastWindowPacketID, DeliveryOrder: cursor.LastWindowDeliveryOrder}
			return nil
		}
		if session.Revision != m.ExpectedRevision || session.State != MQTTSessionActive {
			return nil
		}
		if session.OutboundInflight < cursor.InflightCount || session.PendingMessages < cursor.PendingMessages || session.PendingBytes < cursor.PendingBytes {
			return dberrors.ErrCorruptValue
		}
		var changed [3]MQTTInflight
		var changedCount int
		var erased *MQTTInflight
		var packetID uint16
		var deliveryOrder uint64
		switch m.Op {
		case MQTTWindowAdmit:
			sub, ok, err := loadUpdateRow(mqttSubscriptionTable, state, slot, mqttSubscriptionPrimaryKey(m.Key.Namespace, m.Key.ClientID, m.Key.SessionGeneration, cursor.Topic))
			if err != nil {
				return err
			}
			if !ok || sub.Generation != m.Key.SubscriptionGeneration || sub.AuthorizationVersion != cursor.AuthorizationVersion || sub.Stage != MQTTSubscriptionActive ||
				sub.GrantedQoS != 1 || sub.SubscriptionIdentifier != m.Publication.SubscriptionIdentifier {
				return nil
			}
			limit := session.WindowLimit
			if limit == 0 {
				limit = MQTTDefaultWindowLimit
			}
			if session.ReceiveMaximum < limit {
				limit = session.ReceiveMaximum
			}
			if session.OutboundInflight >= limit {
				result.Status = MQTTWindowFull
				return nil
			}
			if m.Publication.Position <= cursor.WindowThrough || m.Publication.Position > cursor.AccountedThrough ||
				uint64(cursor.InflightCount) >= cursor.PendingMessages || m.Publication.Bytes > cursor.PendingBytes-cursor.InflightBytes || session.NextDeliveryOrder == math.MaxUint64 {
				return nil
			}
			packetID, err = allocateMQTTPacketID(state, slot, session)
			if err != nil {
				return err
			}
			deliveryOrder = session.NextDeliveryOrder
			entry := MQTTInflight{Key: m.Key, Direction: MQTTOutbound, PacketID: packetID, DeliveryOrder: deliveryOrder, Publication: m.Publication,
				QoS: 1, Stage: MQTTInflightAwaitPUBACK, Topic: cursor.Topic, PrevPacketID: cursor.TailPacketID, UpdatedAtMS: m.UpdatedAtMS}
			if cursor.TailPacketID != 0 {
				tail, err := loadMQTTWindowNeighbor(state, slot, m.Key, cursor.TailPacketID)
				if err != nil {
					return err
				}
				if tail.NextPacketID != 0 || tail.Publication.Position >= m.Publication.Position || (tail.PrevPacketID == 0) != (cursor.HeadPacketID == cursor.TailPacketID) {
					return dberrors.ErrCorruptValue
				}
				tail.NextPacketID, tail.UpdatedAtMS = packetID, m.UpdatedAtMS
				changed[changedCount] = tail
				changedCount++
			} else {
				cursor.HeadPacketID = packetID
				cursor.CompletedThrough = m.Publication.Position - 1
			}
			cursor.TailPacketID = packetID
			cursor.InflightCount++
			cursor.InflightBytes += m.Publication.Bytes
			cursor.WindowThrough = m.Publication.Position
			session.OutboundInflight++
			session.NextDeliveryOrder++
			session.NextPacketID = nextMQTTPacketID(packetID)
			changed[changedCount] = entry
			changedCount++
		case MQTTWindowAck:
			entry, ok, err := loadUpdateRow(mqttInflightTable, state, slot, mqttInflightPrimaryKey(m.Key.Namespace, m.Key.ClientID, m.Key.SessionGeneration, MQTTOutbound, m.PacketID))
			if err != nil {
				return err
			}
			if !ok || entry.Key != m.Key || entry.DeliveryOrder != m.DeliveryOrder {
				return nil
			}
			if cursor.InflightCount == 0 || cursor.InflightBytes < entry.Publication.Bytes ||
				(entry.PrevPacketID == 0) != (cursor.HeadPacketID == entry.PacketID) || (entry.NextPacketID == 0) != (cursor.TailPacketID == entry.PacketID) {
				return dberrors.ErrCorruptValue
			}
			if entry.PrevPacketID != 0 {
				prev, err := loadMQTTWindowNeighbor(state, slot, m.Key, entry.PrevPacketID)
				if err != nil {
					return err
				}
				if prev.NextPacketID != entry.PacketID || prev.Publication.Position >= entry.Publication.Position {
					return dberrors.ErrCorruptValue
				}
				prev.NextPacketID, prev.UpdatedAtMS = entry.NextPacketID, m.UpdatedAtMS
				changed[changedCount] = prev
				changedCount++
			} else {
				cursor.HeadPacketID = entry.NextPacketID
			}
			if entry.NextPacketID != 0 {
				next, err := loadMQTTWindowNeighbor(state, slot, m.Key, entry.NextPacketID)
				if err != nil {
					return err
				}
				if next.PrevPacketID != entry.PacketID || next.Publication.Position <= entry.Publication.Position {
					return dberrors.ErrCorruptValue
				}
				next.PrevPacketID, next.UpdatedAtMS = entry.PrevPacketID, m.UpdatedAtMS
				changed[changedCount] = next
				changedCount++
				if entry.PrevPacketID == 0 {
					cursor.CompletedThrough = next.Publication.Position - 1
				}
			} else {
				cursor.TailPacketID = entry.PrevPacketID
			}
			cursor.InflightCount--
			cursor.InflightBytes -= entry.Publication.Bytes
			cursor.PendingMessages--
			cursor.PendingBytes -= entry.Publication.Bytes
			session.OutboundInflight--
			session.PendingMessages--
			session.PendingBytes -= entry.Publication.Bytes
			if cursor.InflightCount == 0 {
				cursor.CompletedThrough = cursor.WindowThrough
			}
			packetID, deliveryOrder = entry.PacketID, entry.DeliveryOrder
			erased = &entry
		case MQTTWindowAdvance:
			if m.Through <= cursor.WindowThrough || m.Through > cursor.AccountedThrough || m.ReleasedMessages > cursor.PendingMessages-uint64(cursor.InflightCount) ||
				m.ReleasedBytes > cursor.PendingBytes-cursor.InflightBytes {
				return nil
			}
			cursor.WindowThrough = m.Through
			cursor.PendingMessages -= m.ReleasedMessages
			cursor.PendingBytes -= m.ReleasedBytes
			session.PendingMessages -= m.ReleasedMessages
			session.PendingBytes -= m.ReleasedBytes
			if cursor.InflightCount == 0 {
				cursor.CompletedThrough = m.Through
			}
		}
		session.Revision++
		session.UpdatedAtMS = m.UpdatedAtMS
		cursor.Revision, cursor.LastMutationDigest, cursor.UpdatedAtMS = session.Revision, digest, m.UpdatedAtMS
		cursor.LastWindowPacketID, cursor.LastWindowDeliveryOrder = packetID, deliveryOrder
		// Validate before staging any row: a rejected conditional request must
		// not leave neighboring commands with a partially changed linked list.
		if ValidateMQTTDeliveryCursor(cursor) != nil || ValidateMQTTSession(session) != nil {
			if m.Op == MQTTWindowAck {
				return dberrors.ErrCorruptValue
			}
			return nil
		}
		for _, entry := range changed[:changedCount] {
			if err := stageUpdateRow(mqttInflightTable, state, batch, slot, entry); err != nil {
				return err
			}
		}
		if erased != nil {
			if err := deleteUpdateRow(mqttInflightTable, state, batch, slot, mqttInflightPrimaryKey(m.Key.Namespace, m.Key.ClientID, m.Key.SessionGeneration, MQTTOutbound, erased.PacketID)); err != nil {
				return err
			}
		}
		if err := stageUpdateRow(mqttDeliveryCursorTable, state, batch, slot, cursor); err != nil {
			return err
		}
		if err := stageUpdateRow(mqttSessionTable, state, batch, slot, session); err != nil {
			return err
		}
		*result = MQTTWindowResult{Status: MQTTWindowApplied, CurrentRevision: session.Revision, PacketID: packetID, DeliveryOrder: deliveryOrder}
		return nil
	})
	return result, nil
}

func loadMQTTWindowNeighbor(state *batchCommitState, slot HashSlot, key MQTTDeliveryCursorKey, packetID uint16) (MQTTInflight, error) {
	r, found, err := loadUpdateRow(mqttInflightTable, state, slot, mqttInflightPrimaryKey(key.Namespace, key.ClientID, key.SessionGeneration, MQTTOutbound, packetID))
	if err != nil {
		return r, err
	}
	if !found || r.Key != key {
		return MQTTInflight{}, dberrors.ErrCorruptValue
	}
	return r, nil
}

func nextMQTTPacketID(id uint16) uint16 {
	if id == math.MaxUint16 {
		return 1
	}
	return id + 1
}

func allocateMQTTPacketID(state *batchCommitState, slot HashSlot, session MQTTSession) (uint16, error) {
	candidate := session.NextPacketID
	// N live exchanges cannot occupy N+1 consecutive IDs. The durable bound
	// keeps this search at most 1025 point reads, including same-batch entries.
	for i := 0; i <= int(session.OutboundInflight); i++ {
		_, found, err := loadUpdateRow(mqttInflightTable, state, slot, mqttInflightPrimaryKey(session.Namespace, session.ClientID, session.Generation, MQTTOutbound, candidate))
		if err != nil {
			return 0, err
		}
		if !found {
			return candidate, nil
		}
		candidate = nextMQTTPacketID(candidate)
	}
	return 0, dberrors.ErrCorruptValue
}
