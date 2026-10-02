package meta

import (
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
)

// Column, family and index IDs are stable durable identifiers.
var mqttSubscriptionTable = registerMetaTable(TableSpec[MQTTSubscription]{
	ID: TableIDMQTTSubscription, Name: "mqtt_subscription",
	Columns: []schema.Column{
		{ID: 1, Name: "broker_namespace", Type: schema.TypeString, Required: true},
		{ID: 2, Name: "client_id", Type: schema.TypeString, Required: true},
		{ID: 3, Name: "session_generation", Type: schema.TypeUint64, Required: true},
		{ID: 4, Name: "topic", Type: schema.TypeString, Required: true},
		{ID: 5, Name: "generation", Type: schema.TypeUint64, Required: true},
		{ID: 6, Name: "target_kind", Type: schema.TypeUint8, Required: true},
		{ID: 7, Name: "target_id", Type: schema.TypeString, Required: true},
		{ID: 8, Name: "granted_qos", Type: schema.TypeUint8, Required: true},
		{ID: 9, Name: "no_local", Type: schema.TypeUint8, Required: true},
		{ID: 10, Name: "retain_as_published", Type: schema.TypeUint8, Required: true},
		{ID: 11, Name: "retain_handling", Type: schema.TypeUint8, Required: true},
		{ID: 12, Name: "subscription_identifier", Type: schema.TypeUint64, Required: true},
		{ID: 13, Name: "authorization_version", Type: schema.TypeUint64, Required: true},
		{ID: 14, Name: "stage", Type: schema.TypeUint8, Required: true},
		{ID: 15, Name: "operation_id", Type: schema.TypeString, Required: true},
		{ID: 16, Name: "recovery_at_ms", Type: schema.TypeInt64, Required: true},
		{ID: 17, Name: "updated_at_ms", Type: schema.TypeInt64, Required: true},
		{ID: 18, Name: "revision", Type: schema.TypeUint64, Required: true},
	},
	Families: []schema.Family{{ID: 0, Name: "primary", Columns: []uint16{5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18}}},
	Primary: PrimarySpec[MQTTSubscription]{IndexID: 1, Name: "pk_mqtt_subscription", Columns: []uint16{1, 2, 3, 4}, Layout: KeyLayout{KeyString, KeyString, KeyUint64, KeyString}, Key: func(r MQTTSubscription) KeyParts {
		return mqttSubscriptionPrimaryKey(r.Namespace, r.ClientID, r.SessionGeneration, r.Topic)
	}},
	Indexes: []IndexSpec[MQTTSubscription]{{ID: 2, Name: "idx_mqtt_subscription_recovery", Columns: []uint16{16, 1, 2, 3, 4}, Layout: KeyLayout{KeyInt64Ordered, KeyString, KeyString, KeyUint64, KeyString}, CorruptIndexKeyIsError: true, Key: func(r MQTTSubscription) (KeyParts, bool) {
		return KeyParts{Int64Ordered(r.RecoveryAtMS), String(r.Namespace), String(r.ClientID), Uint64(r.SessionGeneration), String(r.Topic)}, r.RecoveryAtMS > 0
	}}},
	Validate:           ValidateMQTTSubscription,
	EncodeValueWithKey: encodeMQTTSubscriptionRow,
	DecodeValueWithKey: decodeMQTTSubscriptionRow,
})

// MQTTSubscriptionTable describes durable subscription intent for discovery.
var MQTTSubscriptionTable = mqttSubscriptionTable.Schema()

func encodeMQTTSubscriptionRow(key []byte, r MQTTSubscription) ([]byte, error) {
	if err := ValidateMQTTSubscription(r); err != nil {
		return nil, err
	}
	var w rowcodec.Writer
	_ = w.Uint64(5, r.Generation)
	_ = w.Uint8(6, uint8(r.TargetKind))
	_ = w.String(7, r.TargetID)
	_ = w.Uint8(8, r.GrantedQoS)
	var noLocal uint8
	if r.NoLocal {
		noLocal = 1
	}
	_ = w.Uint8(9, noLocal)
	var retainAsPublished uint8
	if r.RetainAsPublished {
		retainAsPublished = 1
	}
	_ = w.Uint8(10, retainAsPublished)
	_ = w.Uint8(11, r.RetainHandling)
	_ = w.Uint64(12, uint64(r.SubscriptionIdentifier))
	_ = w.Uint64(13, r.AuthorizationVersion)
	_ = w.Uint8(14, uint8(r.Stage))
	_ = w.String(15, r.OperationID)
	_ = w.Int64(16, r.RecoveryAtMS)
	_ = w.Int64(17, r.UpdatedAtMS)
	_ = w.Uint64(18, r.Revision)
	return rowcodec.Wrap(key, 1, rowcodec.CodecColumns, rowcodec.FlagChecksum, w.Bytes()), nil
}

