package cluster

import "time"

const (
	// recoveryOwnerReadBudget tolerates transient dispatch and response scheduling
	// within the existing activation budget. All pages and compatibility reads for
	// one owner share this deadline; it never permits a retry or partial proof.
	recoveryOwnerReadBudget = time.Second
	// recoveryReadChainBudget bounds admission and the sequential owner-read chain,
	// rather than multiplying the owner timeout by up to 256 members. Final proof
	// validation uses the original parent. Readers must cooperate with cancellation;
	// ownership and result credits remain held until the real read returns.
	recoveryReadChainBudget = 2 * time.Second
)

// allRecoveryCallsFailed reports irreversible original-call failures, not an
// individual group's error. Later owners cannot restore these calls' proofs.
func allRecoveryCallsFailed(proofs []recoveryCallProof) bool {
	if len(proofs) == 0 {
		return false
	}
	for _, proof := range proofs {
		if !proof.failed {
			return false
		}
	}
	return true
}
