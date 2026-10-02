package meta

import (
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
	"math"
)

// Column IDs and the complete source tuple are durable format identifiers.
var mqttDeliveryCursorTable = registerMetaTable(TableSpec[MQTTDeliveryCursor]{
	ID: TableIDMQTTDeliveryCursor, Name: "mqtt_delivery_cursor",
	Columns: []schema.Column{
		{ID: 1, Name: "broker_namespace", Type: schema.TypeString, Required: true},
		{ID: 2, Name: "client_id", Type: schema.TypeString, Required: true},
		{ID: 3, Name: "session_generation", Type: schema.TypeUint64, Required: true},
		{ID: 4, Name: "subscription_generation", Type: schema.TypeUint64, Required: true},
		{ID: 5, Name: "source_kind", Type: schema.TypeUint8, Required: true},
		{ID: 6, Name: "source_id", Type: schema.TypeString, Required: true},
		{ID: 7, Name: "source_generation", Type: schema.TypeString, Required: true},
		{ID: 8, Name: "topic", Type: schema.TypeString, Required: true},
		{ID: 9, Name: "authorization_version", Type: schema.TypeUint64, Required: true},
		{ID: 10, Name: "start_after", Type: schema.TypeUint64, Required: true},
		{ID: 11, Name: "accounted_through", Type: schema.TypeUint64, Required: true},
		{ID: 12, Name: "window_through", Type: schema.TypeUint64, Required: true},
		{ID: 13, Name: "completed_through", Type: schema.TypeUint64, Required: true},
		{ID: 14, Name: "pending_messages", Type: schema.TypeUint64, Required: true},
		{ID: 15, Name: "pending_bytes", Type: schema.TypeUint64, Required: true},
		{ID: 16, Name: "revision", Type: schema.TypeUint64, Required: true},
		{ID: 17, Name: "last_mutation_digest", Type: schema.TypeString, Required: true},
		{ID: 18, Name: "updated_at_ms", Type: schema.TypeInt64, Required: true},
		{ID: 19, Name: "inflight_count", Type: schema.TypeUint64},
		{ID: 20, Name: "inflight_bytes", Type: schema.TypeUint64},
		{ID: 21, Name: "head_packet_id", Type: schema.TypeUint64},
		{ID: 22, Name: "tail_packet_id", Type: schema.TypeUint64},
		{ID: 23, Name: "last_window_packet_id", Type: schema.TypeUint64},
		{ID: 24, Name: "last_window_delivery_order", Type: schema.TypeUint64},
		{ID: 25, Name: "accounting_version", Type: schema.TypeUint8},
		{ID: 26, Name: "accounting_head", Type: schema.TypeUint64},
		{ID: 27, Name: "accounting_tail", Type: schema.TypeUint64},
	},
	Families: []schema.Family{{ID: 0, Name: "primary", Columns: []uint16{8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27}}},
	Primary: PrimarySpec[MQTTDeliveryCursor]{IndexID: 1, Name: "pk_mqtt_delivery_cursor", Columns: []uint16{1, 2, 3, 4, 5, 6, 7}, Layout: KeyLayout{KeyString, KeyString, KeyUint64, KeyUint64, KeyUint8, KeyString, KeyString}, Key: func(r MQTTDeliveryCursor) KeyParts {
		return mqttDeliveryCursorPrimaryKey(r.Key)
	}},
	Validate:           ValidateMQTTDeliveryCursor,
	EncodeValueWithKey: encodeMQTTDeliveryCursorRow,
	DecodeValueWithKey: decodeMQTTDeliveryCursorRow,
})

// MQTTDeliveryCursorTable describes source progress for schema discovery.
var MQTTDeliveryCursorTable = mqttDeliveryCursorTable.Schema()

func encodeMQTTDeliveryCursorRow(key []byte, r MQTTDeliveryCursor) ([]byte, error) {
	if err := ValidateMQTTDeliveryCursor(r); err != nil {
		return nil, err
	}
	var w rowcodec.Writer
	_ = w.String(8, r.Topic)
	_ = w.Uint64(9, r.AuthorizationVersion)
	_ = w.Uint64(10, r.StartAfter)
	_ = w.Uint64(11, r.AccountedThrough)
	_ = w.Uint64(12, r.WindowThrough)
	_ = w.Uint64(13, r.CompletedThrough)
	_ = w.Uint64(14, r.PendingMessages)
	_ = w.Uint64(15, r.PendingBytes)
	_ = w.Uint64(16, r.Revision)
	_ = w.String(17, r.LastMutationDigest)
	_ = w.Int64(18, r.UpdatedAtMS)
	_ = w.Uint64(19, uint64(r.InflightCount))
	_ = w.Uint64(20, uint64(r.InflightBytes))
	_ = w.Uint64(21, uint64(r.HeadPacketID))
	_ = w.Uint64(22, uint64(r.TailPacketID))
	_ = w.Uint64(23, uint64(r.LastWindowPacketID))
	_ = w.Uint64(24, uint64(r.LastWindowDeliveryOrder))
	if r.AccountingVersion != 0 {
		_ = w.Uint64(25, uint64(r.AccountingVersion))
		_ = w.Uint64(26, r.AccountingHead)
		_ = w.Uint64(27, r.AccountingTail)
	}

	return rowcodec.Wrap(key, 1, rowcodec.CodecColumns, rowcodec.FlagChecksum, w.Bytes()), nil
}

