package cluster

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/channelappend"
	channelv2 "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/propose"
)

func mapAppendError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case appendErrorMatches(err, channelv2.ErrNotLeader), appendErrorMatches(err, propose.ErrNotLeader), errors.Is(err, cluster.ErrNotLeader):
		return fmt.Errorf("%w: %w", channelappend.ErrNotLeader, err)
	case appendErrorMatches(err, channelv2.ErrStaleMeta), appendErrorMatches(err, channelv2.ErrNotReplica):
		return fmt.Errorf("%w: %w", channelappend.ErrStaleRoute, err)
	case appendErrorMatches(err, channelv2.ErrChannelNotFound):
		return fmt.Errorf("%w: %w", channelappend.ErrChannelNotFound, err)
	case appendErrorMatches(err, channelv2.ErrBackpressured):
		return fmt.Errorf("%w: %w", channelappend.ErrBackpressured, err)
	case errors.Is(err, cluster.ErrRouteNotReady), errors.Is(err, cluster.ErrNoSlotLeader), appendErrorMatches(err, channelv2.ErrNotReady), appendErrorMatches(err, channelv2.ErrWriteFenced), appendErrorIsChannelPlacementUnavailable(err):
		return fmt.Errorf("%w: %w", channelappend.ErrRouteNotReady, err)
	default:
		return fmt.Errorf("%w: %w", channelappend.ErrAppendFailed, err)
	}
}

func appendErrorMatches(err error, sentinel error) bool {
	return errors.Is(err, sentinel) || (err != nil && sentinel != nil && strings.Contains(err.Error(), sentinel.Error()))
}

func appendErrorIsChannelPlacementUnavailable(err error) bool {
	if !appendErrorMatches(err, channelv2.ErrInvalidConfig) {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "channel replica candidates") && strings.Contains(msg, "below replica count")
}
