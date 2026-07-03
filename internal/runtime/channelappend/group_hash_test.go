package channelappend

import (
	"hash/fnv"
	"testing"
)

func TestHashString64MatchesFNV(t *testing.T) {
	cases := []string{"", "a", "2:bench-hot", "2:" + string(make([]byte, 256))}
	for _, c := range cases {
		want := fnv.New64a()
		_, _ = want.Write([]byte(c))
		if got := hashString64(c); got != want.Sum64() {
			t.Fatalf("hashString64(%q) = %d, want %d", c, got, want.Sum64())
		}
	}
}

func TestHashString64NoAllocs(t *testing.T) {
	key := "2:bench-hot"
	allocs := testing.AllocsPerRun(100, func() {
		_ = hashString64(key)
	})
	if allocs != 0 {
		t.Fatalf("hashString64 allocs = %v, want 0", allocs)
	}
}
