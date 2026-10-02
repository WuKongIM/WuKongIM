package message

import (
	"encoding/binary"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
)

func mqttReplayMeterKey(key ChannelKey, generation string, position uint64) []byte {
	b := newMessageKey(key, 17+len(generation))
	b = append(b, byte(keycodec.SpaceIndex))
	b = keycodec.AppendUint32(b, TableIDMQTTReplay)
	b = keycodec.AppendUint16(b, 2)
	b = keycodec.AppendString(b, generation)
	return keycodec.AppendUint64(b, position)
}

// stageMQTTReplayRecord keeps primary content and its small range endpoint in
// the same commit on both copy and backup restore. It writes no global index.
func stageMQTTReplayRecord(batch *engine.Batch, key ChannelKey, generation string, r MQTTReplayRecord, encoded []byte) error {
	if err := batch.Set(mqttReplayRowKey(key, generation, r.Position), encoded); err != nil {
		return err
	}
	b := binary.BigEndian.AppendUint64(nil, r.TotalBytes)
	b = binary.BigEndian.AppendUint64(b, r.TotalStoredBytes)
	b = append(b, r.Digest[:]...)
	meterKey := mqttReplayMeterKey(key, generation, r.Position)
	return batch.Set(meterKey, rowcodec.Wrap(meterKey, 1, rowcodec.CodecFixed, rowcodec.FlagChecksum, b))
}

func loadMQTTReplayMeter(view messageBackupReadView, key ChannelKey, s MQTTReplayState, position uint64) (MQTTReplayState, error) {
	k := mqttReplayMeterKey(key, s.Generation, position)
	value, ok, err := view.Get(k)
	if err != nil {
		return s, err
	}
	if !ok {
		return s, dberrors.ErrCorruptState
	}
	if len(value) != rowcodec.EnvelopeLen(48) {
		return s, dberrors.ErrCorruptValue
	}
	env, err := rowcodec.UnwrapBorrowed(k, value)
	if err != nil {
		return s, err
	}
	if env.Version != 1 || env.Codec != rowcodec.CodecFixed || env.Flags != rowcodec.FlagChecksum || len(env.Payload) != 48 {
		return s, dberrors.ErrCorruptValue
	}
	p := s
	p.Through = position
	p.TotalBytes = binary.BigEndian.Uint64(env.Payload)
	p.TotalStoredBytes = binary.BigEndian.Uint64(env.Payload[8:])
	copy(p.Digest[:], env.Payload[16:])
	if !mqttReplayStateValid(p) || p.TotalBytes > s.TotalBytes || p.TotalStoredBytes > s.TotalStoredBytes || (position == s.Through && p != s) {
		return s, dberrors.ErrCorruptState
	}
	return p, nil
}

// validateMQTTReplayTail verifies the primary tail as well as the small index
// before an extension. Range metering alone deliberately never reads bodies.
func validateMQTTReplayTail(view messageBackupReadView, key ChannelKey, s MQTTReplayState) error {
	base, _, err := mqttReplayBaseline(view, key, s)
	if err != nil {
		return err
	}
	if s == base {
		return nil
	}
	if _, err := loadMQTTReplayMeter(view, key, s, s.Through); err != nil {
		return err
	}
	r, err := loadMQTTReplayRecord(view, key, s.Generation, s.Through)
	if err != nil {
		return err
	}
	if s.TotalBytes != r.TotalBytes || s.TotalStoredBytes != r.TotalStoredBytes || s.Digest != r.Digest {
		return dberrors.ErrCorruptState
	}
	return nil
}
