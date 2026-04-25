package replica

import "github.com/WuKongIM/WuKongIM/pkg/channel"

func (r *replica) applyMetaLocked(meta channel.Meta) error {
	normalized, err := normalizeMeta(meta)
	if err != nil {
		return err
	}
	if err := r.validateMetaLocked(normalized); err != nil {
		return err
	}
	r.commitMetaLocked(normalized)
	return nil
}

func (r *replica) validateMetaLocked(normalized channel.Meta) error {
	switch {
	case r.state.ChannelKey != "" && normalized.Key != r.state.ChannelKey:
		return channel.ErrInvalidMeta
	case normalized.Epoch < r.state.Epoch:
		return channel.ErrStaleMeta
	case normalized.Epoch == r.state.Epoch && normalized.LeaderEpoch < r.meta.LeaderEpoch:
		return channel.ErrStaleMeta
	case normalized.Epoch == r.state.Epoch &&
		normalized.LeaderEpoch == r.meta.LeaderEpoch &&
		r.state.Leader != 0 &&
		normalized.Leader != r.state.Leader:
		return channel.ErrStaleMeta
	}
	return nil
}

func (r *replica) commitMetaLocked(normalized channel.Meta) {
	r.meta = normalized
	r.state.ChannelKey = normalized.Key
	r.state.Epoch = normalized.Epoch
	r.state.Leader = normalized.Leader
}

func normalizeMeta(meta channel.Meta) (channel.Meta, error) {
	meta.Replicas = dedupeNodeIDs(meta.Replicas)
	meta.ISR = dedupeNodeIDs(meta.ISR)

	if meta.Key == "" {
		return channel.Meta{}, channel.ErrInvalidMeta
	}
	if len(meta.Replicas) == 0 {
		return channel.Meta{}, channel.ErrInvalidMeta
	}
	if meta.Leader == 0 {
		return channel.Meta{}, channel.ErrInvalidMeta
	}
	if meta.MinISR < 1 || meta.MinISR > len(meta.Replicas) {
		return channel.Meta{}, channel.ErrInvalidMeta
	}
	if !containsNode(meta.Replicas, meta.Leader) {
		return channel.Meta{}, channel.ErrInvalidMeta
	}
	if !containsNode(meta.ISR, meta.Leader) {
		return channel.Meta{}, channel.ErrInvalidMeta
	}
	for _, id := range meta.ISR {
		if !containsNode(meta.Replicas, id) {
			return channel.Meta{}, channel.ErrInvalidMeta
		}
	}

	return meta, nil
}

func dedupeNodeIDs(ids []channel.NodeID) []channel.NodeID {
	if len(ids) == 0 {
		return nil
	}

	out := make([]channel.NodeID, 0, len(ids))
	seen := make(map[channel.NodeID]struct{}, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func containsNode(ids []channel.NodeID, target channel.NodeID) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}
