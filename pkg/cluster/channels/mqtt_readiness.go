package channels

import (
	"context"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
)

// attachMQTTReplayReadiness enriches an active migration probe only. Ordinary
// runtime diagnostics remain observational; absent capability stays explicit.
func (s *Service) attachMQTTReplayReadiness(parent context.Context, m ch.Meta, proof ch.RuntimeProbeChannel) (ch.RuntimeProbeChannel, error) {
	if parent == nil {
		parent = context.Background()
	}
	if err := parent.Err(); err != nil {
		return ch.RuntimeProbeChannel{}, err
	}
	proof.ReplayReadiness = nil
	capability, ok := s.store.(channelstore.MQTTReplayAnchorFactory)
	if !ok || !capability.SupportsMQTTReplayAnchors() {
		return proof, nil
	}
	var empty ch.RuntimeProbeChannel
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	fresh, ok := s.metaSource.(FreshChannelMetaSource)
	if !ok {
		return empty, ch.ErrInvalidConfig
	}
	recheck := func() error {
		current, err := fresh.ResolveChannelMetaFresh(ctx, m.ID)
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if ch.MQTTReplayCopyAuthority(current) != ch.MQTTReplayCopyAuthority(m) || current.WriteFence != m.WriteFence {
			return ch.ErrStaleMeta
		}
		return nil
	}
	if err := recheck(); err != nil {
		return empty, err
	}
	handle, err := s.store.ChannelStore(ch.ChannelKeyForID(m.ID), m.ID)
	if err != nil {
		return empty, err
	}
	defer handle.Close()
	reader, ok := handle.(channelstore.MQTTReplayReadinessReader)
	if !ok {
		return empty, ch.ErrInvalidConfig
	}
	ready, err := reader.ReadMQTTReplayReadiness(ctx, proof.HW)
	if err != nil {
		return empty, err
	}
	if err = ctx.Err(); err != nil {
		return empty, err
	}
	if !ready.ValidFor(proof.HW) {
		return empty, ch.ErrLogConflict
	}
	if err = recheck(); err != nil {
		return empty, err
	}
	after, err := s.RuntimeProbe(ctx, ch.RuntimeSelector{ChannelIDs: []ch.ChannelID{m.ID}})
	if err != nil {
		return empty, err
	}
	if err = ctx.Err(); err != nil {
		return empty, err
	}
	if len(after.Channels) != 1 {
		return empty, ch.ErrStaleMeta
	}
	current := after.Channels[0]
	if current.ChannelID != proof.ChannelID || current.Role != proof.Role || current.Status != proof.Status || current.ChannelEpoch != proof.ChannelEpoch || current.LeaderEpoch != proof.LeaderEpoch || current.WriteFence != proof.WriteFence || current.RecoveryRequired != proof.RecoveryRequired {
		return empty, ch.ErrStaleMeta
	}
	proof.ReplayReadiness = &ready
	return proof, nil
}
