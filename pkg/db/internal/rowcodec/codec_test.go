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

func TestEnvelopeWrapToMatchesWrap(t *testing.T) {
	key := []byte("row-key")
	payload := []byte("payload")
	want := rowcodec.Wrap(key, 7, rowcodec.CodecRaw, rowcodec.FlagChecksum, payload)
	got := make([]byte, rowcodec.EnvelopeLen(len(payload)))
	if err := rowcodec.WrapTo(got, key, 7, rowcodec.CodecRaw, rowcodec.FlagChecksum, payload); err != nil {
		t.Fatalf("WrapTo(): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("WrapTo() = %x, want %x", got, want)
	}
	env, err := rowcodec.Unwrap(key, got)
	if err != nil {
		t.Fatalf("Unwrap(WrapTo()): %v", err)
	}
	if env.Version != 7 || env.Codec != rowcodec.CodecRaw || !bytes.Equal(env.Payload, payload) {
		t.Fatalf("env = %#v", env)
	}
}

func TestEnvelopeWrapToRejectsWrongLength(t *testing.T) {
	err := rowcodec.WrapTo(make([]byte, rowcodec.EnvelopeLen(3)-1), []byte("key"), 1, rowcodec.CodecRaw, 0, []byte("abc"))
	if !errors.Is(err, db.ErrInvalidArgument) {
		t.Fatalf("err = %v, want invalid argument", err)
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

func TestBorrowedDecodeKeepsChecksumAndResultOwnership(t *testing.T) {
	var w rowcodec.Writer
	if err := w.String(1, "owned-string"); err != nil {
		t.Fatal(err)
	}
	if err := w.RawBytes(2, []byte("owned-bytes")); err != nil {
		t.Fatal(err)
	}
	key := []byte("borrowed-row")
	raw := rowcodec.Wrap(key, 1, rowcodec.CodecColumns, rowcodec.FlagChecksum, w.Bytes())
	env, err := rowcodec.UnwrapBorrowed(key, raw)
	if err != nil {
		t.Fatal(err)
	}
	s := rowcodec.NewBorrowedScanner(env.Payload)
	if !s.Next() {
		t.Fatal(s.Err())
	}
	text, err := s.String()
	if err != nil {
		t.Fatal(err)
	}
	if !s.Next() {
		t.Fatal(s.Err())
	}
	value, err := s.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	value[0] = 'X'
	again, err := s.Bytes()
	if err != nil || string(again) != "owned-bytes" {
		t.Fatalf("accessor aliases data: %q %v", again, err)
	}
	clear(raw)
	if text != "owned-string" || string(again) != "owned-bytes" {
		t.Fatal("decoded values alias recycled input")
	}
	raw = rowcodec.Wrap(key, 1, rowcodec.CodecColumns, rowcodec.FlagChecksum, w.Bytes())
	raw[len(raw)-1] ^= 0xff
	if _, err := rowcodec.UnwrapBorrowed(key, raw); !errors.Is(err, db.ErrChecksumMismatch) {
		t.Fatalf("corruption accepted: %v", err)
	}
	if _, err := rowcodec.UnwrapBorrowed(key, raw[:3]); !errors.Is(err, db.ErrCorruptValue) {
		t.Fatalf("truncated envelope accepted: %v", err)
	}
}

func TestBorrowedScannerRejectsMalformedColumns(t *testing.T) {
	for _, raw := range [][]byte{{0x11, 0x80}, {0x11, 3, 'a'}, {0x01, 0}, {0x1f}} {
		s := rowcodec.NewBorrowedScanner(raw)
		if s.Next() || !errors.Is(s.Err(), db.ErrCorruptValue) {
			t.Fatalf("malformed column accepted: %x %v", raw, s.Err())
		}
	}
}

func TestBorrowedBytesAvoidsIntermediateCopyAndChecksType(t *testing.T) {
	var w rowcodec.Writer
	if err := w.RawBytes(1, []byte("replica-ids")); err != nil {
		t.Fatal(err)
	}
	if err := w.Uint64(2, 3); err != nil {
		t.Fatal(err)
	}
	raw := w.Bytes()
	scanner := rowcodec.NewBorrowedScanner(raw)
	if !scanner.Next() {
		t.Fatal(scanner.Err())
	}
	view, err := scanner.BorrowedBytes()
	if err != nil {
		t.Fatal(err)
	}
	owned, err := scanner.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	offset := bytes.Index(raw, []byte("replica-ids"))
	if offset < 0 || len(view) != len("replica-ids") || &view[0] != &raw[offset] {
		t.Fatal("borrowed bytes copied the encoded field")
	}
	if &owned[0] == &view[0] {
		t.Fatal("Bytes no longer owns its result")
	}
	if !scanner.Next() {
		t.Fatal(scanner.Err())
	}
	if _, err := scanner.BorrowedBytes(); !errors.Is(err, db.ErrCorruptValue) {
		t.Fatalf("wrong-type accessor: %v", err)
	}
}
