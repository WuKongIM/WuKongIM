package lifecycle

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestResourceStackClosesInReverseOrder(t *testing.T) {
	var calls []string
	var stack ResourceStack
	stack.Push("first", func() error {
		calls = append(calls, "first")
		return nil
	})
	stack.Push("second", func() error {
		calls = append(calls, "second")
		return nil
	})
	stack.Push("third", func() error {
		calls = append(calls, "third")
		return nil
	})

	if err := stack.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}

	want := []string{"third", "second", "first"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestResourceStackAggregatesCloseErrorsWithNames(t *testing.T) {
	firstErr := errors.New("first failed")
	thirdErr := errors.New("third failed")
	var stack ResourceStack
	stack.Push("first resource", func() error { return firstErr })
	stack.Push("second resource", func() error { return nil })
	stack.Push("third resource", func() error { return thirdErr })

	err := stack.Close()
	if err == nil {
		t.Fatal("close succeeded, want error")
	}
	if !errors.Is(err, firstErr) || !errors.Is(err, thirdErr) {
		t.Fatalf("close err = %v, want wrapped close errors", err)
	}
	if got := err.Error(); !strings.Contains(got, "first resource") || !strings.Contains(got, "third resource") {
		t.Fatalf("close err = %q, want resource names", got)
	}
}

func TestResourceStackReleaseTransfersOwnership(t *testing.T) {
	var calls int
	var stack ResourceStack
	stack.Push("owned", func() error {
		calls++
		return nil
	})

	stack.Release()

	if err := stack.Close(); err != nil {
		t.Fatalf("close after release failed: %v", err)
	}
	if calls != 0 {
		t.Fatalf("close calls = %d, want 0", calls)
	}
}

func TestResourceStackCloseIsIdempotent(t *testing.T) {
	var calls int
	var stack ResourceStack
	stack.Push("only", func() error {
		calls++
		return nil
	})

	if err := stack.Close(); err != nil {
		t.Fatalf("first close failed: %v", err)
	}
	if err := stack.Close(); err != nil {
		t.Fatalf("second close failed: %v", err)
	}
	if calls != 1 {
		t.Fatalf("close calls = %d, want 1", calls)
	}
}
