package engine_test

import (
	"bytes"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

func TestBatchSetCommitAndGet(t *testing.T) {
	db := openTestDB(t)
	batch := db.NewBatch()
	if err := batch.Set([]byte("k1"), []byte("v1")); err != nil {
		t.Fatalf("Set(): %v", err)
	}
	if err := batch.Commit(false); err != nil {
		t.Fatalf("Commit(): %v", err)
	}
	got, ok, err := db.Get([]byte("k1"))
	if err != nil || !ok || string(got) != "v1" {
		t.Fatalf("Get() = %q ok=%v err=%v", got, ok, err)
	}
}

func TestBatchSetDeferred(t *testing.T) {
	db := openTestDB(t)
	batch := db.NewBatch()
	if err := batch.SetDeferred(2, 2, func(key, value []byte) error {
		copy(key, "kd")
		copy(value, "vd")
		return nil
	}); err != nil {
		t.Fatalf("SetDeferred(): %v", err)
	}
	if err := batch.Commit(false); err != nil {
		t.Fatalf("Commit(): %v", err)
	}
	got, ok, err := db.Get([]byte("kd"))
	if err != nil || !ok || string(got) != "vd" {
		t.Fatalf("Get() = %q ok=%v err=%v", got, ok, err)
	}
}

func TestIteratorHonorsBounds(t *testing.T) {
	db := openTestDB(t)
	batch := db.NewBatch()
	for _, kv := range [][2]string{{"a1", "1"}, {"b1", "2"}, {"b2", "3"}, {"c1", "4"}} {
		if err := batch.Set([]byte(kv[0]), []byte(kv[1])); err != nil {
			t.Fatalf("Set(): %v", err)
		}
	}
	if err := batch.Commit(false); err != nil {
		t.Fatalf("Commit(): %v", err)
	}

	iter, err := db.NewIter(engine.Span{Start: []byte("b"), End: []byte("c")}, engine.IterOptions{})
	if err != nil {
		t.Fatalf("NewIter(): %v", err)
	}
	defer iter.Close()

	var keys []string
	for ok := iter.First(); ok; ok = iter.Next() {
		keys = append(keys, string(iter.Key()))
	}
	if err := iter.Error(); err != nil {
		t.Fatalf("iter error: %v", err)
	}
	if want := []string{"b1", "b2"}; !equalStrings(keys, want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}
}

func TestDeleteRange(t *testing.T) {
	db := openTestDB(t)
	batch := db.NewBatch()
	for _, key := range []string{"p1", "p2", "q1"} {
		if err := batch.Set([]byte(key), []byte("v")); err != nil {
			t.Fatalf("Set(): %v", err)
		}
	}
	if err := batch.DeleteRange(engine.Span{Start: []byte("p"), End: []byte("q")}); err != nil {
		t.Fatalf("DeleteRange(): %v", err)
	}
	if err := batch.Commit(false); err != nil {
		t.Fatalf("Commit(): %v", err)
	}
	if _, ok, err := db.Get([]byte("p1")); err != nil || ok {
		t.Fatalf("p1 ok=%v err=%v, want deleted", ok, err)
	}
	if got, ok, err := db.Get([]byte("q1")); err != nil || !ok || !bytes.Equal(got, []byte("v")) {
		t.Fatalf("q1 = %q ok=%v err=%v", got, ok, err)
	}
}

func TestOpenReadOnlyRejectsWrites(t *testing.T) {
	dir := t.TempDir()
	db, err := engine.Open(dir, engine.Options{})
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	batch := db.NewBatch()
	if err := batch.Set([]byte("k"), []byte("v")); err != nil {
		t.Fatalf("Set(): %v", err)
	}
	if err := batch.Commit(true); err != nil {
		t.Fatalf("Commit(): %v", err)
	}
	if err := batch.Close(); err != nil {
		t.Fatalf("Close batch(): %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}

	readOnly, err := engine.Open(dir, engine.Options{ReadOnly: true})
	if err != nil {
		t.Fatalf("Open(read-only): %v", err)
	}
	defer readOnly.Close()

	got, ok, err := readOnly.Get([]byte("k"))
	if err != nil {
		t.Fatalf("Get(): %v", err)
	}
	if !ok || string(got) != "v" {
		t.Fatalf("Get() = %q, %v; want v, true", got, ok)
	}
	roBatch := readOnly.NewBatch()
	defer roBatch.Close()
	if err := roBatch.Set([]byte("next"), []byte("value")); err != nil {
		t.Fatalf("Set() should stage before Pebble rejects commit: %v", err)
	}
	if err := roBatch.Commit(true); err == nil {
		t.Fatal("Commit(read-only) = nil, want error")
	}
}

func openTestDB(t *testing.T) *engine.DB {
	t.Helper()
	db, err := engine.Open(t.TempDir(), engine.Options{})
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close(): %v", err)
		}
	})
	return db
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
