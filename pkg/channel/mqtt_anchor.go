package channel

import "github.com/WuKongIM/WuKongIM/pkg/quorumlog"

// MQTTReplayAnchorProof retains one committed content checkpoint independently
// of ordinary history. Current membership and release admission stay separate.
type MQTTReplayAnchorProof struct {
	Anchor   quorumlog.MQTTReplayAnchor
	Manifest ProposalManifest
}
