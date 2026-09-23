package meta

import (
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
)

const mqttSessionValueVersion byte = 1

// Column IDs are durable. Column 26 is a derived index column, not a value field.
var mqttSessionTable = registerMetaTable(TableSpec[MQTTSession]{
	ID: TableIDMQTTSession, Name: "mqtt_session",
	Columns: []schema.Column{
		{ID: 1, Name: "broker_namespace", Type: schema.TypeString, Required: true},
		{ID: 2, Name: "client_id", Type: schema.TypeString, Required: true},
		{ID: 3, Name: "uid", Type: schema.TypeString, Required: true},
		{ID: 4, Name: "generation", Type: schema.TypeUint64, Required: true},
		{ID: 5, Name: "revision", Type: schema.TypeUint64, Required: true},
		{ID: 6, Name: "owner_generation", Type: schema.TypeUint64, Required: true},
		{ID: 7, Name: "owner_node_id", Type: schema.TypeUint64, Required: true},
		{ID: 8, Name: "owner_boot_id", Type: schema.TypeString, Required: true},
		{ID: 9, Name: "connection_id", Type: schema.TypeUint64, Required: true},
		{ID: 10, Name: "lease_until_ms", Type: schema.TypeInt64, Required: true},
		{ID: 11, Name: "state", Type: schema.TypeUint8, Required: true},
		{ID: 12, Name: "session_expiry_sec", Type: schema.TypeUint64, Required: true},
		{ID: 13, Name: "offline_expires_at_ms", Type: schema.TypeInt64, Required: true},
		{ID: 14, Name: "device_flag", Type: schema.TypeUint8, Required: true},
		{ID: 15, Name: "receive_maximum", Type: schema.TypeUint64, Required: true},
		{ID: 16, Name: "max_packet_bytes", Type: schema.TypeUint64, Required: true},
		{ID: 17, Name: "next_packet_id", Type: schema.TypeUint64, Required: true},
		{ID: 18, Name: "next_delivery_order", Type: schema.TypeUint64, Required: true},
		{ID: 19, Name: "pending_messages", Type: schema.TypeUint64, Required: true},
		{ID: 20, Name: "pending_bytes", Type: schema.TypeUint64, Required: true},
		{ID: 21, Name: "quota_messages", Type: schema.TypeUint64, Required: true},
		{ID: 22, Name: "quota_bytes", Type: schema.TypeUint64, Required: true},
		{ID: 23, Name: "will_generation", Type: schema.TypeUint64, Required: true},
		{ID: 24, Name: "termination_reason", Type: schema.TypeUint8, Required: true},
		{ID: 25, Name: "updated_at_ms", Type: schema.TypeInt64, Required: true},
		{ID: 26, Name: "deadline_ms", Type: schema.TypeInt64},
		{ID: 27, Name: "outbound_inflight", Type: schema.TypeUint64},
		{ID: 28, Name: "window_limit", Type: schema.TypeUint64},
	},
	Families: []schema.Family{{ID: 0, Name: "primary", Columns: []uint16{3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 27, 28}}},
	Primary: PrimarySpec[MQTTSession]{IndexID: 1, Name: "pk_mqtt_session", Columns: []uint16{1, 2}, Layout: KeyLayout{KeyString, KeyString}, Key: func(r MQTTSession) KeyParts {
		return mqttSessionPrimaryKey(r.Namespace, r.ClientID)
	}},
	Indexes: []IndexSpec[MQTTSession]{{ID: 2, Name: "idx_mqtt_session_deadline", Columns: []uint16{26, 1, 2}, Layout: KeyLayout{KeyInt64Ordered, KeyString, KeyString}, CorruptIndexKeyIsError: true, Key: func(r MQTTSession) (KeyParts, bool) {
		deadline := mqttSessionDeadline(r)
		return KeyParts{Int64Ordered(deadline), String(r.Namespace), String(r.ClientID)}, deadline > 0
	}}},
	Validate:           ValidateMQTTSession,
	EncodeValueWithKey: encodeMQTTSessionRow,
	DecodeValueWithKey: decodeMQTTSessionRow,
})

// MQTTSessionTable describes the durable binding/session table for discovery.
var MQTTSessionTable = mqttSessionTable.Schema()

func encodeMQTTSessionRow(key []byte, r MQTTSession) ([]byte, error) {
	if err := ValidateMQTTSession(r); err != nil {
		return nil, err
	}
	var w rowcodec.Writer
	_ = w.String(3, r.UID)
	_ = w.Uint64(4, r.Generation)
	_ = w.Uint64(5, r.Revision)
	_ = w.Uint64(6, r.OwnerGeneration)
	_ = w.Uint64(7, r.OwnerNodeID)
	_ = w.String(8, r.OwnerBootID)
	_ = w.Uint64(9, r.ConnectionID)
	_ = w.Int64(10, r.LeaseUntilMS)
	_ = w.Uint8(11, uint8(r.State))
	_ = w.Uint64(12, uint64(r.SessionExpirySec))
	_ = w.Int64(13, r.OfflineExpiresAtMS)
	_ = w.Uint8(14, r.DeviceFlag)
	_ = w.Uint64(15, uint64(r.ReceiveMaximum))
	_ = w.Uint64(16, uint64(r.MaxPacketBytes))
	_ = w.Uint64(17, uint64(r.NextPacketID))
	_ = w.Uint64(18, r.NextDeliveryOrder)
	_ = w.Uint64(19, r.PendingMessages)
	_ = w.Uint64(20, r.PendingBytes)
	_ = w.Uint64(21, r.QuotaMessages)
	_ = w.Uint64(22, r.QuotaBytes)
	_ = w.Uint64(23, r.WillGeneration)
	_ = w.Uint8(24, uint8(r.TerminationReason))
	_ = w.Int64(25, r.UpdatedAtMS)
	_ = w.Uint64(27, uint64(r.OutboundInflight))
	_ = w.Uint64(28, uint64(r.WindowLimit))
	return rowcodec.Wrap(key, mqttSessionValueVersion, rowcodec.CodecColumns, rowcodec.FlagChecksum, w.Bytes()), nil
}

