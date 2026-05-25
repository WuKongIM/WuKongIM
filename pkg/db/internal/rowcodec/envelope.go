package rowcodec

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

const envelopeHeaderLen = 7

const (
	// CodecColumns stores column-family delta encoded payloads.
	CodecColumns byte = 0x01
	// CodecRaw stores raw byte payloads.
	CodecRaw byte = 0x02
	// CodecFixed stores fixed binary payloads.
	CodecFixed byte = 0x03
)

const (
	// FlagChecksum enables CRC32C verification over key, header, and payload.
	FlagChecksum byte = 1 << iota
)

var crc32cTable = crc32.MakeTable(crc32.Castagnoli)

// Envelope is the decoded value envelope header and payload.
type Envelope struct {
	// Version is the table-specific value codec version.
	Version byte
	// Codec identifies how Payload should be decoded.
	Codec byte
	// Flags stores envelope feature bits.
	Flags byte
	// Payload is the envelope body without the header.
	Payload []byte
}

// Wrap builds a value envelope for key and payload.
func Wrap(key []byte, version byte, codec byte, flags byte, payload []byte) []byte {
	value := make([]byte, envelopeHeaderLen+len(payload))
	value[0] = version
	value[1] = codec
	value[2] = flags
	copy(value[envelopeHeaderLen:], payload)
	if flags&FlagChecksum != 0 {
		sum := envelopeChecksum(key, value)
		binary.BigEndian.PutUint32(value[3:7], sum)
	}
	return value
}

// Unwrap verifies and decodes a value envelope.
func Unwrap(key []byte, value []byte) (Envelope, error) {
	if len(value) < envelopeHeaderLen {
		return Envelope{}, fmt.Errorf("%w: envelope too short", dberrors.ErrCorruptValue)
	}
	flags := value[2]
	if flags&FlagChecksum != 0 {
		want := binary.BigEndian.Uint32(value[3:7])
		got := envelopeChecksum(key, value)
		if got != want {
			return Envelope{}, dberrors.ErrChecksumMismatch
		}
	}
	return Envelope{
		Version: value[0],
		Codec:   value[1],
		Flags:   flags,
		Payload: append([]byte(nil), value[envelopeHeaderLen:]...),
	}, nil
}

func envelopeChecksum(key []byte, value []byte) uint32 {
	sum := crc32.Update(0, crc32cTable, key)
	sum = crc32.Update(sum, crc32cTable, value[:3])
	var zero [4]byte
	sum = crc32.Update(sum, crc32cTable, zero[:])
	sum = crc32.Update(sum, crc32cTable, value[envelopeHeaderLen:])
	if sum == 0 {
		return 1
	}
	return sum
}
