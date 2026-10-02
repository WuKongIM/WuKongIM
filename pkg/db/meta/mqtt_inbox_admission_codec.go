package meta

import (
	"encoding/binary"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
)

// System 1 is reserved permanently for one person admission record per channel.
// Channel type is fixed at 1; UID/client identity lives only in its bounded value.
func mqttInboxAdmissionKey(slot HashSlot, id string) []byte {
	var builder keycodec.Builder
	prefix := builder.Reset().Domain(keycodec.DomainMeta).Partition(keycodec.PartitionHashSlot, hashSlotPartitionID(slot)).System(TableIDMQTTSourceBinding, 1).Key()
	key, _ := encodeKeyParts(prefix, KeyParts{String(id)})
	return key
}

func encodeMQTTInboxAdmission(key []byte, r MQTTInboxAdmission) ([]byte, error) {
	if err := ValidateMQTTInboxAdmission(r); err != nil {
		return nil, err
	}
	p := make([]byte, 0, 45+len(r.After.Namespace)+len(r.After.ClientID))
	for _, n := range []uint64{r.DirectoryGeneration, r.Revision, uint64(r.UpdatedAtMS)} {
		p = binary.BigEndian.AppendUint64(p, n)
	}
	p = append(p, r.Participant)
	for _, v := range []string{r.After.Namespace, r.After.ClientID} {
		p = binary.BigEndian.AppendUint16(p, uint16(len(v)))
		p = append(p, v...)
	}
	p = binary.BigEndian.AppendUint64(p, r.After.SessionGeneration)
	p = binary.BigEndian.AppendUint64(p, r.After.SubscriptionGeneration)
	return rowcodec.Wrap(key, 1, rowcodec.CodecFixed, rowcodec.FlagChecksum, p), nil
}

func decodeMQTTInboxAdmission(key []byte, id string, value []byte) (MQTTInboxAdmission, error) {
	bad := dberrors.ErrCorruptValue
	if len(value) > 2200 {
		return MQTTInboxAdmission{}, bad
	}
	env, err := rowcodec.UnwrapBorrowed(key, value)
	if err != nil {
		return MQTTInboxAdmission{}, err
	}
	p := env.Payload
	if env.Version != 1 || env.Codec != rowcodec.CodecFixed || env.Flags != rowcodec.FlagChecksum || len(p) < 45 {
		return MQTTInboxAdmission{}, bad
	}
	r := MQTTInboxAdmission{ChannelID: id, DirectoryGeneration: binary.BigEndian.Uint64(p), Revision: binary.BigEndian.Uint64(p[8:]), UpdatedAtMS: int64(binary.BigEndian.Uint64(p[16:])), Participant: p[24]}
	p = p[25:]
	for _, target := range []*string{&r.After.Namespace, &r.After.ClientID} {
		if len(p) < 2 {
			return MQTTInboxAdmission{}, bad
		}
		n := int(binary.BigEndian.Uint16(p))
		p = p[2:]
		if n > 1024 || len(p) < n {
			return MQTTInboxAdmission{}, bad
		}
		*target = string(p[:n])
		p = p[n:]
	}
	if len(p) != 16 {
		return MQTTInboxAdmission{}, bad
	}
	r.After.SessionGeneration = binary.BigEndian.Uint64(p)
	r.After.SubscriptionGeneration = binary.BigEndian.Uint64(p[8:])
	if r.After != (MQTTSourceBindingKey{}) {
		uids, e := mqttInboxParticipants(id)
		if e != nil || r.Participant > 1 {
			return MQTTInboxAdmission{}, bad
		}
		r.After.Owner = MQTTBindingOwner{Kind: MQTTBindingUID, ID: uids[r.Participant]}
	}
	if ValidateMQTTInboxAdmission(r) != nil {
		return MQTTInboxAdmission{}, bad
	}
	return r, nil
}

func loadMQTTInboxAdmission(state *batchCommitState, key []byte, id string) (MQTTInboxAdmission, bool, error) {
	value, found, err := mqttSourceBindingTable.loadBatchValue(state, key)
	if err != nil || !found {
		return MQTTInboxAdmission{}, false, err
	}
	r, err := decodeMQTTInboxAdmission(key, id, value)
	return r, err == nil, err
}

func stageMQTTInboxAdmission(state *batchCommitState, b *engine.Batch, key []byte, r MQTTInboxAdmission) error {
	value, err := encodeMQTTInboxAdmission(key, r)
	if err != nil {
		return err
	}
	if err = b.Set(key, value); err != nil {
		return err
	}
	state.tableRows[string(key)] = tableRowOverlay{value: value, exists: true}
	return nil
}