func decodeMQTTSessionRow(key []byte, pk KeyParts, value []byte) (MQTTSession, error) {
	var r MQTTSession
	if len(value) > 8<<10 || len(pk) != 2 {
		return r, dberrors.ErrCorruptValue
	}
	env, err := rowcodec.UnwrapBorrowed(key, value)
	if err != nil {
		return r, err
	}
	if env.Version != mqttSessionValueVersion || env.Codec != rowcodec.CodecColumns || env.Flags != rowcodec.FlagChecksum {
		return r, dberrors.ErrCorruptValue
	}
	r.Namespace, r.ClientID = pk[0].S, pk[1].S
	s := rowcodec.NewBorrowedScanner(env.Payload)
	var seen uint32
	var last uint16
	for s.Next() {
		id := s.ColumnID()
		if id <= last {
			return MQTTSession{}, dberrors.ErrCorruptValue
		}
		last = id
		if id >= 3 && id <= 25 {
			seen |= 1 << (id - 3)
		}
		var n uint64
		var small uint8
		switch id {
		case 3:
			r.UID, err = s.String()
		case 4:
			r.Generation, err = s.Uint64()
		case 5:
			r.Revision, err = s.Uint64()
		case 6:
			r.OwnerGeneration, err = s.Uint64()
		case 7:
			r.OwnerNodeID, err = s.Uint64()
		case 8:
			r.OwnerBootID, err = s.String()
		case 9:
			r.ConnectionID, err = s.Uint64()
		case 10:
			r.LeaseUntilMS, err = s.Int64()
		case 11:
			small, err = s.Uint8()
			r.State = MQTTSessionState(small)
		case 12:
			n, err = s.Uint64()
			if n > math.MaxUint32 {
				return MQTTSession{}, dberrors.ErrCorruptValue
			}
			r.SessionExpirySec = uint32(n)
		case 13:
			r.OfflineExpiresAtMS, err = s.Int64()
		case 14:
			r.DeviceFlag, err = s.Uint8()
		case 15:
			n, err = s.Uint64()
			if n > math.MaxUint16 {
				return MQTTSession{}, dberrors.ErrCorruptValue
			}
			r.ReceiveMaximum = uint16(n)
		case 16:
			n, err = s.Uint64()
			if n > math.MaxUint32 {
				return MQTTSession{}, dberrors.ErrCorruptValue
			}
			r.MaxPacketBytes = uint32(n)
		case 17:
			n, err = s.Uint64()
			if n > math.MaxUint16 {
				return MQTTSession{}, dberrors.ErrCorruptValue
			}
			r.NextPacketID = uint16(n)
		case 18:
			r.NextDeliveryOrder, err = s.Uint64()
		case 19:
			r.PendingMessages, err = s.Uint64()
		case 20:
			r.PendingBytes, err = s.Uint64()
		case 21:
			r.QuotaMessages, err = s.Uint64()
		case 22:
			r.QuotaBytes, err = s.Uint64()
		case 23:
			r.WillGeneration, err = s.Uint64()
		case 24:
			small, err = s.Uint8()
			r.TerminationReason = MQTTSessionEndReason(small)
		case 25:
			r.UpdatedAtMS, err = s.Int64()
		case 27, 28:
			n, err = s.Uint64()
			if n > math.MaxUint16 {
				return MQTTSession{}, dberrors.ErrCorruptValue
			}
			if id == 27 {
				r.OutboundInflight = uint16(n)
			} else {
				r.WindowLimit = uint16(n)
			}
		}
		if err != nil {
			return MQTTSession{}, err
		}
	}
	if s.Err() != nil || seen != (1<<23)-1 || ValidateMQTTSession(r) != nil {
		return MQTTSession{}, dberrors.ErrCorruptValue
	}
	return r, nil
}

func inspectMQTTSessionRow(r MQTTSession) InspectRow {
	return InspectRow{
		"broker_namespace": r.Namespace, "client_id": r.ClientID, "uid": r.UID,
		"generation": r.Generation, "revision": r.Revision, "owner_generation": r.OwnerGeneration,
		"owner_node_id": r.OwnerNodeID, "owner_boot_id": r.OwnerBootID, "connection_id": r.ConnectionID,
		"lease_until_ms": r.LeaseUntilMS, "state": uint8(r.State), "session_expiry_sec": uint64(r.SessionExpirySec),
		"offline_expires_at_ms": r.OfflineExpiresAtMS, "device_flag": r.DeviceFlag,
		"receive_maximum": uint64(r.ReceiveMaximum), "max_packet_bytes": uint64(r.MaxPacketBytes),
		"next_packet_id": uint64(r.NextPacketID), "next_delivery_order": r.NextDeliveryOrder,
		"pending_messages": r.PendingMessages, "pending_bytes": r.PendingBytes,
		"quota_messages": r.QuotaMessages, "quota_bytes": r.QuotaBytes,
		"will_generation": r.WillGeneration, "termination_reason": uint8(r.TerminationReason),
		"updated_at_ms": r.UpdatedAtMS, "deadline_ms": mqttSessionDeadline(r),
		"outbound_inflight": uint64(r.OutboundInflight), "window_limit": uint64(r.WindowLimit),
	}
}
