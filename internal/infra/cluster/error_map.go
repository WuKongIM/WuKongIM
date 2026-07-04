package cluster

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
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
	case appendErrorMatches(err, channelruntime.ErrNotLeader), appendErrorMatches(err, propose.ErrNotLeader), errors.Is(err, cluster.ErrNotLeader):
		return fmt.Errorf("%w: %w", channelappend.ErrNotLeader, err)
	case appendErrorMatches(err, channelruntime.ErrStaleMeta), appendErrorMatches(err, channelruntime.ErrNotReplica):
		return fmt.Errorf("%w: %w", channelappend.ErrStaleRoute, err)
	case appendErrorMatches(err, channelruntime.ErrChannelNotFound):
		return fmt.Errorf("%w: %w", channelappend.ErrChannelNotFound, err)
	case appendErrorMatches(err, channelruntime.ErrBackpressured):
		return fmt.Errorf("%w: %w", channelappend.ErrBackpressured, err)
	case errors.Is(err, cluster.ErrRouteNotReady), errors.Is(err, cluster.ErrNoSlotLeader), appendErrorMatches(err, channelruntime.ErrNotReady), appendErrorMatches(err, channelruntime.ErrWriteFenced), appendErrorIsChannelPlacementUnavailable(err):
		return fmt.Errorf("%w: %w", channelappend.ErrRouteNotReady, err)
	default:
		return fmt.Errorf("%w: %w", channelappend.ErrAppendFailed, err)
	}
}

func appendErrorMatches(err error, sentinel error) bool {
	return channelruntime.ErrorMatches(err, sentinel)
}

func appendErrorIsChannelPlacementUnavailable(err error) bool {
	if !appendErrorMatches(err, channelruntime.ErrInvalidConfig) {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "channel replica candidates") && strings.Contains(msg, "below replica count")
}
