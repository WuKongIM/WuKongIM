package meta

import (
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
	"math"
)

// Durable IDs include the recovery-deadline column reserved outside the value.
var mqttWillTable = registerMetaTable(TableSpec[MQTTWill]{
	ID: TableIDMQTTWill, Name: "mqtt_will",
	Columns: []schema.Column{
		{ID: 1, Name: "broker_namespace", Type: schema.TypeString, Required: true},
		{ID: 2, Name: "client_id", Type: schema.TypeString, Required: true},
		{ID: 3, Name: "session_generation", Type: schema.TypeUint64, Required: true},
		{ID: 4, Name: "will_generation", Type: schema.TypeUint64, Required: true},
		{ID: 5, Name: "uid", Type: schema.TypeString, Required: true},
		{ID: 6, Name: "owner_generation", Type: schema.TypeUint64, Required: true},
		{ID: 7, Name: "owner_node_id", Type: schema.TypeUint64, Required: true},
		{ID: 8, Name: "owner_boot_id", Type: schema.TypeString, Required: true},
		{ID: 9, Name: "connection_id", Type: schema.TypeUint64, Required: true},
		{ID: 10, Name: "revision", Type: schema.TypeUint64, Required: true},
		{ID: 11, Name: "decision_revision", Type: schema.TypeUint64, Required: true},
		{ID: 12, Name: "topic", Type: schema.TypeString, Required: true},
		{ID: 13, Name: "target_id", Type: schema.TypeString, Required: true},
		{ID: 14, Name: "target_type", Type: schema.TypeUint8, Required: true},
		{ID: 15, Name: "payload", Type: schema.TypeBytes, Required: true},
		{ID: 16, Name: "publication_metadata", Type: schema.TypeBytes, Required: true},
		{ID: 17, Name: "delay_seconds", Type: schema.TypeUint64, Required: true},
		{ID: 18, Name: "qos", Type: schema.TypeUint8, Required: true},
		{ID: 19, Name: "client_msg_no", Type: schema.TypeString, Required: true},
		{ID: 20, Name: "idempotency_key", Type: schema.TypeString, Required: true},
		{ID: 21, Name: "stage", Type: schema.TypeUint8, Required: true},
		{ID: 22, Name: "disconnected_at_ms", Type: schema.TypeInt64, Required: true},
		{ID: 23, Name: "due_at_ms", Type: schema.TypeInt64, Required: true},
		{ID: 24, Name: "execution_generation", Type: schema.TypeUint64, Required: true},
		{ID: 25, Name: "executor_node_id", Type: schema.TypeUint64, Required: true},
		{ID: 26, Name: "executor_boot_id", Type: schema.TypeString, Required: true},
		{ID: 27, Name: "lease_until_ms", Type: schema.TypeInt64, Required: true},
		{ID: 28, Name: "cancel_reason", Type: schema.TypeUint8, Required: true},
		{ID: 29, Name: "reject_reason", Type: schema.TypeUint8, Required: true},
		{ID: 30, Name: "message_id", Type: schema.TypeUint64, Required: true},
		{ID: 31, Name: "message_seq", Type: schema.TypeUint64, Required: true},
		{ID: 32, Name: "published_at_ms", Type: schema.TypeInt64, Required: true},
		{ID: 33, Name: "updated_at_ms", Type: schema.TypeInt64, Required: true},
		{ID: 34, Name: "recovery_at_ms", Type: schema.TypeInt64},
		{ID: 35, Name: "dispatch_stage", Type: schema.TypeUint8},
		{ID: 36, Name: "dispatch_payload", Type: schema.TypeBytes},
	},
	Families: []schema.Family{{ID: 0, Name: "primary", Columns: []uint16{5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 35, 36}}},
	Primary:  PrimarySpec[MQTTWill]{IndexID: 1, Name: "pk_mqtt_will", Columns: []uint16{1, 2, 3, 4}, Layout: KeyLayout{KeyString, KeyString, KeyUint64, KeyUint64}, Key: func(r MQTTWill) KeyParts { return mqttWillPrimaryKey(r.Key) }},
	Indexes: []IndexSpec[MQTTWill]{{ID: 2, Name: "idx_mqtt_will_recovery", Columns: []uint16{34, 1, 2, 3, 4}, Layout: KeyLayout{KeyInt64Ordered, KeyString, KeyString, KeyUint64, KeyUint64}, CorruptIndexKeyIsError: true, Key: func(r MQTTWill) (KeyParts, bool) {
		at := mqttWillRecoveryAt(r)
		return append(KeyParts{Int64Ordered(at)}, mqttWillPrimaryKey(r.Key)...), at > 0
	}}},
	Validate:           ValidateMQTTWill,
	EncodeValueWithKey: encodeMQTTWillRow,
	DecodeValueWithKey: decodeMQTTWillRow,
})

