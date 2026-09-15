//go:build !race

package message

import "testing"

// Production allocation accounting excludes race instrumentation. Each key is
// deliberately retained so compiler escape analysis cannot remove its storage.
func TestMessageReadKeyAllocationBudget(t *testing.T) {
	for name, encode := range map[string]func() []byte{
		"row":        func() []byte { return encodeMessageRowKey("cohort-1-channel-199:2", 123, 0) },
		"checkpoint": func() []byte { return encodeCheckpointKey("cohort-1-channel-199:2") },
		"retention":  func() []byte { return encodeRetentionStateKey("cohort-1-channel-199:2") },
		"rank":       func() []byte { return encodeMessageIndexPrefix("cohort-1-channel-199:2", messageIndexIDNonBusinessSeq) },
	} {
		t.Run(name, func(t *testing.T) {
			var key []byte
			allocations := testing.AllocsPerRun(30, func() { key = encode() })
			if len(key) == 0 {
				t.Fatal("empty encoded key")
			}
			if allocations > 1 {
				t.Fatalf("key allocations = %.0f, want <= 1", allocations)
			}
		})
	}
}
