package app

import (
	"context"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

type presenceAuthorityClient struct {
	cluster     raftcluster.API
	local       *presence.App
	remote      *accessnode.Client
	localNodeID uint64
}

func (c *presenceAuthorityClient) RegisterAuthoritative(ctx context.Context, cmd presence.RegisterAuthoritativeCommand) (presence.RegisterAuthoritativeResult, error) {
	if c.shouldUseLocalLeader(cmd.SlotID) {
		return c.local.RegisterAuthoritative(ctx, cmd)
	}
	return c.remote.RegisterAuthoritative(ctx, cmd)
}

func (c *presenceAuthorityClient) UnregisterAuthoritative(ctx context.Context, cmd presence.UnregisterAuthoritativeCommand) error {
	if c.shouldUseLocalLeader(cmd.SlotID) {
		return c.local.UnregisterAuthoritative(ctx, cmd)
	}
	return c.remote.UnregisterAuthoritative(ctx, cmd)
}

func (c *presenceAuthorityClient) HeartbeatAuthoritative(ctx context.Context, cmd presence.HeartbeatAuthoritativeCommand) (presence.HeartbeatAuthoritativeResult, error) {
	if c.shouldUseLocalLeader(cmd.Lease.SlotID) {
		return c.local.HeartbeatAuthoritative(ctx, cmd)
	}
	return c.remote.HeartbeatAuthoritative(ctx, cmd)
}

func (c *presenceAuthorityClient) ReplayAuthoritative(ctx context.Context, cmd presence.ReplayAuthoritativeCommand) error {
	if c.shouldUseLocalLeader(cmd.Lease.SlotID) {
		return c.local.ReplayAuthoritative(ctx, cmd)
	}
	return c.remote.ReplayAuthoritative(ctx, cmd)
}

func (c *presenceAuthorityClient) EndpointsByUID(ctx context.Context, uid string) ([]presence.Route, error) {
	if c.cluster == nil || c.remote == nil {
		if c.local == nil {
			return nil, nil
		}
		return c.local.EndpointsByUID(ctx, uid)
	}
	slotID := c.cluster.SlotForKey(uid)
	if leaderID, err := c.cluster.LeaderOf(slotID); err == nil && c.cluster.IsLocal(leaderID) {
		return c.local.EndpointsByUID(ctx, uid)
	}
	return c.remote.EndpointsByUID(ctx, uid)
}

func (c *presenceAuthorityClient) EndpointsByUIDs(ctx context.Context, uids []string) (map[string][]presence.Route, error) {
	if c.cluster == nil || c.remote == nil {
		if c.local == nil {
			return nil, nil
		}
		return c.local.EndpointsByUIDs(ctx, uids)
	}

	grouped := make(map[uint64][]string)
	for _, uid := range uids {
		slotID := uint64(c.cluster.SlotForKey(uid))
		grouped[slotID] = append(grouped[slotID], uid)
	}

	out := make(map[string][]presence.Route, len(uids))
	for slotID, groupUIDs := range grouped {
		if leaderID, err := c.cluster.LeaderOf(multiraft.SlotID(slotID)); err == nil && c.cluster.IsLocal(leaderID) {
			routes, err := c.local.EndpointsByUIDs(ctx, groupUIDs)
			if err != nil {
				return nil, err
			}
			for uid, current := range routes {
				out[uid] = append([]presence.Route(nil), current...)
			}
			continue
		}

		routes, err := c.remote.EndpointsByUIDs(ctx, groupUIDs)
		if err != nil {
			return nil, err
		}
		for uid, current := range routes {
			out[uid] = append([]presence.Route(nil), current...)
		}
	}
	return out, nil
}

func (c *presenceAuthorityClient) ApplyRouteAction(ctx context.Context, action presence.RouteAction) error {
	if c.local != nil && (action.NodeID == 0 || action.NodeID == c.localNodeID) {
		return c.local.ApplyRouteAction(ctx, action)
	}
	return c.remote.ApplyRouteAction(ctx, action)
}

func (c *presenceAuthorityClient) shouldUseLocalLeader(slotID uint64) bool {
	if c.local == nil {
		return false
	}
	if c.cluster == nil {
		return true
	}
	leaderID, err := c.cluster.LeaderOf(multiraft.SlotID(slotID))
	return err == nil && c.cluster.IsLocal(leaderID)
}
