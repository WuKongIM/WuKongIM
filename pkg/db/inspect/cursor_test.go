package inspect

import (
	"errors"
	"reflect"
	"testing"
)

func TestCursorMetaRoundTrip(t *testing.T) {
	query, err := Parse("select uid from meta.user where uid='u1' limit 10")
	if err != nil {
		t.Fatalf("Parse() err = %v", err)
	}
	query.Limit = normalizeLimit(Options{}, query.Limit)
	payload := cursorPayload{
		Version:   1,
		Domain:    "meta",
		Table:     "user",
		ScanMode:  scanModePointPartition,
		HashSlot:  7,
		Primary:   []any{"u1"},
		QueryHash: queryHash(query),
	}

	raw, err := encodeCursor(payload)
	if err != nil {
		t.Fatalf("encodeCursor() err = %v", err)
	}
	got, err := decodeCursor(raw, query)
	if err != nil {
		t.Fatalf("decodeCursor() err = %v", err)
	}
	if got.HashSlot != payload.HashSlot || got.ScanMode != payload.ScanMode || !reflect.DeepEqual(got.Primary, payload.Primary) {
		t.Fatalf("decodeCursor() = %+v, want %+v", got, payload)
	}
}

func TestCursorNumericPrimaryRoundTrip(t *testing.T) {
	query, err := Parse("select * from meta.conversation where uid='u1' limit 10")
	if err != nil {
		t.Fatalf("Parse() err = %v", err)
	}
	query.Limit = normalizeLimit(Options{}, query.Limit)
	raw, err := encodeCursor(cursorPayload{
		Domain:    "meta",
		Table:     "conversation",
		ScanMode:  scanModePointPartition,
		HashSlot:  3,
		Primary:   []any{"u1", "g1", int64(2)},
		QueryHash: queryHash(query),
	})
	if err != nil {
		t.Fatalf("encodeCursor() err = %v", err)
	}

	got, err := decodeCursor(raw, query)
	if err != nil {
		t.Fatalf("decodeCursor() err = %v", err)
	}
	if !reflect.DeepEqual(got.Primary, []any{"u1", "g1", int64(2)}) {
		t.Fatalf("Primary = %#v, want string/string/int64", got.Primary)
	}
}

func TestCursorRejectsQueryMismatch(t *testing.T) {
	query, err := Parse("select uid from meta.user where uid='u1' limit 10")
	if err != nil {
		t.Fatalf("Parse() err = %v", err)
	}
	query.Limit = normalizeLimit(Options{}, query.Limit)
	raw, err := encodeCursor(cursorPayload{
		Version:   1,
		Domain:    "meta",
		Table:     "user",
		ScanMode:  scanModePointPartition,
		HashSlot:  7,
		Primary:   []any{"u1"},
		QueryHash: queryHash(query),
	})
	if err != nil {
		t.Fatalf("encodeCursor() err = %v", err)
	}
	other, err := Parse("select uid from meta.user where uid='u2' limit 10")
	if err != nil {
		t.Fatalf("Parse(other) err = %v", err)
	}
	other.Limit = normalizeLimit(Options{}, other.Limit)

	_, err = decodeCursor(raw, other)
	if !errors.Is(err, ErrCursorMismatch) {
		t.Fatalf("decodeCursor() err = %v, want ErrCursorMismatch", err)
	}
}

func TestCursorRejectsMalformed(t *testing.T) {
	query, err := Parse("select uid from meta.user limit 10")
	if err != nil {
		t.Fatalf("Parse() err = %v", err)
	}

	_, err = decodeCursor("not valid base64", query)
	if !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("decodeCursor() err = %v, want ErrInvalidQuery", err)
	}
}
