package rowcodec

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

// Type identifies the encoded type of a column value.
type Type byte

const (
	// TypeString stores a UTF-8 string as length-prefixed bytes.
	TypeString Type = 0x01
	// TypeBytes stores arbitrary length-prefixed bytes.
	TypeBytes Type = 0x02
	// TypeInt64 stores a zig-zag varint int64.
	TypeInt64 Type = 0x03
	// TypeUint64 stores a uvarint uint64.
	TypeUint64 Type = 0x04
	// TypeBool stores one byte, 0 or 1.
	TypeBool Type = 0x05
	// TypeUint8 stores one byte.
	TypeUint8 Type = 0x06
)

// Writer encodes ascending column IDs into one family payload.
type Writer struct {
	buf  []byte
	last uint16
}

// Reset clears the writer and keeps its scratch buffer.
func (w *Writer) Reset() {
	w.buf = w.buf[:0]
	w.last = 0
}

// Bytes returns the encoded family payload.
func (w *Writer) Bytes() []byte {
	return append([]byte(nil), w.buf...)
}

// String appends a string column.
func (w *Writer) String(columnID uint16, value string) error {
	if err := w.begin(columnID, TypeString); err != nil {
		return err
	}
	w.buf = appendUvarint(w.buf, uint64(len(value)))
	w.buf = append(w.buf, value...)
	return nil
}

// RawBytes appends a bytes column.
func (w *Writer) RawBytes(columnID uint16, value []byte) error {
	if err := w.begin(columnID, TypeBytes); err != nil {
		return err
	}
	w.buf = appendUvarint(w.buf, uint64(len(value)))
	w.buf = append(w.buf, value...)
	return nil
}

// Int64 appends an int64 column.
func (w *Writer) Int64(columnID uint16, value int64) error {
	if err := w.begin(columnID, TypeInt64); err != nil {
		return err
	}
	w.buf = appendUvarint(w.buf, encodeZigZagInt64(value))
	return nil
}

// Uint64 appends a uint64 column.
func (w *Writer) Uint64(columnID uint16, value uint64) error {
	if err := w.begin(columnID, TypeUint64); err != nil {
		return err
	}
	w.buf = appendUvarint(w.buf, value)
	return nil
}

// Bool appends a bool column.
func (w *Writer) Bool(columnID uint16, value bool) error {
	if err := w.begin(columnID, TypeBool); err != nil {
		return err
	}
	if value {
		w.buf = append(w.buf, 1)
	} else {
		w.buf = append(w.buf, 0)
	}
	return nil
}

// Uint8 appends a uint8 column.
func (w *Writer) Uint8(columnID uint16, value uint8) error {
	if err := w.begin(columnID, TypeUint8); err != nil {
		return err
	}
	w.buf = append(w.buf, value)
	return nil
}

func (w *Writer) begin(columnID uint16, typ Type) error {
	if columnID <= w.last {
		return fmt.Errorf("%w: column ids must be ascending", dberrors.ErrInvalidArgument)
	}
	delta := columnID - w.last
	if delta == 0 {
		return fmt.Errorf("%w: zero column delta", dberrors.ErrInvalidArgument)
	}
	if delta <= 15 {
		w.buf = append(w.buf, byte(delta<<4)|byte(typ))
	} else {
		w.buf = append(w.buf, byte(typ))
		w.buf = appendUvarint(w.buf, uint64(delta))
	}
	w.last = columnID
	return nil
}

// Scanner decodes a family payload one column at a time.
type Scanner struct {
	data     []byte
	columnID uint16
	typ      Type
	bytes    []byte
	u64      uint64
	boolVal  bool
	u8       uint8
	err      error
	ok       bool
}

// NewScanner returns a scanner over payload.
func NewScanner(payload []byte) *Scanner {
	return &Scanner{data: payload}
}

