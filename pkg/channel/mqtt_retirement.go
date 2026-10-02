package channel

import "github.com/WuKongIM/WuKongIM/pkg/quorumlog"

// MQTTReplayRetirementProof retains a committed consumer-retirement decision.
// Its journal is independent of original history; physical GC and admission
// against current consumer authority remain separate capabilities.
type MQTTReplayRetirementProof struct {
	Retirement quorumlog.MQTTReplayRetirement
	Manifest   ProposalManifest
}

// MQTTReplayRetirementResult separates the replicated retired baseline from
// bounded replica-local cleanup. Done reports engine removal, not disk compaction.
type MQTTReplayRetirementResult struct {
	// RetirementPosition identifies the committed decision backing Retired.
	RetirementPosition uint64
	Retired            MQTTReplayPrefix
	// DeletedThrough tracks engine removal; Deleted counts this call's primary rows.
	DeletedThrough uint64
	Deleted        int
	Done           bool
}
