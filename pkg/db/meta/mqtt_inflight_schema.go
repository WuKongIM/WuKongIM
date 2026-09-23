package meta

import (
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
	"math"
)

// Stable column IDs preserve immutable publication references and list links.
var mqttInflightTable = registerMetaTable(TableSpec[MQTTInflight]{
	ID: TableIDMQTTInflight, Name: "mqtt_inflight",
	Columns: []schema.Column{
		{ID: 1, Name: "broker_namespace", Type: schema.TypeString, Required: true},
		{ID: 2, Name: "client_id", Type: schema.TypeString, Required: true},
		{ID: 3, Name: "session_generation", Type: schema.TypeUint64, Required: true},
		{ID: 4, Name: "direction", Type: schema.TypeUint8, Required: true},
		{ID: 5, Name: "packet_id", Type: schema.TypeUint64, Required: true},
		{ID: 6, Name: "subscription_generation", Type: schema.TypeUint64, Required: true},
		{ID: 7, Name: "source_kind", Type: schema.TypeUint8, Required: true},
		{ID: 8, Name: "source_id", Type: schema.TypeString, Required: true},
		{ID: 9, Name: "source_generation", Type: schema.TypeString, Required: true},
		{ID: 10, Name: "delivery_order", Type: schema.TypeUint64, Required: true},
		{ID: 11, Name: "source_position", Type: schema.TypeUint64, Required: true},
		{ID: 12, Name: "message_id", Type: schema.TypeUint64, Required: true},
		{ID: 13, Name: "message_seq", Type: schema.TypeUint64, Required: true},
		{ID: 14, Name: "content_version", Type: schema.TypeUint64, Required: true},
		{ID: 15, Name: "content_hash", Type: schema.TypeString, Required: true},
		{ID: 16, Name: "accounted_bytes", Type: schema.TypeUint64, Required: true},
		{ID: 17, Name: "qos", Type: schema.TypeUint8, Required: true},
		{ID: 18, Name: "stage", Type: schema.TypeUint8, Required: true},
		{ID: 19, Name: "topic", Type: schema.TypeString, Required: true},
		{ID: 20, Name: "subscription_identifier", Type: schema.TypeUint64, Required: true},
		{ID: 21, Name: "prev_packet_id", Type: schema.TypeUint64, Required: true},
		{ID: 22, Name: "next_packet_id", Type: schema.TypeUint64, Required: true},
		{ID: 23, Name: "updated_at_ms", Type: schema.TypeInt64, Required: true},
	},
	Families: []schema.Family{{ID: 0, Name: "primary", Columns: []uint16{6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23}}},
	Primary: PrimarySpec[MQTTInflight]{IndexID: 1, Name: "pk_mqtt_inflight", Columns: []uint16{1, 2, 3, 4, 5}, Layout: KeyLayout{KeyString, KeyString, KeyUint64, KeyUint8, KeyUint64}, Key: func(r MQTTInflight) KeyParts {
		return mqttInflightPrimaryKey(r.Key.Namespace, r.Key.ClientID, r.Key.SessionGeneration, r.Direction, r.PacketID)
	}},
	Indexes: []IndexSpec[MQTTInflight]{{ID: 2, Name: "idx_mqtt_inflight_send_order", Columns: []uint16{1, 2, 3, 4, 10, 5}, Layout: KeyLayout{KeyString, KeyString, KeyUint64, KeyUint8, KeyUint64, KeyUint64}, CorruptIndexKeyIsError: true, Key: func(r MQTTInflight) (KeyParts, bool) {
		return KeyParts{String(r.Key.Namespace), String(r.Key.ClientID), Uint64(r.Key.SessionGeneration), Uint8(uint8(r.Direction)), Uint64(r.DeliveryOrder), Uint64(uint64(r.PacketID))}, true
	}}},
	Validate:           ValidateMQTTInflight,
	EncodeValueWithKey: encodeMQTTInflightRow,
	DecodeValueWithKey: decodeMQTTInflightRow,
})

// MQTTInflightTable exposes bounded exchange storage for schema discovery.
var MQTTInflightTable = mqttInflightTable.Schema()

func encodeMQTTInflightRow(key []byte, r MQTTInflight) ([]byte, error) {
	if err := ValidateMQTTInflight(r); err != nil {
		return nil, err
	}
	var w rowcodec.Writer
	_ = w.Uint64(6, r.Key.SubscriptionGeneration)
	_ = w.Uint8(7, uint8(r.Key.SourceKind))
	_ = w.String(8, r.Key.SourceID)
	_ = w.String(9, r.Key.SourceGeneration)
	_ = w.Uint64(10, r.DeliveryOrder)
	_ = w.Uint64(11, r.Publication.Position)
	_ = w.Uint64(12, r.Publication.MessageID)
	_ = w.Uint64(13, r.Publication.MessageSeq)
	_ = w.Uint64(14, r.Publication.ContentVersion)
	_ = w.String(15, r.Publication.ContentHash)
	_ = w.Uint64(16, r.Publication.Bytes)
	_ = w.Uint8(17, r.QoS)
	_ = w.Uint8(18, uint8(r.Stage))
	_ = w.String(19, r.Topic)
	_ = w.Uint64(20, uint64(r.Publication.SubscriptionIdentifier))
	_ = w.Uint64(21, uint64(r.PrevPacketID))
	_ = w.Uint64(22, uint64(r.NextPacketID))
	_ = w.Int64(23, r.UpdatedAtMS)
	return rowcodec.Wrap(key, 1, rowcodec.CodecColumns, rowcodec.FlagChecksum, w.Bytes()), nil
}