func decodeMQTTDeliveryCursorRow(key []byte, pk KeyParts, value []byte) (MQTTDeliveryCursor, error) {
	var r MQTTDeliveryCursor
	if len(value) > 8<<10 || len(pk) != 7 {
		return r, dberrors.ErrCorruptValue
	}
	env, err := rowcodec.UnwrapBorrowed(key, value)
	if err != nil {
		return r, err
	}
	if env.Version != 1 || env.Codec != rowcodec.CodecColumns || env.Flags != rowcodec.FlagChecksum {
		return r, dberrors.ErrCorruptValue
	}
	r.Key = mqttDeliveryCursorKeyFromParts(pk)
	s := rowcodec.NewBorrowedScanner(env.Payload)
	var seen uint32
	var accountingSeen uint8
	var last uint16
	for s.Next() {
		id := s.ColumnID()
		if id <= last {
			return MQTTDeliveryCursor{}, dberrors.ErrCorruptValue
		}
		last = id
		if id >= 8 && id <= 18 {
			seen |= 1 << (id - 8)
		}
		if id >= 25 && id <= 27 {
			accountingSeen |= 1 << (id - 25)
		}
		switch id {
		case 8:
			r.Topic, err = s.String()
		case 9:
			r.AuthorizationVersion, err = s.Uint64()
		case 10:
			r.StartAfter, err = s.Uint64()
		case 11:
			r.AccountedThrough, err = s.Uint64()
		case 12:
			r.WindowThrough, err = s.Uint64()
		case 13:
			r.CompletedThrough, err = s.Uint64()
		case 14:
			r.PendingMessages, err = s.Uint64()
		case 15:
			r.PendingBytes, err = s.Uint64()
		case 16:
			r.Revision, err = s.Uint64()
		case 17:
			r.LastMutationDigest, err = s.String()
		case 18:
			r.UpdatedAtMS, err = s.Int64()
		case 19:
			var n uint64
			n, err = s.Uint64()
			if n > math.MaxUint16 {
				return MQTTDeliveryCursor{}, dberrors.ErrCorruptValue
			}
			r.InflightCount = uint16(n)
		case 20:
			r.InflightBytes, err = s.Uint64()
		case 21:
			var n uint64
			n, err = s.Uint64()
			if n > math.MaxUint16 {
				return MQTTDeliveryCursor{}, dberrors.ErrCorruptValue
			}
			r.HeadPacketID = uint16(n)
		case 22:
			var n uint64
			n, err = s.Uint64()
			if n > math.MaxUint16 {
				return MQTTDeliveryCursor{}, dberrors.ErrCorruptValue
			}
			r.TailPacketID = uint16(n)
		case 23:
			var n uint64
			n, err = s.Uint64()
			if n > math.MaxUint16 {
				return MQTTDeliveryCursor{}, dberrors.ErrCorruptValue
			}
			r.LastWindowPacketID = uint16(n)
		case 24:
			r.LastWindowDeliveryOrder, err = s.Uint64()
		case 25:
			var n uint64
			n, err = s.Uint64()
			if n > 1 {
				return MQTTDeliveryCursor{}, dberrors.ErrCorruptValue
			}
			r.AccountingVersion = uint8(n)
		case 26:
			r.AccountingHead, err = s.Uint64()
		case 27:
			r.AccountingTail, err = s.Uint64()

		}
		if err != nil {
			return MQTTDeliveryCursor{}, err
		}
	}
	if s.Err() != nil || seen != (1<<11)-1 ||
		(accountingSeen != 0 && (accountingSeen != 7 || r.AccountingVersion != 1)) || ValidateMQTTDeliveryCursor(r) != nil {
		return MQTTDeliveryCursor{}, dberrors.ErrCorruptValue
	}
	return r, nil
}

func inspectMQTTDeliveryCursorRow(r MQTTDeliveryCursor) InspectRow {
	return InspectRow{
		"broker_namespace": r.Key.Namespace, "client_id": r.Key.ClientID, "session_generation": r.Key.SessionGeneration,
		"subscription_generation": r.Key.SubscriptionGeneration, "source_kind": uint8(r.Key.SourceKind), "source_id": r.Key.SourceID, "source_generation": r.Key.SourceGeneration,
		"topic": r.Topic, "authorization_version": r.AuthorizationVersion, "start_after": r.StartAfter,
		"accounted_through": r.AccountedThrough, "window_through": r.WindowThrough, "completed_through": r.CompletedThrough,
		"pending_messages": r.PendingMessages, "pending_bytes": r.PendingBytes, "revision": r.Revision,
		"last_mutation_digest": r.LastMutationDigest, "updated_at_ms": r.UpdatedAtMS,
		"inflight_count":             uint64(r.InflightCount),
		"inflight_bytes":             uint64(r.InflightBytes),
		"head_packet_id":             uint64(r.HeadPacketID),
		"tail_packet_id":             uint64(r.TailPacketID),
		"last_window_packet_id":      uint64(r.LastWindowPacketID),
		"last_window_delivery_order": uint64(r.LastWindowDeliveryOrder),
		"accounting_version":         uint64(r.AccountingVersion), "accounting_head": r.AccountingHead, "accounting_tail": r.AccountingTail,
	}
}