// MQTTWillTable exposes durable Will obligations for schema and snapshot tooling.
var MQTTWillTable = mqttWillTable.Schema()

func encodeMQTTWillRow(key []byte, r MQTTWill) ([]byte, error) {
	if err := ValidateMQTTWill(r); err != nil {
		return nil, err
	}
	var w rowcodec.Writer
	_ = w.String(5, r.UID)
	_ = w.Uint64(6, r.OwnerGeneration)
	_ = w.Uint64(7, r.OwnerNodeID)
	_ = w.String(8, r.OwnerBootID)
	_ = w.Uint64(9, r.ConnectionID)
	_ = w.Uint64(10, r.Revision)
	_ = w.Uint64(11, r.DecisionRevision)
	_ = w.String(12, r.Topic)
	_ = w.String(13, r.TargetID)
	_ = w.Uint8(14, r.TargetType)
	_ = w.RawBytes(15, r.Payload)
	_ = w.RawBytes(16, r.PublicationMetadata)
	_ = w.Uint64(17, uint64(r.DelaySeconds))
	_ = w.Uint8(18, r.QoS)
	_ = w.String(19, r.ClientMsgNo)
	_ = w.String(20, r.IdempotencyKey)
	_ = w.Uint8(21, uint8(r.Stage))
	_ = w.Int64(22, r.DisconnectedAtMS)
	_ = w.Int64(23, r.DueAtMS)
	_ = w.Uint64(24, r.ExecutionGeneration)
	_ = w.Uint64(25, r.ExecutorNodeID)
	_ = w.String(26, r.ExecutorBootID)
	_ = w.Int64(27, r.LeaseUntilMS)
	_ = w.Uint8(28, uint8(r.CancelReason))
	_ = w.Uint8(29, uint8(r.RejectReason))
	_ = w.Uint64(30, r.MessageID)
	_ = w.Uint64(31, r.MessageSeq)
	_ = w.Int64(32, r.PublishedAtMS)
	_ = w.Int64(33, r.UpdatedAtMS)
	if r.DispatchStage != MQTTWillDispatchLegacy {
		_ = w.Uint8(35, uint8(r.DispatchStage))
		if r.DispatchStage >= MQTTWillDispatchPrepared {
			_ = w.RawBytes(36, r.DispatchPayload)
		}
	}
	return rowcodec.Wrap(key, 1, rowcodec.CodecColumns, rowcodec.FlagChecksum, w.Bytes()), nil
}

