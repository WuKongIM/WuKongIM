package codec

import (
	"bytes"
	"errors"
	"io"
	"math"
	"strings"
	"testing"
)

func TestEncoderDecoderPrimitiveWireRoundTrip(t *testing.T) {
	enc := NewEncoder()
	enc.WriteUint8(0xab)
	enc.WriteInt16(-2)
	enc.WriteUint16(0xfedc)
	enc.WriteInt32(-3)
	enc.WriteUint32(0xfedcba98)
	enc.WriteInt64(-4)
	enc.WriteUint64(0xfedcba9876543210)
	enc.WriteString("channel")
	enc.WriteBinary([]byte{0, 1, 2})
	enc.WriteVariable(321)
	enc.WriteStringAll("tail")
	enc.End()

	if got, want := enc.Len(), len(enc.Bytes()); got != want {
		t.Fatalf("Encoder.Len() = %d, want %d", got, want)
	}
	dec := NewDecoder(enc.Bytes())
	assertDecodedValue(t, "uint8", uint8(0xab), dec.Uint8)
	assertDecodedValue(t, "int16", int16(-2), dec.Int16)
	assertDecodedValue(t, "uint16", uint16(0xfedc), dec.Uint16)
	assertDecodedValue(t, "int32", int32(-3), dec.Int32)
	assertDecodedValue(t, "uint32", uint32(0xfedcba98), dec.Uint32)
	assertDecodedValue(t, "int64", int64(-4), dec.Int64)
	assertDecodedValue(t, "uint64", uint64(0xfedcba9876543210), dec.Uint64)
	assertDecodedValue(t, "string", "channel", dec.String)
	gotBinary, err := dec.Binary()
	if err != nil || !bytes.Equal(gotBinary, []byte{0, 1, 2}) {
		t.Fatalf("decode binary = (%v, %v), want [0 1 2]", gotBinary, err)
	}
	assertDecodedValue(t, "variable", uint64(321), dec.Variable)
	assertDecodedValue(t, "string-all", "tail", dec.StringAll)
	if got := dec.Len(); got != 0 {
		t.Fatalf("Decoder.Len() = %d after complete decode, want 0", got)
	}
}

func assertDecodedValue[T comparable](t *testing.T, name string, want T, read func() (T, error)) {
	t.Helper()
	got, err := read()
	if err != nil {
		t.Fatalf("decode %s error = %v", name, err)
	}
	if got != want {
		t.Fatalf("decode %s = %v, want %v", name, got, want)
	}
}

func TestDecoderRejectsTruncatedOrInvalidPrimitiveValues(t *testing.T) {
	tests := []struct {
		name string
		read func() error
	}{
		{name: "uint8", read: func() error { _, err := NewDecoder(nil).Uint8(); return err }},
		{name: "int16", read: func() error { _, err := NewDecoder([]byte{1}).Int16(); return err }},
		{name: "uint16", read: func() error { _, err := NewDecoder([]byte{1}).Uint16(); return err }},
		{name: "int32", read: func() error { _, err := NewDecoder([]byte{1, 2, 3}).Int32(); return err }},
		{name: "uint32", read: func() error { _, err := NewDecoder([]byte{1, 2, 3}).Uint32(); return err }},
		{name: "int64", read: func() error { _, err := NewDecoder(make([]byte, 7)).Int64(); return err }},
		{name: "uint64", read: func() error { _, err := NewDecoder(make([]byte, 7)).Uint64(); return err }},
		{name: "bytes", read: func() error { _, err := NewDecoder([]byte{1}).Bytes(2); return err }},
		{name: "binary prefix", read: func() error { _, err := NewDecoder([]byte{0}).Binary(); return err }},
		{name: "binary negative length", read: func() error { _, err := NewDecoder([]byte{0xff, 0xff}).Binary(); return err }},
		{name: "binary truncated body", read: func() error { _, err := NewDecoder([]byte{0, 2, 1}).Binary(); return err }},
		{name: "string truncated body", read: func() error { _, err := NewDecoder([]byte{0, 2, 'a'}).String(); return err }},
		{name: "variable truncated", read: func() error { _, err := NewDecoder([]byte{0x80}).Variable(); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.read(); err == nil {
				t.Fatal("decode error = nil, want malformed input rejection")
			}
		})
	}

	dec := NewDecoder([]byte{1, 2, 3})
	got, err := dec.Bytes(2)
	if err != nil || !bytes.Equal(got, []byte{1, 2}) || dec.Len() != 1 {
		t.Fatalf("Bytes(2) = (%v, %v), remaining=%d", got, err, dec.Len())
	}
}

func TestEncoderLengthPrefixesAndWriterErrors(t *testing.T) {
	opts := NewEncodeOptions()
	if opts.Cap != 100 {
		t.Fatalf("default encoder capacity = %d, want 100", opts.Cap)
	}
	EcodeWithCap(4096)(opts)
	if opts.Cap != 4096 {
		t.Fatalf("configured encoder capacity = %d, want 4096", opts.Cap)
	}

	enc := NewEncoder()
	enc.WriteString("")
	enc.WriteBinary(nil)
	if got := enc.Bytes(); !bytes.Equal(got, []byte{0, 0, 0, 0}) {
		t.Fatalf("empty length prefixes = %v, want four zero bytes", got)
	}
	assertPanics(t, "oversized string", func() { NewEncoder().WriteString(strings.Repeat("x", math.MaxInt16+1)) })
	assertPanics(t, "oversized binary", func() { NewEncoder().WriteBinary(make([]byte, math.MaxInt16+1)) })

	w := &failOnWrite{failAt: 1}
	if err := WriteUint32(7, w); !errors.Is(err, errWriterRejected) {
		t.Fatalf("WriteUint32() error = %v, want writer error", err)
	}
	w = &failOnWrite{failAt: 1}
	if err := WriteInt16(7, w); !errors.Is(err, errWriterRejected) {
		t.Fatalf("WriteInt16() error = %v, want writer error", err)
	}
	w = &failOnWrite{failAt: 1}
	if err := WriteBinary(nil, w); !errors.Is(err, errWriterRejected) {
		t.Fatalf("WriteBinary(nil) error = %v, want prefix writer error", err)
	}
	w = &failOnWrite{failAt: 2}
	if err := WriteBinary([]byte("body"), w); !errors.Is(err, errWriterRejected) {
		t.Fatalf("WriteBinary(body) error = %v, want body writer error", err)
	}

	var buf bytes.Buffer
	if err := WriteBinary([]byte("ok"), &buf); err != nil {
		t.Fatalf("WriteBinary() error = %v", err)
	}
	if got := buf.Bytes(); !bytes.Equal(got, []byte{0, 2, 'o', 'k'}) {
		t.Fatalf("WriteBinary() bytes = %v", got)
	}
}

func assertPanics(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatalf("%s did not panic", name)
		}
	}()
	fn()
}

var errWriterRejected = errors.New("writer rejected data")

type failOnWrite struct {
	writes int
	failAt int
}

func (w *failOnWrite) Write(p []byte) (int, error) {
	w.writes++
	if w.writes == w.failAt {
		return 0, errWriterRejected
	}
	return len(p), nil
}

var _ io.Writer = (*failOnWrite)(nil)
