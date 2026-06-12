package cluster

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/channelappend"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/propose"
)

func mapAppendError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case appendErrorMatches(err, channelv2.ErrNotLeader), appendErrorMatches(err, propose.ErrNotLeader), errors.Is(err, clusterv2.ErrNotLeader):
		return fmt.Errorf("%w: %w", channelappend.ErrNotLeader, err)
	case appendErrorMatches(err, channelv2.ErrStaleMeta), appendErrorMatches(err, channelv2.ErrNotReplica):
		return fmt.Errorf("%w: %w", channelappend.ErrStaleRoute, err)
	case appendErrorMatches(err, channelv2.ErrChannelNotFound):
		return fmt.Errorf("%w: %w", channelappend.ErrChannelNotFound, err)
	case appendErrorMatches(err, channelv2.ErrBackpressured):
		return fmt.Errorf("%w: %w", channelappend.ErrBackpressured, err)
	case errors.Is(err, clusterv2.ErrRouteNotReady), errors.Is(err, clusterv2.ErrNoSlotLeader), appendErrorMatches(err, channelv2.ErrNotReady):
		return fmt.Errorf("%w: %w", channelappend.ErrRouteNotReady, err)
	default:
		return fmt.Errorf("%w: %w", channelappend.ErrAppendFailed, err)
	}
}

func appendErrorMatches(err error, sentinel error) bool {
	return errors.Is(err, sentinel) || (err != nil && sentinel != nil && strings.Contains(err.Error(), sentinel.Error()))
}