func decodeMQTTWillRow(key []byte, pk KeyParts, value []byte) (MQTTWill, error) {
	var r MQTTWill
	if len(value) > 192<<10 || len(pk) != 4 {
		return r, dberrors.ErrCorruptValue
	}
	env, err := rowcodec.UnwrapBorrowed(key, value)
	if err != nil {
		return r, err
	}
	if env.Version != 1 || env.Codec != rowcodec.CodecColumns || env.Flags != rowcodec.FlagChecksum {
		return r, dberrors.ErrCorruptValue
	}
	r.Key = mqttWillKeyFromParts(pk)
	s := rowcodec.NewBorrowedScanner(env.Payload)
	var seen uint32
	var last uint16
	var seenDispatch, seenPayload bool
	for s.Next() {
		id := s.ColumnID()
		if id <= last {
			return MQTTWill{}, dberrors.ErrCorruptValue
		}
		last = id
		if id >= 5 && id <= 33 {
			seen |= 1 << (id - 5)
		}
		switch id {
		case 5:
			r.UID, err = s.String()
		case 6:
			r.OwnerGeneration, err = s.Uint64()
		case 7:
			r.OwnerNodeID, err = s.Uint64()
		case 8:
			r.OwnerBootID, err = s.String()
		case 9:
			r.ConnectionID, err = s.Uint64()
		case 10:
			r.Revision, err = s.Uint64()
		case 11:
			r.DecisionRevision, err = s.Uint64()
		case 12:
			r.Topic, err = s.String()
		case 13:
			r.TargetID, err = s.String()
		case 14:
			r.TargetType, err = s.Uint8()
		case 15:
			r.Payload, err = s.Bytes()
		case 16:
			r.PublicationMetadata, err = s.Bytes()
		case 17:
			var n uint64
			n, err = s.Uint64()
			if n > math.MaxUint32 {
				return MQTTWill{}, dberrors.ErrCorruptValue
			}
			r.DelaySeconds = uint32(n)
		case 18:
			r.QoS, err = s.Uint8()
		case 19:
			r.ClientMsgNo, err = s.String()
		case 20:
			r.IdempotencyKey, err = s.String()
		case 21:
			var n uint8
			n, err = s.Uint8()
			r.Stage = MQTTWillStage(n)
		case 22:
			r.DisconnectedAtMS, err = s.Int64()
		case 23:
			r.DueAtMS, err = s.Int64()
		case 24:
			r.ExecutionGeneration, err = s.Uint64()
		case 25:
			r.ExecutorNodeID, err = s.Uint64()
		case 26:
			r.ExecutorBootID, err = s.String()
		case 27:
			r.LeaseUntilMS, err = s.Int64()
		case 28:
			var n uint8
			n, err = s.Uint8()
			r.CancelReason = MQTTWillCancelReason(n)
		case 29:
			var n uint8
			n, err = s.Uint8()
			r.RejectReason = MQTTWillRejectReason(n)
		case 30:
			r.MessageID, err = s.Uint64()
		case 31:
			r.MessageSeq, err = s.Uint64()
		case 32:
			r.PublishedAtMS, err = s.Int64()
		case 33:
			r.UpdatedAtMS, err = s.Int64()
		case 35:
			var n uint8
			n, err = s.Uint8()
			r.DispatchStage, seenDispatch = MQTTWillDispatchStage(n), true
		case 36:
			r.DispatchPayload, err = s.Bytes()
			seenPayload = true
		}
		if err != nil {
			return MQTTWill{}, err
		}
	}
	if s.Err() != nil || seen != (1<<29)-1 || ValidateMQTTWill(r) != nil || seenDispatch != (r.DispatchStage != MQTTWillDispatchLegacy) || seenPayload != (r.DispatchStage >= MQTTWillDispatchPrepared) {
		return MQTTWill{}, dberrors.ErrCorruptValue
	}
	return r, nil
}

func inspectMQTTWillRow(r MQTTWill) InspectRow {
	return InspectRow{
		"broker_namespace":           r.Key.Namespace,
		"client_id":                  r.Key.ClientID,
		"session_generation":         r.Key.SessionGeneration,
		"will_generation":            r.Key.WillGeneration,
		"uid":                        r.UID,
		"owner_generation":           r.OwnerGeneration,
		"owner_node_id":              r.OwnerNodeID,
		"owner_boot_id":              r.OwnerBootID,
		"connection_id":              r.ConnectionID,
		"revision":                   r.Revision,
		"decision_revision":          r.DecisionRevision,
		"topic":                      r.Topic,
		"target_id":                  r.TargetID,
		"target_type":                r.TargetType,
		"payload_bytes":              len(r.Payload),
		"publication_metadata_bytes": len(r.PublicationMetadata),
		"delay_seconds":              uint64(r.DelaySeconds),
		"qos":                        r.QoS,
		"client_msg_no":              r.ClientMsgNo,
		"idempotency_key":            r.IdempotencyKey,
		"stage":                      uint8(r.Stage),
		"disconnected_at_ms":         r.DisconnectedAtMS,
		"due_at_ms":                  r.DueAtMS,
		"execution_generation":       r.ExecutionGeneration,
		"executor_node_id":           r.ExecutorNodeID,
		"executor_boot_id":           r.ExecutorBootID,
		"lease_until_ms":             r.LeaseUntilMS,
		"cancel_reason":              uint8(r.CancelReason),
		"reject_reason":              uint8(r.RejectReason),
		"message_id":                 r.MessageID,
		"message_seq":                r.MessageSeq,
		"published_at_ms":            r.PublishedAtMS,
		"updated_at_ms":              r.UpdatedAtMS,
		"recovery_at_ms":             mqttWillRecoveryAt(r),
		"dispatch_stage":             uint8(r.DispatchStage),
		"dispatch_payload_bytes":     len(r.DispatchPayload),
	}
}
