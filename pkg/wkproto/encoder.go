package wkproto

import (
	"bytes"
	"io"
)

// var pool = bytebufferpool.Pool{}

// Encoder 编码者
type Encoder struct {
	w *bytes.Buffer
}

// NewEncoder NewEncoder
func NewEncoder() *Encoder {

	return &Encoder{
		w: bytes.NewBuffer([]byte{}),
	}
}

// Bytes Bytes
func (e *Encoder) Bytes() []byte {
	return e.w.Bytes()
}

// Len Len
func (e *Encoder) Len() int {
	return e.w.Len()
}

// WriteByte WriteByte
func (e *Encoder) WriteByte(b byte) error {
	return e.w.WriteByte(b)
}

// WriteInt WriteInt
func (e *Encoder) WriteInt(i int) error {
	return e.w.WriteByte(byte(i))
}

// WriteUint8 WriteUint8
func (e *Encoder) WriteUint8(i uint8) {
	e.WriteInt(int(i))
}

// WriteInt16 WriteInt16
func (e *Encoder) WriteInt16(i int) {
	e.w.Write([]byte{byte(i >> 8), byte(i & 0xFF)})
}

// WriteUint16 WriteUint16
func (e *Encoder) WriteUint16(i uint16) {
	e.WriteInt16(int(i))
}

// WriteInt32 WriteInt32
func (e *Encoder) WriteInt32(i int32) {
	e.w.Write([]byte{
		byte(i >> 24),
		byte(i >> 16),
		byte(i >> 8),
		byte(i & 0xFF),
	})
}

// WriteInt64 WriteInt64
func (e *Encoder) WriteInt64(i int64) {
	e.w.Write([]byte{
		byte(i >> 56),
		byte(i >> 48),
		byte(i >> 40),
		byte(i >> 32),
		byte(i >> 24),
		byte(i >> 16),
		byte(i >> 8),
		byte(i & 0xFF),
	})
}

// WriteUint64 WriteUint64
func (e *Encoder) WriteUint64(i uint64) {
	e.w.Write([]byte{
		byte(i >> 56),
		byte(i >> 48),
		byte(i >> 40),
		byte(i >> 32),
		byte(i >> 24),
		byte(i >> 16),
		byte(i >> 8),
		byte(i & 0xFF),
	})
}

// WriteUint32 WriteUint32
func (e *Encoder) WriteUint32(i uint32) {
	WriteUint32(i, e.w)
}

// WriteString WriteString
func (e *Encoder) WriteString(str string) {
	e.WriteBinary([]byte(str))
}

// WriteStringAll WriteStringAll
func (e *Encoder) WriteStringAll(str string) {
	e.WriteBytes([]byte(str))
}

// WriteBinary WriteBinary
func (e *Encoder) WriteBinary(b []byte) {
	if len(b) == 0 {
		e.WriteInt16(0)
	} else {
		e.WriteInt16(len(b))
		e.w.Write(b)
	}

}

// WriteBytes WriteBytes
func (e *Encoder) WriteBytes(b []byte) {
	e.w.Write(b)
}

// WriteVariable WriteVariable
func (e *Encoder) WriteVariable(v int) {
	b := []byte{}
	for v > 0 {
		digit := v % 0x80
		v /= 0x80
		if v > 0 {
			digit |= 0x80
		}
		b = append(b, byte(digit))
	}
	e.w.Write(b)
}

func (e *Encoder) End() {
	// bytebufferpool.Put(e.w)
}

// WriteUint32 WriteUint32
func WriteUint32(v uint32, w io.Writer) error {
	if _, err := w.Write([]byte{
		byte(v >> 24),
		byte(v >> 16),
		byte(v >> 8),
		byte(v & 0xFF),
	}); err != nil {
		return err
	}
	return nil
}

// WriteBinary WriteBinary
func WriteBinary(b []byte, w io.Writer) error {
	var err error
	if len(b) == 0 {
		err = WriteInt16(0, w)
		if err != nil {
			return err
		}
	} else {
		err = WriteInt16(len(b), w)
		if err != nil {
			return err
		}
		_, err = w.Write(b)
		if err != nil {
			return err
		}
	}
	return nil
}

// WriteInt16 WriteInt16
func WriteInt16(i int, w io.Writer) error {
	_, err := w.Write([]byte{byte(i >> 8), byte(i & 0xFF)})
	return err
}
