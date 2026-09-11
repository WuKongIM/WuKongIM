package migration

import (
	"context"
	"errors"
)

// SearchCheckpointView streams the original search program's startup rebuild
// seeds. Verification must run offline, before the plugin advances any cursor.
type SearchCheckpointView interface {
	WalkSearchCheckpoints(context.Context, func(ChannelIdentity, uint64) error) error
}

// VerifySearchCheckpoints rederives the full retained-channel set from this
// independently prepared workspace. Every target gets the full set so a later
// Channel leadership change cannot hide history on a different plugin node.
func VerifySearchCheckpoints(ctx context.Context, w Workspace, view SearchCheckpointView) error {
	var expected, actual uint64
	if err := WalkTargetChannels(ctx, w, func(c TargetChannel) error {
		if c.Count > 0 {
			expected++
		}
		return nil
	}); err != nil {
		return err
	}
	err := view.WalkSearchCheckpoints(ctx, func(id ChannelIdentity, seq uint64) error {
		if seq != 0 {
			return errors.New("search index checkpoint advanced before offline verification")
		}
		data, found, err := w.Get(ctx, []byte("target/channels/"+channelTuple(id)))
		if err != nil {
			return err
		}
		var c TargetChannel
		if !found || UnmarshalState(data, &c) != nil || c.Channel != id || c.Count == 0 {
			return errors.New("search checkpoint does not match retained target history")
		}
		actual++
		return nil
	})
	if err != nil {
		return err
	}
	if actual != expected {
		return errors.New("search rebuild checkpoint set is incomplete")
	}
	return nil
}
