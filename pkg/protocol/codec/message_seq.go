package codec

import (
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/pkg/errors"
)

func decodeMessageSeq(dec *Decoder, version uint8) (uint64, error) {
	if version <= frame.LegacyMessageSeqVersion {
		value, err := dec.Uint32()
		return uint64(value), err
	}
	return dec.Uint64()
}

func encodeMessageSeq(enc *Encoder, version uint8, value uint64) error {
	if version <= frame.LegacyMessageSeqVersion {
		if value > uint64(^uint32(0)) {
			return errors.New("message seq exceeds legacy uint32")
		}
		enc.WriteUint32(uint32(value))
		return nil
	}
	enc.WriteUint64(value)
	return nil
}

func messageSeqSize(version uint8) int {
	if version <= frame.LegacyMessageSeqVersion {
		return frame.MessageSeqLegacyByteSize
	}
	return frame.MessageSeqU64ByteSize
}
