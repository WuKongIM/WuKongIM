package wire

import (
	"encoding/binary"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
)

const (
	// HeaderSize is the fixed byte size of every transport frame header.
	HeaderSize = 24

	// Magic identifies transport wire frames.
	Magic uint16 = 0x574b

	// Version is the supported transport wire version.
	Version uint8 = 1

	// ResponseOK marks a successful RPC response payload.
	ResponseOK uint8 = 0

	// ResponseErr marks an errored RPC response payload.
	ResponseErr uint8 = 1

	// ResponseServiceNotFound marks an RPC service absent on the remote node.
	ResponseServiceNotFound uint8 = 2
)

const (
	headerMagicOffset     = 0
	headerVersionOffset   = 2
	headerFlagsOffset     = 3
	headerKindOffset      = 4
	headerPriorityOffset  = 5
	headerServiceIDOffset = 6
	headerRequestIDOffset = 8
	headerBodyLenOffset   = 16
	headerReservedOffset  = 20
)

// Header is the fixed metadata prefix carried by every transport frame.
type Header struct {
	// Kind identifies how the receiver should interpret the frame.
	Kind core.FrameKind
	// Priority selects the receiver scheduling lane.
	Priority core.Priority
	// ServiceID identifies the logical service for RPC and control frames.
	ServiceID uint16
	// RequestID correlates RPC requests with responses.
	RequestID uint64
	// BodyLen is the payload byte count that follows the header.
	BodyLen uint32
}

// Frame is a decoded transport wire frame and its owned payload. Writers borrow Body.Bytes()
// until net.Buffers.WriteTo returns; callers must release or mutate Body only after the write completes.
type Frame struct {
	// Header contains validated frame metadata.
	Header Header
	// Body owns the frame payload bytes.
	Body core.OwnedBuffer
}

// EncodeHeader serializes header as a fixed 24-byte big-endian prefix.
func EncodeHeader(header Header) [HeaderSize]byte {
	var encoded [HeaderSize]byte
	binary.BigEndian.PutUint16(encoded[headerMagicOffset:], Magic)
	encoded[headerVersionOffset] = Version
	encoded[headerFlagsOffset] = 0
	encoded[headerKindOffset] = uint8(header.Kind)
	encoded[headerPriorityOffset] = uint8(header.Priority)
	binary.BigEndian.PutUint16(encoded[headerServiceIDOffset:], header.ServiceID)
	binary.BigEndian.PutUint64(encoded[headerRequestIDOffset:], header.RequestID)
	binary.BigEndian.PutUint32(encoded[headerBodyLenOffset:], header.BodyLen)
	binary.BigEndian.PutUint32(encoded[headerReservedOffset:], 0)
	return encoded
}

// DecodeHeader validates and decodes a fixed transport wire header.
func DecodeHeader(encoded []byte, maxBodyBytes int) (Header, error) {
	if len(encoded) < HeaderSize {
		return Header{}, fmt.Errorf("%w: short header %d/%d", core.ErrInvalidFrame, len(encoded), HeaderSize)
	}
	if magic := binary.BigEndian.Uint16(encoded[headerMagicOffset:]); magic != Magic {
		return Header{}, fmt.Errorf("%w: magic 0x%04x", core.ErrInvalidFrame, magic)
	}
	if version := encoded[headerVersionOffset]; version != Version {
		return Header{}, fmt.Errorf("%w: version %d", core.ErrInvalidFrame, version)
	}
	if flags := encoded[headerFlagsOffset]; flags != 0 {
		return Header{}, fmt.Errorf("%w: flags %d", core.ErrInvalidFrame, flags)
	}
	if reserved := binary.BigEndian.Uint32(encoded[headerReservedOffset:]); reserved != 0 {
		return Header{}, fmt.Errorf("%w: reserved %d", core.ErrInvalidFrame, reserved)
	}

	header := Header{
		Kind:      core.FrameKind(encoded[headerKindOffset]),
		Priority:  core.Priority(encoded[headerPriorityOffset]),
		ServiceID: binary.BigEndian.Uint16(encoded[headerServiceIDOffset:]),
		RequestID: binary.BigEndian.Uint64(encoded[headerRequestIDOffset:]),
		BodyLen:   binary.BigEndian.Uint32(encoded[headerBodyLenOffset:]),
	}
	if !header.Kind.Valid() {
		return Header{}, fmt.Errorf("%w: kind %d", core.ErrInvalidFrame, header.Kind)
	}
	if err := header.Priority.Validate(); err != nil {
		return Header{}, err
	}
	if bodyExceedsMax(header.BodyLen, maxBodyBytes) {
		return Header{}, fmt.Errorf("%w: body %d > max %d", core.ErrMsgTooLarge, header.BodyLen, maxBodyBytes)
	}
	return header, nil
}

func bodyExceedsMax(bodyLen uint32, maxBodyBytes int) bool {
	if maxBodyBytes < 0 {
		return true
	}
	return uint64(bodyLen) > uint64(maxBodyBytes)
}

func bodyLenToInt(bodyLen uint32) (int, error) {
	if uint64(bodyLen) > uint64(^uint(0)>>1) {
		return 0, fmt.Errorf("%w: body %d > int max", core.ErrMsgTooLarge, bodyLen)
	}
	return int(bodyLen), nil
}
