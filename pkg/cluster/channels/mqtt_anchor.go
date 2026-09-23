package channels

import (
	"context"
	"slices"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

// mqttAnchorForwardRequest sends immutable evidence, not authoritative metadata.
// Copy.Leader binds one serving node and prevents recursive forwarding.
type mqttAnchorForwardRequest struct {
	Copy              ch.MQTTReplayCopyReceipt
	MessageID         uint64
	ServerTimestampMS int64
}

func (q mqttAnchorForwardRequest) valid() bool {
	if q.MessageID == 0 || q.ServerTimestampMS <= 0 || !q.Copy.Valid() || len(q.Copy.Copies) < q.Copy.WriteQuorum || !slices.Contains(q.Copy.Copies, q.Copy.Leader) {
		return false
	}
	for i, n := range q.Copy.Copies {
		if n == 0 || (i > 0 && q.Copy.Copies[i-1] >= n) {
			return false
		}
	}
	return true
}

type mqttAnchorForwarder interface {
	ForwardMQTTReplayAnchor(context.Context, ch.NodeID, mqttAnchorForwardRequest) (ch.MQTTReplayAnchorProof, error)
}

// CommitMQTTReplayAnchor admits copy evidence through fresh Slot authority and
// the owning reactor. A post-commit fence failure withholds only the reply.
func (s *Service) CommitMQTTReplayAnchor(ctx context.Context, q ch.MQTTReplayAnchorRequest) (ch.MQTTReplayAnchorProof, error) {
	if !q.Valid() {
		return ch.MQTTReplayAnchorProof{}, ch.ErrInvalidConfig
	}
	forward := mqttAnchorForwardRequest{Copy: q.Copy, MessageID: q.MessageID, ServerTimestampMS: q.ServerTimestampMS}
	// Validation bounds the only retained caller-owned slice before cloning it.
	forward.Copy.Copies = slices.Clone(q.Copy.Copies)
	return s.commitMQTTReplayAnchor(ctx, forward, 0)
}

func (s *Service) handleForwardMQTTReplayAnchor(ctx context.Context, q mqttAnchorForwardRequest) (ch.MQTTReplayAnchorProof, error) {
	if s == nil || q.Copy.Leader == 0 || q.Copy.Leader != s.localNode {
		return ch.MQTTReplayAnchorProof{}, ch.ErrNotLeader
	}
	return s.commitMQTTReplayAnchor(ctx, q, s.localNode)
}

func (s *Service) commitMQTTReplayAnchor(ctx context.Context, q mqttAnchorForwardRequest, serving ch.NodeID) (ch.MQTTReplayAnchorProof, error) {
	var empty ch.MQTTReplayAnchorProof
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if s == nil || !q.valid() {
		return empty, ch.ErrInvalidConfig
	}
	meta, err := s.mqttAnchorMeta(ctx, q)
	if err != nil {
		return empty, err
	}
	if serving != 0 && meta.Leader != serving {
		return empty, ch.ErrNotLeader
	}
	var proof ch.MQTTReplayAnchorProof
	if meta.Leader == s.localNode {
		committer, ok := s.runtime.(ch.MQTTReplayAnchorCommitter)
		if !ok {
			return empty, ch.ErrInvalidConfig
		}
		if err = s.applyRuntimeMetaContext(ctx, meta, true, true); err != nil {
			return empty, err
		}
		proof, err = committer.CommitMQTTReplayAnchor(ctx, ch.MQTTReplayAnchorRequest{Meta: meta, Copy: q.Copy, MessageID: q.MessageID, ServerTimestampMS: q.ServerTimestampMS})
	} else {
		forward, ok := s.forward.(mqttAnchorForwarder)
		if !ok {
			return empty, ch.ErrInvalidConfig
		}
		proof, err = forward.ForwardMQTTReplayAnchor(ctx, meta.Leader, q)
	}
	if err != nil {
		return empty, err
	}
	if err = ctx.Err(); err != nil {
		return empty, err
	}
	// Both reads validate against the same complete copy authority, including
	// ordered placement/status/quorum even if an epoch was erroneously unchanged.
	if _, err = s.mqttAnchorMeta(ctx, q); err != nil {
		return empty, err
	}
	if !validMQTTAnchorProof(q, proof) {
		return empty, ch.ErrLogConflict
	}
	return proof, nil
}

func (s *Service) mqttAnchorMeta(ctx context.Context, q mqttAnchorForwardRequest) (ch.Meta, error) {
	m, err := s.mqttCopyMeta(ctx, q.Copy.Request)
	if err != nil {
		return ch.Meta{}, err
	}
	if !q.Copy.ValidFor(m) {
		return ch.Meta{}, ch.ErrStaleMeta
	}
	return m, nil
}

// validMQTTAnchorProof checks the typed result's association, not durability.
// Only the owning reactor/sequencer may establish the committed journal proof.
func validMQTTAnchorProof(q mqttAnchorForwardRequest, p ch.MQTTReplayAnchorProof) bool {
	m, expected := p.Manifest, q.Copy.Request
	if !p.Anchor.Valid() || !m.StructurallyValid() || m.Version != quorumlog.MQTTReplayAnchorProposalManifestVersion || p.Anchor.Through >= m.LastOffset {
		return false
	}
	// Authority order matches the Channel log: a newer epoch supersedes every
	// term of an older epoch, and a newer term supersedes its old route fences.
	for _, pair := range [][2]uint64{{m.ChannelEpoch, expected.ExpectedChannelEpoch}, {m.LeaderTerm, expected.ExpectedLeaderEpoch}, {m.FenceVersion, expected.ExpectedRouteGeneration}} {
		if pair[0] < pair[1] {
			break
		}
		if pair[0] > pair[1] {
			return false
		}
	}
	prefix := p.Prefix()
	if prefix == q.Copy.After {
		return true
	}
	// An otherwise idle log must not grow a chain of anchors about anchors.
	return prefix == q.Copy.Before && prefix.Through+1 == m.LastOffset && m.LastOffset == q.Copy.After.Through
}

func (g *ServiceGateway) handleForwardMQTTReplayAnchor(ctx context.Context, q mqttAnchorForwardRequest) (ch.MQTTReplayAnchorProof, error) {
	s, err := g.service()
	if err != nil {
		return ch.MQTTReplayAnchorProof{}, err
	}
	return s.handleForwardMQTTReplayAnchor(ctx, q)
}

var _ ch.MQTTReplayAnchorCommitter = (*Service)(nil)
