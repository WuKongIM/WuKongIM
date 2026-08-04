package chatlifecycle

import (
	"testing"
)

func assertPercentPair(t *testing.T, name string, first, second, wantFirst, wantSecond int) {
	t.Helper()
	if first != wantFirst || second != wantSecond {
		t.Fatalf("%s = %d/%d, want %d/%d", name, first, second, wantFirst, wantSecond)
	}
}

func assertDurationBuckets(t *testing.T, name string, got, want []DurationShare) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s buckets = %+v, want %+v", name, got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("%s buckets[%d] = %+v, want %+v", name, index, got[index], want[index])
		}
	}
}

func samePayloads(got, want []PayloadShare) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range want {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
