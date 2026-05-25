package rowcodec_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
)

func TestEnvelopeDetectsChecksumMismatch(t *testing.T) {
	key := []byte("k")
	value := rowcodec.Wrap(key, 1, rowcodec.CodecColumns, rowcodec.FlagChecksum, []byte("payload"))
	value[len(value)-1] ^= 0xff
	_, err := rowcodec.Unwrap(key, value)
	if !errors.Is(err, db.ErrChecksumMismatch) {
		t.Fatalf("err = %v, want checksum mismatch", err)
	}
}

func TestEnvelopeRoundTrip(t *testing.T) {
	key := []byte("row-key")
	payload := []byte("payload")
	value := rowcodec.Wrap(key, 7, rowcodec.CodecRaw, rowcodec.FlagChecksum, payload)
	env, err := rowcodec.Unwrap(key, value)
	if err != nil {
		t.Fatalf("Unwrap(): %v", err)
	}
	if env.Version != 7 || env.Codec != rowcodec.CodecRaw || !bytes.Equal(env.Payload, payload) {
		t.Fatalf("env = %#v", env)
	}
}

func TestColumnWriterAndScannerRoundTrip(t *testing.T) {
	var w rowcodec.Writer
	if err := w.String(1, "uid-1"); err != nil {
		t.Fatalf("String(): %v", err)
	}
	if err := w.Int64(2, -42); err != nil {
		t.Fatalf("Int64(): %v", err)
	}
	if err := w.Uint64(3, 99); err != nil {
		t.Fatalf("Uint64(): %v", err)
	}
	if err := w.Bool(4, true); err != nil {
		t.Fatalf("Bool(): %v", err)
	}
	if err := w.Uint8(5, 8); err != nil {
		t.Fatalf("Uint8(): %v", err)
	}
	if err := w.RawBytes(6, []byte("raw")); err != nil {
		t.Fatalf("Bytes(): %v", err)
	}

	s := rowcodec.NewScanner(w.Bytes())
	assertNextString(t, s, 1, "uid-1")
	assertNextInt64(t, s, 2, -42)
	assertNextUint64(t, s, 3, 99)
	assertNextBool(t, s, 4, true)
	assertNextUint8(t, s, 5, 8)
	assertNextBytes(t, s, 6, []byte("raw"))
	if s.Next() {
		t.Fatalf("unexpected extra column %d", s.ColumnID())
	}
	if err := s.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}
}

func TestColumnWriterRejectsNonAscendingColumns(t *testing.T) {
	var w rowcodec.Writer
	if err := w.String(2, "first"); err != nil {
		t.Fatalf("String(): %v", err)
	}
	if err := w.String(2, "again"); !errors.Is(err, db.ErrInvalidArgument) {
		t.Fatalf("err = %v, want invalid argument", err)
	}
}

func TestScannerReportsTypeMismatch(t *testing.T) {
	var w rowcodec.Writer
	if err := w.String(1, "not-int"); err != nil {
		t.Fatalf("String(): %v", err)
	}
	s := rowcodec.NewScanner(w.Bytes())
	if !s.Next() {
		t.Fatal("missing first column")
	}
	if _, err := s.Int64(); !errors.Is(err, db.ErrCorruptValue) {
		t.Fatalf("err = %v, want corrupt value", err)
	}
}

func assertNextString(t *testing.T, s *rowcodec.Scanner, columnID uint16, want string) {
	t.Helper()
	if !s.Next() || s.ColumnID() != columnID {
		t.Fatalf("next column = %d ok=%v, want %d", s.ColumnID(), s.OK(), columnID)
	}
	got, err := s.String()
	if err != nil || got != want {
		t.Fatalf("String() = %q, %v, want %q", got, err, want)
	}
}

func assertNextInt64(t *testing.T, s *rowcodec.Scanner, columnID uint16, want int64) {
	t.Helper()
	if !s.Next() || s.ColumnID() != columnID {
		t.Fatalf("next column = %d ok=%v, want %d", s.ColumnID(), s.OK(), columnID)
	}
	got, err := s.Int64()
	if err != nil || got != want {
		t.Fatalf("Int64() = %d, %v, want %d", got, err, want)
	}
}

func assertNextUint64(t *testing.T, s *rowcodec.Scanner, columnID uint16, want uint64) {
	t.Helper()
	if !s.Next() || s.ColumnID() != columnID {
		t.Fatalf("next column = %d ok=%v, want %d", s.ColumnID(), s.OK(), columnID)
	}
	got, err := s.Uint64()
	if err != nil || got != want {
		t.Fatalf("Uint64() = %d, %v, want %d", got, err, want)
	}
}

func assertNextBool(t *testing.T, s *rowcodec.Scanner, columnID uint16, want bool) {
	t.Helper()
	if !s.Next() || s.ColumnID() != columnID {
		t.Fatalf("next column = %d ok=%v, want %d", s.ColumnID(), s.OK(), columnID)
	}
	got, err := s.Bool()
	if err != nil || got != want {
		t.Fatalf("Bool() = %v, %v, want %v", got, err, want)
	}
}

func assertNextUint8(t *testing.T, s *rowcodec.Scanner, columnID uint16, want uint8) {
	t.Helper()
	if !s.Next() || s.ColumnID() != columnID {
		t.Fatalf("next column = %d ok=%v, want %d", s.ColumnID(), s.OK(), columnID)
	}
	got, err := s.Uint8()
	if err != nil || got != want {
		t.Fatalf("Uint8() = %d, %v, want %d", got, err, want)
	}
}

func assertNextBytes(t *testing.T, s *rowcodec.Scanner, columnID uint16, want []byte) {
	t.Helper()
	if !s.Next() || s.ColumnID() != columnID {
		t.Fatalf("next column = %d ok=%v, want %d", s.ColumnID(), s.OK(), columnID)
	}
	got, err := s.Bytes()
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("Bytes() = %q, %v, want %q", got, err, want)
	}
}
