package channels

import (
	"context"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

const willReceiptConcurrent = 4

type willReceiptForwardRequest struct {
	Leader  ch.NodeID
	Request ch.WillReceiptRequest
}
type willReceiptForwarder interface {
	ForwardWillReceipt(context.Context, ch.NodeID, ch.WillReceiptRequest) (ch.WillReceiptResult, error)
}

// ReadWillReceipt surrounds a recovered runtime read with fresh Slot authority.
// A successful empty observation still grants no authority to repeat a Will.
func (s *Service) ReadWillReceipt(ctx context.Context, q ch.WillReceiptRequest) (ch.WillReceiptResult, error) {
	return s.readWillReceipt(ctx, q, 0)
}
func (s *Service) handleForwardWillReceipt(ctx context.Context, q willReceiptForwardRequest) (ch.WillReceiptResult, error) {
	if s == nil || q.Leader == 0 || q.Leader != s.localNode {
		return ch.WillReceiptResult{}, ch.ErrNotLeader
	}
	return s.readWillReceipt(ctx, q.Request, q.Leader)
}
func (s *Service) readWillReceipt(ctx context.Context, q ch.WillReceiptRequest, serving ch.NodeID) (ch.WillReceiptResult, error) {
	var empty ch.WillReceiptResult
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if s == nil || !q.Valid() {
		return empty, ch.ErrInvalidConfig
	}
	select {
	case s.willReceiptReads <- struct{}{}:
		defer func() { <-s.willReceiptReads }()
	default:
		return empty, ch.ErrBackpressured
	}
	meta, err := s.willReceiptAuthority(ctx, q)
	if err != nil {
		return empty, err
	}
	if serving != 0 && serving != meta.Leader {
		return empty, ch.ErrNotLeader
	}
	var proof ch.WillReceiptResult
	if meta.Leader == s.localNode {
		reader, ok := s.runtime.(ch.WillReceiptReader)
		if !ok {
			return empty, ch.ErrInvalidConfig
		}
		if err = s.applyRuntimeMetaContext(ctx, meta, true, true); err != nil {
			return empty, err
		}
		proof, err = reader.ReadWillReceipt(ctx, q)
	} else {
		forward, ok := s.forward.(willReceiptForwarder)
		if !ok {
			return empty, ch.ErrInvalidConfig
		}
		proof, err = forward.ForwardWillReceipt(ctx, meta.Leader, q)
	}
	if err != nil {
		return empty, err
	}
	if err = ctx.Err(); err != nil {
		return empty, err
	}
	current, err := s.willReceiptAuthority(ctx, q)
	if err != nil {
		return empty, err
	}
	if mqttCopyAuthority(meta) != mqttCopyAuthority(current) || !sameWriteFence(meta.WriteFence, current.WriteFence) {
		return empty, ch.ErrStaleMeta
	}
	if err = ctx.Err(); err != nil {
		return empty, err
	}
	if !proof.Valid() {
		return empty, ch.ErrLogConflict
	}
	return proof, nil
}

// willReceiptAuthority excludes caches and requires exact, majority-capable
// placement; stable migration fences may block writes while permitting evidence.
func (s *Service) willReceiptAuthority(ctx context.Context, q ch.WillReceiptRequest) (ch.Meta, error) {
	reader, ok := s.metaSource.(FreshChannelMetaSource)
	if !ok {
		return ch.Meta{}, ch.ErrInvalidConfig
	}
	m, err := reader.ResolveChannelMetaFresh(ctx, q.ChannelID)
	if err != nil {
		return ch.Meta{}, err
	}
	if err = ctx.Err(); err != nil {
		return ch.Meta{}, err
	}
	if m.Status == ch.StatusDeleting || m.Status == ch.StatusDeleted {
		return ch.Meta{}, ch.ErrChannelNotFound
	}
	if len(m.Replicas) > 256 || len(m.ISR) > 256 || int(m.MinISR)*2 <= len(m.ISR) {
		return ch.Meta{}, ch.ErrNotReady
	}
	// This is immutable evidence, so a stable migration write fence is allowed.
	unfenced := m
	unfenced.WriteFence = ch.WriteFence{}
	if err = validateMQTTChannelAuthority(q.ChannelID, q.ExpectedChannelEpoch, q.ExpectedLeaderEpoch, q.ExpectedRouteGeneration, unfenced); err != nil {
		return ch.Meta{}, err
	}
	return m, nil
}
func (g *ServiceGateway) handleForwardWillReceipt(ctx context.Context, q willReceiptForwardRequest) (ch.WillReceiptResult, error) {
	s, err := g.service()
	if err != nil {
		return ch.WillReceiptResult{}, err
	}
	return s.handleForwardWillReceipt(ctx, q)
}

var _ ch.WillReceiptReader = (*Service)(nil)
