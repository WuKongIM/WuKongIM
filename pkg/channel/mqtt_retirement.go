package channel

import "github.com/WuKongIM/WuKongIM/pkg/quorumlog"

// MQTTReplayRetirementProof retains a committed consumer-retirement decision.
// Its journal is independent of original history; physical GC and admission
// against current consumer authority remain separate capabilities.
type MQTTReplayRetirementProof struct {
	Retirement quorumlog.MQTTReplayRetirement
	Manifest   ProposalManifest
}