func decodeMQTTSubscriptionRow(key []byte, pk KeyParts, value []byte) (MQTTSubscription, error) {
	var r MQTTSubscription
	if len(value) > 8<<10 || len(pk) != 4 {
		return r, dberrors.ErrCorruptValue
	}
	env, err := rowcodec.UnwrapBorrowed(key, value)
	if err != nil {
		return r, err
	}
	if env.Version != 1 || env.Codec != rowcodec.CodecColumns || env.Flags != rowcodec.FlagChecksum {
		return r, dberrors.ErrCorruptValue
	}
	r.Namespace, r.ClientID, r.SessionGeneration, r.Topic = pk[0].S, pk[1].S, pk[2].U64, pk[3].S
	s := rowcodec.NewBorrowedScanner(env.Payload)
	var seen uint32
	var last uint16
	for s.Next() {
		id := s.ColumnID()
		if id <= last {
			return MQTTSubscription{}, dberrors.ErrCorruptValue
		}
		last = id
		if id >= 5 && id <= 18 {
			seen |= 1 << (id - 5)
		}
		switch id {
		case 5:
			r.Generation, err = s.Uint64()
		case 6:
			var n uint8
			n, err = s.Uint8()
			r.TargetKind = MQTTSubscriptionTargetKind(n)
		case 7:
			r.TargetID, err = s.String()
		case 8:
			r.GrantedQoS, err = s.Uint8()
		case 9:
			var n uint8
			n, err = s.Uint8()
			if n > 1 {
				return MQTTSubscription{}, dberrors.ErrCorruptValue
			}
			r.NoLocal = n == 1
		case 10:
			var n uint8
			n, err = s.Uint8()
			if n > 1 {
				return MQTTSubscription{}, dberrors.ErrCorruptValue
			}
			r.RetainAsPublished = n == 1
		case 11:
			r.RetainHandling, err = s.Uint8()
		case 12:
			var n uint64
			n, err = s.Uint64()
			if n > 268435455 {
				return MQTTSubscription{}, dberrors.ErrCorruptValue
			}
			r.SubscriptionIdentifier = uint32(n)
		case 13:
			r.AuthorizationVersion, err = s.Uint64()
		case 14:
			var n uint8
			n, err = s.Uint8()
			r.Stage = MQTTSubscriptionStage(n)
		case 15:
			r.OperationID, err = s.String()
		case 16:
			r.RecoveryAtMS, err = s.Int64()
		case 17:
			r.UpdatedAtMS, err = s.Int64()
		case 18:
			r.Revision, err = s.Uint64()
		}
		if err != nil {
			return MQTTSubscription{}, err
		}
	}
	if s.Err() != nil || seen != (1<<14)-1 || ValidateMQTTSubscription(r) != nil {
		return MQTTSubscription{}, dberrors.ErrCorruptValue
	}
	return r, nil
}

func inspectMQTTSubscriptionRow(r MQTTSubscription) InspectRow {
	return InspectRow{
		"broker_namespace": r.Namespace, "client_id": r.ClientID, "session_generation": r.SessionGeneration,
		"topic": r.Topic, "generation": r.Generation, "revision": r.Revision, "target_kind": uint8(r.TargetKind), "target_id": r.TargetID,
		"granted_qos": r.GrantedQoS, "no_local": r.NoLocal, "retain_as_published": r.RetainAsPublished,
		"retain_handling": r.RetainHandling, "subscription_identifier": uint64(r.SubscriptionIdentifier),
		"authorization_version": r.AuthorizationVersion, "stage": uint8(r.Stage), "operation_id": r.OperationID,
		"recovery_at_ms": r.RecoveryAtMS, "updated_at_ms": r.UpdatedAtMS,
	}
}
