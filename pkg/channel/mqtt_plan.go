package channel

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

// MQTTReplayPlanner captures a coherent accepted-prefix view on the recovered
// leader. Entry adapters must establish fresh authority around this call.
type MQTTReplayPlanner interface {
	PlanMQTTReplay(context.Context, MQTTReplayPlanRequest) (MQTTReplayPlan, error)
}

// MQTTReplayPlanRequest selects exact authority and source, never a caller HW.
type MQTTReplayPlanRequest struct {
	ChannelID ChannelID
	// Expected fences must still identify the same recovered authority at completion.
	ExpectedChannelEpoch, ExpectedLeaderEpoch, ExpectedRouteGeneration uint64
	Generation                                                         string
}

// Valid rejects unbounded identities, implicit authority and noncanonical sources.
func (q MQTTReplayPlanRequest) Valid() bool {
	return q.ChannelID.ID != "" && len(q.ChannelID.ID) <= 1024 && utf8.ValidString(q.ChannelID.ID) && !strings.ContainsRune(q.ChannelID.ID, 0) && q.ChannelID.Type != 0 &&
		q.ExpectedChannelEpoch != 0 && q.ExpectedLeaderEpoch != 0 && q.ExpectedRouteGeneration != 0 && validMQTTReplayGeneration(q.Generation)
}

// MQTTReplayPlan contains only committed source and journal evidence. Local
// replay copying may be ahead and cannot change the accepted starting position.
type MQTTReplayPlan struct {
	// Source retains the reactor-captured HW even if later appends have committed.
	Source MQTTSourceSnapshot
	Anchor MQTTReplayAnchorProof
	// HasAnchor distinguishes a verified latest journal row from its absence.
	HasAnchor bool
}

func (p MQTTReplayPlan) valid() bool {
	if !validMQTTReplayGeneration(p.Source.Generation) || p.Source.StartAfter >= p.Source.CommittedThrough {
		return false
	}
	if !p.HasAnchor {
		return p.Anchor == (MQTTReplayAnchorProof{})
	}
	a, m := p.Anchor.Anchor, p.Anchor.Manifest
	return a.Valid() && m.StructurallyValid() && m.Version == quorumlog.MQTTReplayAnchorProposalManifestVersion && a.Through < m.LastOffset &&
		m.LastOffset <= p.Source.CommittedThrough && p.Anchor.Prefix().Generation == p.Source.Generation && a.StartAfter == p.Source.StartAfter
}

// ValidFor validates result association; only the store/runtime proves durability.
func (p MQTTReplayPlan) ValidFor(q MQTTReplayPlanRequest) bool {
	if !q.Valid() || !p.valid() || p.Source.Generation != q.Generation {
		return false
	}
	if p.HasAnchor {
		m := p.Anchor.Manifest
		for _, pair := range [][2]uint64{{m.ChannelEpoch, q.ExpectedChannelEpoch}, {m.LeaderTerm, q.ExpectedLeaderEpoch}, {m.FenceVersion, q.ExpectedRouteGeneration}} {
			if pair[0] < pair[1] {
				break
			}
			if pair[0] > pair[1] {
				return false
			}
		}
	}
	return true
}

// NextRange bounds the next copy page from accepted progress. A lone latest
// control is idle; later content includes that control in the next copy range.
func (p MQTTReplayPlan) NextRange(limit, maxBytes int) (MQTTReplayRange, bool, error) {
	if !p.valid() || limit < 1 || limit > 256 || maxBytes < 1 || maxBytes > 16<<20 {
		return MQTTReplayRange{}, false, ErrInvalidConfig
	}
	from := p.Source.StartAfter + 1
	if p.HasAnchor {
		from = p.Anchor.Anchor.Through + 1
		if from == p.Anchor.Manifest.LastOffset && from == p.Source.CommittedThrough {
			return MQTTReplayRange{}, false, nil
		}
	}
	return MQTTReplayRange{Generation: p.Source.Generation, From: from, Through: p.Source.CommittedThrough, Limit: limit, MaxBytes: maxBytes}, true, nil
}