func decodeMQTTInflightRow(key []byte, pk KeyParts, value []byte) (MQTTInflight, error) {
	var r MQTTInflight
	if len(value) > 8<<10 || len(pk) != 5 || pk[4].U64 > math.MaxUint16 {
		return r, dberrors.ErrCorruptValue
	}
	env, err := rowcodec.UnwrapBorrowed(key, value)
	if err != nil {
		return r, err
	}
	if env.Version != 1 || env.Codec != rowcodec.CodecColumns || env.Flags != rowcodec.FlagChecksum {
		return r, dberrors.ErrCorruptValue
	}
	r.Key.Namespace, r.Key.ClientID, r.Key.SessionGeneration = pk[0].S, pk[1].S, pk[2].U64
	r.Direction, r.PacketID = MQTTExchangeDirection(pk[3].U8), uint16(pk[4].U64)
	s := rowcodec.NewBorrowedScanner(env.Payload)
	var seen uint32
	var last uint16
	for s.Next() {
		id := s.ColumnID()
		if id <= last {
			return MQTTInflight{}, dberrors.ErrCorruptValue
		}
		last = id
		if id >= 6 && id <= 23 {
			seen |= 1 << (id - 6)
		}
		switch id {
		case 6:
			r.Key.SubscriptionGeneration, err = s.Uint64()
		case 7:
			var n uint8
			n, err = s.Uint8()
			r.Key.SourceKind = MQTTSourceKind(n)
		case 8:
			r.Key.SourceID, err = s.String()
		case 9:
			r.Key.SourceGeneration, err = s.String()
		case 10:
			r.DeliveryOrder, err = s.Uint64()
		case 11:
			r.Publication.Position, err = s.Uint64()
		case 12:
			r.Publication.MessageID, err = s.Uint64()
		case 13:
			r.Publication.MessageSeq, err = s.Uint64()
		case 14:
			r.Publication.ContentVersion, err = s.Uint64()
		case 15:
			r.Publication.ContentHash, err = s.String()
		case 16:
			r.Publication.Bytes, err = s.Uint64()
		case 17:
			r.QoS, err = s.Uint8()
		case 18:
			var n uint8
			n, err = s.Uint8()
			r.Stage = MQTTInflightStage(n)
		case 19:
			r.Topic, err = s.String()
		case 20:
			var n uint64
			n, err = s.Uint64()
			if n > 268435455 {
				return MQTTInflight{}, dberrors.ErrCorruptValue
			}
			r.Publication.SubscriptionIdentifier = uint32(n)
		case 21:
			var n uint64
			n, err = s.Uint64()
			if n > math.MaxUint16 {
				return MQTTInflight{}, dberrors.ErrCorruptValue
			}
			r.PrevPacketID = uint16(n)
		case 22:
			var n uint64
			n, err = s.Uint64()
			if n > math.MaxUint16 {
				return MQTTInflight{}, dberrors.ErrCorruptValue
			}
			r.NextPacketID = uint16(n)
		case 23:
			r.UpdatedAtMS, err = s.Int64()
		}
		if err != nil {
			return MQTTInflight{}, err
		}
	}
	if s.Err() != nil || seen != (1<<18)-1 || ValidateMQTTInflight(r) != nil {
		return MQTTInflight{}, dberrors.ErrCorruptValue
	}
	return r, nil
}

func inspectMQTTInflightRow(r MQTTInflight) InspectRow {
	return InspectRow{
		"broker_namespace": r.Key.Namespace, "client_id": r.Key.ClientID, "session_generation": r.Key.SessionGeneration,
		"direction": uint8(r.Direction), "packet_id": uint64(r.PacketID),
		"subscription_generation": r.Key.SubscriptionGeneration,
		"source_kind":             uint8(r.Key.SourceKind),
		"source_id":               r.Key.SourceID,
		"source_generation":       r.Key.SourceGeneration,
		"delivery_order":          r.DeliveryOrder,
		"source_position":         r.Publication.Position,
		"message_id":              r.Publication.MessageID,
		"message_seq":             r.Publication.MessageSeq,
		"content_version":         r.Publication.ContentVersion,
		"content_hash":            r.Publication.ContentHash,
		"accounted_bytes":         r.Publication.Bytes,
		"qos":                     r.QoS,
		"stage":                   uint8(r.Stage),
		"topic":                   r.Topic,
		"subscription_identifier": uint64(r.Publication.SubscriptionIdentifier),
		"prev_packet_id":          uint64(r.PrevPacketID),
		"next_packet_id":          uint64(r.NextPacketID),
		"updated_at_ms":           r.UpdatedAtMS,
	}
}
