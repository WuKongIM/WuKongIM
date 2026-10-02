package channel

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

// MQTTReplayRepairer repairs one accepted interval on an exact current replica.
// Its result proves local content, never source release or migration readiness.
type MQTTReplayRepairer interface {
	RepairMQTTReplay(context.Context, MQTTReplayRepairRequest) (MQTTReplayPrefix, error)
}

// MQTTReplayRepairRequest identifies one bounded interval and two current
// replicas. Neither caller nor donor supplies the receiver's expected digest.
type MQTTReplayRepairRequest struct {
	Target, Donor  NodeID
	AnchorPosition uint64
	Request        MQTTReplayRequest
}

// Valid rejects implicit identities, recursive self-donation and unbounded work.
func (q MQTTReplayRepairRequest) Valid() bool {
	r := q.Request
	return q.Target != 0 && q.Donor != 0 && q.Target != q.Donor && q.AnchorPosition > r.Range.Through && r.Valid() && len(r.ChannelID.ID) <= 1024 && utf8.ValidString(r.ChannelID.ID) && !strings.ContainsRune(r.ChannelID.ID, 0)
}

// AcceptsPrefix checks result association only, not committed content evidence.
func (q MQTTReplayRepairRequest) AcceptsPrefix(p MQTTReplayPrefix) bool {
	r := q.Request.Range
	return q.Valid() && p.Generation == r.Generation && p.StartAfter < r.From && p.Through == r.Through && p.TotalStoredBytes > 0 && p.TotalBytes <= p.TotalStoredBytes && p.Digest != [32]byte{}
}

// AcceptsAnchor checks a storage-verified proof against the requested interval
// and current authority order; older committed authority remains valid.
func (q MQTTReplayRepairRequest) AcceptsAnchor(p MQTTReplayAnchorProof) bool {
	m, r := p.Manifest, q.Request
	if !q.AcceptsPrefix(p.Prefix()) || !p.Anchor.Valid() || !m.StructurallyValid() || m.Version != quorumlog.MQTTReplayAnchorProposalManifestVersion || m.LastOffset != q.AnchorPosition {
		return false
	}
	for _, pair := range [][2]uint64{{m.ChannelEpoch, r.ExpectedChannelEpoch}, {m.LeaderTerm, r.ExpectedLeaderEpoch}, {m.FenceVersion, r.ExpectedRouteGeneration}} {
		if pair[0] < pair[1] {
			break
		}
		if pair[0] > pair[1] {
			return false
		}
	}
	return true
}
