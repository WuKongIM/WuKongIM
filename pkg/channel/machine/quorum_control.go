package machine

import ch "github.com/WuKongIM/WuKongIM/pkg/channel"

// QuorumControlCommittedResult proves an existing or new internal control under
// the owning append operation. It never maps request records to durable rows.
type QuorumControlCommittedResult struct {
	Fence            ch.Fence
	CommittedThrough uint64
	Err              error
}

// ApplyQuorumControlCommitted publishes a fenced durable control position and
// retires observers without synthesizing message identities for exact retries.
func (s *ChannelState) ApplyQuorumControlCommitted(res QuorumControlCommittedResult) Decision {
	if !s.matchesInflightFence(res.Fence) {
		return Decision{}
	}
	if res.Err != nil {
		return s.failInflightAppend(res.Err)
	}
	if res.CommittedThrough == 0 {
		return s.failInflightAppend(ch.ErrLogConflict)
	}
	s.LEO = maxUint64(s.LEO, res.CommittedThrough)
	s.HW = maxUint64(s.HW, res.CommittedThrough)
	progress := s.Progress[s.LocalNode]
	progress.Match = maxUint64(progress.Match, res.CommittedThrough)
	s.Progress[s.LocalNode] = progress
	var replies []Reply
	for _, op := range s.InflightAppend.WaiterOpIDs {
		if _, ok := s.PendingAppends[op]; ok {
			replies = append(replies, Reply{Kind: ReplyKindAppend, OpID: op})
		}
	}
	s.AbortAppendBatchProposal(res.Fence.OpID)
	return Decision{Replies: replies}
}
