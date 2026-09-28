package channels

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// resolvePreparedAppendMeta cannot create a missing runtime or authorize from a
// cache. The serving node repeats this read; the sequencer enforces the same
// caller-supplied authority after mailbox admission and again at flush.
func (s *Service) resolvePreparedAppendMeta(ctx context.Context, id ch.ChannelID, epoch, leaderEpoch, route uint64, mode ch.CommitMode) (ch.Meta, error) {
	if err := ctx.Err(); err != nil {
		return ch.Meta{}, err
	}
	if s == nil || epoch == 0 || leaderEpoch == 0 || route == 0 ||
		(mode != 0 && mode != ch.CommitModeQuorum) {
		return ch.Meta{}, ch.ErrInvalidConfig
	}
	reader, ok := s.metaSource.(FreshChannelMetaSource)
	if !ok {
		return ch.Meta{}, ch.ErrInvalidConfig
	}
	meta, err := reader.ResolveChannelMetaFresh(ctx, id)
	if err != nil {
		return ch.Meta{}, err
	}
	if err = ctx.Err(); err != nil {
		return ch.Meta{}, err
	}
	if err = validateMQTTChannelAuthority(id, epoch, leaderEpoch, route, meta); err != nil {
		return ch.Meta{}, err
	}
	return meta, nil
}

// applyAppendMeta keeps ordinary append behavior while making explicit prepared
// requests use bounded context-aware activation. A newer cache floor can fence
// the old request but cannot change the authority carried by that request.
func (s *Service) applyAppendMeta(ctx context.Context, meta ch.Meta, route uint64) error {
	if route == 0 {
		return s.applyRuntimeMeta(meta, false)
	}
	if err := s.applyRequestMetaContext(ctx, meta); err != nil {
		return err
	}
	return ctx.Err()
}
