package channels

import (
	"context"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

// mqttRetirementForwardRequest sends immutable selection and authority identity;
// only fresh Slot metadata may populate the actual reactor request.
type mqttRetirementForwardRequest struct {
	Leader            ch.NodeID
	Authority         [32]byte
	Selection         ch.MQTTReplayRetirementSelectionRequest
	Candidate         ch.MQTTReplayAnchorProof
	MessageID         uint64
	ServerTimestampMS int64
}

func mqttRetirementForward(q ch.MQTTReplayRetirementRequest) mqttRetirementForwardRequest {
	m := q.Meta
	return mqttRetirementForwardRequest{Leader: m.Leader, Authority: ch.MQTTReplayCopyAuthority(m), Selection: ch.MQTTReplayRetirementSelectionRequest{
		Source: ch.MQTTReplayPlanRequest{ChannelID: m.ID, ExpectedChannelEpoch: m.Epoch, ExpectedLeaderEpoch: m.LeaderEpoch, ExpectedRouteGeneration: m.RouteGeneration, Generation: q.Captured.Prefix().Generation}, Captured: q.Captured, Through: q.ConsumerThrough, Limit: 1}, Candidate: q.Candidate, MessageID: q.MessageID, ServerTimestampMS: q.ServerTimestampMS}
}

func (q mqttRetirementForwardRequest) valid() bool {
	return q.Leader != 0 && q.Authority != [32]byte{} && q.MessageID != 0 && q.ServerTimestampMS > 0 && q.Selection.BeforeAnchor == 0 && q.Selection.Limit == 1 && q.Selection.Accepts(ch.MQTTReplayRetirementSelection{Captured: q.Selection.Captured, Candidate: q.Candidate, HasCandidate: true, Done: true})
}

func (q mqttRetirementForwardRequest) request(m ch.Meta) ch.MQTTReplayRetirementRequest {
	return ch.MQTTReplayRetirementRequest{Meta: m, Captured: q.Selection.Captured, Candidate: q.Candidate, ConsumerThrough: q.Selection.Through, MessageID: q.MessageID, ServerTimestampMS: q.ServerTimestampMS}
}

// accepts verifies the committed result's association without trusting caller
// metadata. Durability comes only from the owning reactor/native journal.
func (q mqttRetirementForwardRequest) accepts(p ch.MQTTReplayRetirementProof) bool {
	if !q.valid() || !p.Retirement.Valid() || !p.Manifest.StructurallyValid() || p.Manifest.Version != quorumlog.MQTTReplayRetirementProposalManifestVersion || p.Retirement.AnchorPosition >= p.Manifest.LastOffset {
		return false
	}
	a, b := p.Retirement.Anchor, q.Candidate.Anchor
	want := quorumlog.MQTTReplayRetirement{Anchor: b, AnchorPosition: q.Candidate.Manifest.LastOffset, AnchorDigest: q.Candidate.Manifest.Digest}
	if a.SourceCommand != b.SourceCommand || a.StartAfter != b.StartAfter || a.Through < b.Through || a.TotalBytes < b.TotalBytes || a.TotalStoredBytes < b.TotalStoredBytes || (a.Through == b.Through && p.Retirement != want) || (a.Through > b.Through && a.TotalStoredBytes <= b.TotalStoredBytes) {
		return false
	}
	e := q.Selection.Source
	for _, pair := range [][2]uint64{{p.Manifest.ChannelEpoch, e.ExpectedChannelEpoch}, {p.Manifest.LeaderTerm, e.ExpectedLeaderEpoch}, {p.Manifest.FenceVersion, e.ExpectedRouteGeneration}} {
		if pair[0] < pair[1] {
			return true
		}
		if pair[0] > pair[1] {
			return false
		}
	}
	return true
}

type mqttRetirementForwarder interface {
	ForwardMQTTReplayRetirement(context.Context, ch.NodeID, mqttRetirementForwardRequest) (ch.MQTTReplayRetirementProof, error)
}

// CommitMQTTReplayRetirement binds trusted consumer permission to fresh full
// placement and the reactor queue. Losing authority withholds even a durable reply.
func (s *Service) CommitMQTTReplayRetirement(ctx context.Context, q ch.MQTTReplayRetirementRequest) (ch.MQTTReplayRetirementProof, error) {
	if !q.Valid() {
		return ch.MQTTReplayRetirementProof{}, ch.ErrInvalidConfig
	}
	if q.Meta.WriteFence.Set() {
		return ch.MQTTReplayRetirementProof{}, ch.ErrWriteFenced
	}
	return s.commitMQTTReplayRetirement(ctx, mqttRetirementForward(q), 0)
}

func (s *Service) handleForwardMQTTReplayRetirement(ctx context.Context, q mqttRetirementForwardRequest) (ch.MQTTReplayRetirementProof, error) {
	if s == nil || q.Leader == 0 || q.Leader != s.localNode {
		return ch.MQTTReplayRetirementProof{}, ch.ErrNotLeader
	}
	return s.commitMQTTReplayRetirement(ctx, q, s.localNode)
}

func (s *Service) mqttRetirementMeta(ctx context.Context, q mqttRetirementForwardRequest) (ch.Meta, error) {
	p := q.Selection.Source
	m, err := s.mqttCopyMeta(ctx, ch.MQTTReplayRequest{ChannelID: p.ChannelID, ExpectedChannelEpoch: p.ExpectedChannelEpoch, ExpectedLeaderEpoch: p.ExpectedLeaderEpoch, ExpectedRouteGeneration: p.ExpectedRouteGeneration})
	if err != nil {
		return ch.Meta{}, err
	}
	if m.Leader != q.Leader || ch.MQTTReplayCopyAuthority(m) != q.Authority {
		return ch.Meta{}, ch.ErrStaleMeta
	}
	if !q.request(m).Valid() {
		return ch.Meta{}, ch.ErrInvalidConfig
	}
	return m, nil
}

func (s *Service) commitMQTTReplayRetirement(ctx context.Context, q mqttRetirementForwardRequest, serving ch.NodeID) (ch.MQTTReplayRetirementProof, error) {
	var empty ch.MQTTReplayRetirementProof
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
	m, err := s.mqttRetirementMeta(ctx, q)
	if err != nil {
		return empty, err
	}
	if serving != 0 && m.Leader != serving {
		return empty, ch.ErrNotLeader
	}
	var p ch.MQTTReplayRetirementProof
	if m.Leader == s.localNode {
		committer, ok := s.runtime.(ch.MQTTReplayRetirementCommitter)
		if !ok {
			return empty, ch.ErrInvalidConfig
		}
		if err = s.applyRuntimeMetaContext(ctx, m, true, true); err != nil {
			return empty, err
		}
		p, err = committer.CommitMQTTReplayRetirement(ctx, q.request(m))
	} else if f, ok := s.forward.(mqttRetirementForwarder); ok {
		p, err = f.ForwardMQTTReplayRetirement(ctx, m.Leader, q)
	} else {
		return empty, ch.ErrInvalidConfig
	}
	if err != nil {
		return empty, err
	}
	if err = ctx.Err(); err != nil {
		return empty, err
	}
	if _, err = s.mqttRetirementMeta(ctx, q); err != nil {
		return empty, err
	}
	if !q.accepts(p) {
		return empty, ch.ErrLogConflict
	}
	return p, nil
}

func (g *ServiceGateway) handleForwardMQTTReplayRetirement(ctx context.Context, q mqttRetirementForwardRequest) (ch.MQTTReplayRetirementProof, error) {
	s, err := g.service()
	if err != nil {
		return ch.MQTTReplayRetirementProof{}, err
	}
	return s.handleForwardMQTTReplayRetirement(ctx, q)
}

var _ ch.MQTTReplayRetirementCommitter = (*Service)(nil)
