package reviewagentverify_test

import "testing"

// TestReviewAgentRecoveryCanary intentionally fails so the live Review Agent
// must preserve failed mandatory evidence and refuse approval or auto-merge.
func TestReviewAgentRecoveryCanary(t *testing.T) {
	t.Fatal("intentional Review Agent recovery canary failure")
}
