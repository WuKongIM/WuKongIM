package channel

import "github.com/WuKongIM/WuKongIM/pkg/quorumlog"

// MQTTReplayAnchorProof retains one committed content checkpoint independently
// of ordinary history. Current membership and release admission stay separate.
type MQTTReplayAnchorProof struct {
	Anchor   quorumlog.MQTTReplayAnchor
	Manifest ProposalManifest
}

// MQTTReplayAnchorState binds source and two bounded journal lookups to the
// same committed snapshot. Absence is explicit rather than a zero-value proof.
type MQTTReplayAnchorState struct {
	Source                  MQTTSourceSnapshot
	Latest, Requested       MQTTReplayAnchorProof
	HasLatest, HasRequested bool
	// MaintenanceOnly is a pinned proof that the bounded tail after Latest's
	// accepted prefix contains only committed anchor/retirement controls.
	MaintenanceOnly bool
}

// Prefix returns the independently committed immutable content boundary.
func (p MQTTReplayAnchorProof) Prefix() MQTTReplayPrefix {
	a := p.Anchor
	return MQTTReplayPrefix{Generation: quorumlog.MQTTSourceGeneration(a.SourceCommand), StartAfter: a.StartAfter, Through: a.Through, TotalBytes: a.TotalBytes, TotalStoredBytes: a.TotalStoredBytes, Digest: a.Digest}
}