// Next advances to the next column.
func (s *Scanner) Next() bool {
	if s == nil || s.err != nil || len(s.data) == 0 {
		if s != nil {
			s.ok = false
		}
		return false
	}
	s.ok = false
	s.bytes = nil
	s.u64 = 0
	s.boolVal = false
	s.u8 = 0

	tag := s.data[0]
	s.data = s.data[1:]
	delta := uint16(tag >> 4)
	s.typ = Type(tag & 0x0f)
	if delta == 0 {
		extended, rest, ok := readUvarint(s.data)
		if !ok || extended == 0 || extended > uint64(^uint16(0)) {
			s.err = fmt.Errorf("%w: invalid extended column delta", dberrors.ErrCorruptValue)
			return false
		}
		delta = uint16(extended)
		s.data = rest
	}
	s.columnID += delta

	s.ok = s.readValue()
	return s.ok
}

// OK reports whether the scanner is positioned at a valid column.
func (s *Scanner) OK() bool {
	return s != nil && s.ok
}

// Err returns the first scanner error.
func (s *Scanner) Err() error {
	if s == nil {
		return nil
	}
	return s.err
}

// ColumnID returns the current column ID.
func (s *Scanner) ColumnID() uint16 {
	if s == nil {
		return 0
	}
	return s.columnID
}

// Type returns the current column type.
func (s *Scanner) Type() Type {
	if s == nil {
		return 0
	}
	return s.typ
}

// String returns the current value as a string.
func (s *Scanner) String() (string, error) {
	if err := s.require(TypeString); err != nil {
		return "", err
	}
	return string(s.bytes), nil
}

// Bytes returns the current value as a copied byte slice.
func (s *Scanner) Bytes() ([]byte, error) {
	if err := s.require(TypeBytes); err != nil {
		return nil, err
	}
	return append([]byte(nil), s.bytes...), nil
}

// Int64 returns the current value as an int64.
func (s *Scanner) Int64() (int64, error) {
	if err := s.require(TypeInt64); err != nil {
		return 0, err
	}
	return decodeZigZagInt64(s.u64), nil
}

// Uint64 returns the current value as a uint64.
func (s *Scanner) Uint64() (uint64, error) {
	if err := s.require(TypeUint64); err != nil {
		return 0, err
	}
	return s.u64, nil
}

// Bool returns the current value as a bool.
func (s *Scanner) Bool() (bool, error) {
	if err := s.require(TypeBool); err != nil {
		return false, err
	}
	return s.boolVal, nil
}

// Uint8 returns the current value as a uint8.
func (s *Scanner) Uint8() (uint8, error) {
	if err := s.require(TypeUint8); err != nil {
		return 0, err
	}
	return s.u8, nil
}

func (s *Scanner) readValue() bool {
	switch s.typ {
	case TypeString, TypeBytes:
		length, rest, ok := readUvarint(s.data)
		if !ok {
			s.err = fmt.Errorf("%w: invalid bytes length", dberrors.ErrCorruptValue)
			return false
		}
		if uint64(len(rest)) < length {
			s.err = fmt.Errorf("%w: bytes payload truncated", dberrors.ErrCorruptValue)
			return false
		}
		s.bytes = append([]byte(nil), rest[:length]...)
		s.data = rest[length:]
		return true
	case TypeInt64, TypeUint64:
		value, rest, ok := readUvarint(s.data)
		if !ok {
			s.err = fmt.Errorf("%w: invalid varint", dberrors.ErrCorruptValue)
			return false
		}
		s.u64 = value
		s.data = rest
		return true
	case TypeBool:
		if len(s.data) < 1 {
			s.err = fmt.Errorf("%w: bool payload truncated", dberrors.ErrCorruptValue)
			return false
		}
		s.boolVal = s.data[0] != 0
		s.data = s.data[1:]
		return true
	case TypeUint8:
		if len(s.data) < 1 {
			s.err = fmt.Errorf("%w: uint8 payload truncated", dberrors.ErrCorruptValue)
			return false
		}
		s.u8 = s.data[0]
		s.data = s.data[1:]
		return true
	default:
		s.err = fmt.Errorf("%w: unsupported value type %d", dberrors.ErrCorruptValue, s.typ)
		return false
	}
}

func (s *Scanner) require(typ Type) error {
	if s == nil || !s.ok {
		return fmt.Errorf("%w: scanner is not positioned", dberrors.ErrCorruptValue)
	}
	if s.typ != typ {
		return fmt.Errorf("%w: column %d type %d is not %d", dberrors.ErrCorruptValue, s.columnID, s.typ, typ)
	}
	return nil
}
