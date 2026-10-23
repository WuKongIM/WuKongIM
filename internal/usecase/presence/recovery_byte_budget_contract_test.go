package presence

import "testing"

// This fast resource-admission guard supplements the exported integration test
// that fills the real owned-byte ledger with free cohort and key capacity.
func TestRecoveryOwnedUIDByteBudgetRemainsIndependentOfWidth(t *testing.T) {
	if recoveryMaxUIDBytes != 128<<20 {
		t.Fatalf("owned UID byte budget=%d, want fixed 128 MiB", recoveryMaxUIDBytes)
	}
	if recoveryPageUIDBytes != 32<<20 {
		t.Fatalf("page UID byte budget=%d, want fixed 32 MiB", recoveryPageUIDBytes)
	}
}
