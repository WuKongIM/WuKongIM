package backup

import "testing"

func TestCloneRestorePointsDetachesVerificationEvidence(t *testing.T) {
	evidence := &VerificationEvidence{FailureCategory: "original"}
	source := []RestorePoint{{ID: "rp-1", LastVerification: evidence}}

	cloned := CloneRestorePoints(source)
	cloned[0].LastVerification.FailureCategory = "changed"

	if source[0].LastVerification.FailureCategory != "original" {
		t.Fatalf("source evidence = %q, want detached original", source[0].LastVerification.FailureCategory)
	}
}
