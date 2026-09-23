package meta

import (
	"context"
	"encoding/hex"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

// MQTTExchangeDirection separates the two packet-identifier namespaces.
type MQTTExchangeDirection uint8

const MQTTOutbound MQTTExchangeDirection = 1

// MQTTInflightStage is the durable exchange phase, not a socket-write timestamp.
type MQTTInflightStage uint8

const MQTTInflightAwaitPUBACK MQTTInflightStage = 1

// MQTTInflightPublication freezes one immutable replay reference and its byte
// accounting. A later message edit or subscription change cannot replace it.
type MQTTInflightPublication struct {
	Position               uint64 `json:"position"`
	MessageID              uint64 `json:"message_id"`
	MessageSeq             uint64 `json:"message_seq"`
	ContentVersion         uint64 `json:"content_version"`
	ContentHash            string `json:"content_hash"`
	Bytes                  uint64 `json:"bytes"`
	SubscriptionIdentifier uint32 `json:"subscription_identifier"`
}

// MQTTInflight exists only inside the bounded QoS window. The source-scoped
// links maintain the earliest outstanding gap without retaining ACK tombstones.
type MQTTInflight struct {
	Key       MQTTDeliveryCursorKey `json:"key"`
	Direction MQTTExchangeDirection `json:"direction"`
	PacketID  uint16                `json:"packet_id"`
	// DeliveryOrder also fences a reused packet ID's new exchange.
	DeliveryOrder uint64                  `json:"delivery_order"`
	Publication   MQTTInflightPublication `json:"publication"`
	QoS           uint8                   `json:"qos"`
	Stage         MQTTInflightStage       `json:"stage"`
	Topic         string                  `json:"topic"`
	PrevPacketID  uint16                  `json:"prev_packet_id"`
	NextPacketID  uint16                  `json:"next_packet_id"`
	UpdatedAtMS   int64                   `json:"updated_at_ms"`
}

func validateMQTTInflightPublication(p MQTTInflightPublication) error {
	if p.Position == 0 || p.MessageID == 0 || p.MessageSeq == 0 || p.ContentVersion == 0 || len(p.ContentHash) != 64 || p.SubscriptionIdentifier > 268435455 {
		return dberrors.ErrInvalidArgument
	}
	if _, err := hex.DecodeString(p.ContentHash); err != nil {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

// ValidateMQTTInflight checks one stored exchange; neighbor consistency is
// checked atomically by window commands while they hold the Slot write fence.
func ValidateMQTTInflight(r MQTTInflight) error {
	if validateMQTTDeliveryCursorKey(r.Key) != nil || r.Direction != MQTTOutbound || r.PacketID == 0 || r.DeliveryOrder == 0 ||
		validateMQTTInflightPublication(r.Publication) != nil || r.QoS != 1 || r.Stage != MQTTInflightAwaitPUBACK ||
		validateMQTTIdentity(r.Topic, 2048) != nil || r.UpdatedAtMS <= 0 || r.PrevPacketID == r.PacketID || r.NextPacketID == r.PacketID ||
		(r.PrevPacketID != 0 && r.PrevPacketID == r.NextPacketID) {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

func mqttInflightPrimaryKey(namespace, clientID string, generation uint64, direction MQTTExchangeDirection, packetID uint16) KeyParts {
	return KeyParts{String(namespace), String(clientID), Uint64(generation), Uint8(uint8(direction)), Uint64(uint64(packetID))}
}

// GetMQTTInflight reads one immutable exchange from node storage. Callers must
// establish current Slot authority and owner identity before any network send.
func (s *Shard) GetMQTTInflight(ctx context.Context, namespace, clientID string, generation uint64, direction MQTTExchangeDirection, packetID uint16) (MQTTInflight, bool, error) {
	if validateMQTTIdentity(namespace, 1024) != nil || validateMQTTIdentity(clientID, 1024) != nil || generation == 0 || direction != MQTTOutbound || packetID == 0 {
		return MQTTInflight{}, false, dberrors.ErrInvalidArgument
	}
	return mqttInflightTable.Get(ctx, s, mqttInflightPrimaryKey(namespace, clientID, generation, direction, packetID))
}

// MQTTInflightCursor includes the complete tie-breaker in a scoped send-order scan.
type MQTTInflightCursor struct {
	DeliveryOrder uint64
	PacketID      uint16
}

// ListMQTTInflight returns original send order, including across PacketID wrap.
// A reconnect must first establish authority and obtain a coherent recovery view;
// these storage pages alone do not authorize sending or bypass Receive Maximum.
func (s *Shard) ListMQTTInflight(ctx context.Context, namespace, clientID string, generation uint64, direction MQTTExchangeDirection, after MQTTInflightCursor, limit int) ([]MQTTInflight, MQTTInflightCursor, bool, error) {
	if validateMQTTIdentity(namespace, 1024) != nil || validateMQTTIdentity(clientID, 1024) != nil || generation == 0 || direction != MQTTOutbound || limit < 1 || limit > 256 {
		return nil, after, false, dberrors.ErrInvalidArgument
	}
	prefix := KeyParts{String(namespace), String(clientID), Uint64(generation), Uint8(uint8(direction))}
	var cursor KeyParts
	if after != (MQTTInflightCursor{}) {
		if after.DeliveryOrder == 0 || after.PacketID == 0 {
			return nil, after, false, dberrors.ErrInvalidArgument
		}
		cursor = KeyParts{String(namespace), String(clientID), Uint64(generation), Uint8(uint8(direction)), Uint64(after.DeliveryOrder), Uint64(uint64(after.PacketID))}
	}
	rows, next, done, err := mqttInflightTable.ScanIndex(ctx, s, 2, prefix, cursor, limit)
	if err != nil {
		return nil, after, false, err
	}
	if len(next) > 0 {
		after = MQTTInflightCursor{DeliveryOrder: next[4].U64, PacketID: uint16(next[5].U64)}
	}
	return rows, after, done, nil
}
