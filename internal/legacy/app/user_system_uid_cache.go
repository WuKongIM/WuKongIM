package app

import (
	"context"
	"fmt"
	"sort"

	userusecase "github.com/WuKongIM/WuKongIM/internal/legacy/usecase/user"
)

type systemUIDCacheRemote interface {
	AddSystemUIDsToCache(ctx context.Context, nodeID uint64, uids []string) error
	RemoveSystemUIDsFromCache(ctx context.Context, nodeID uint64, uids []string) error
}

// clusterUserUsecase keeps persisted system UID changes in every node-local cache.
type clusterUserUsecase struct {
	local       *userusecase.App
	remote      systemUIDCacheRemote
	localNodeID uint64
	peerNodeIDs []uint64
}

func (u *clusterUserUsecase) UpdateToken(ctx context.Context, cmd userusecase.UpdateTokenCommand) error {
	return u.local.UpdateToken(ctx, cmd)
}

func (u *clusterUserUsecase) DeviceQuit(ctx context.Context, cmd userusecase.DeviceQuitCommand) error {
	return u.local.DeviceQuit(ctx, cmd)
}

func (u *clusterUserUsecase) OnlineStatus(ctx context.Context, uids []string) ([]userusecase.OnlineStatus, error) {
	return u.local.OnlineStatus(ctx, uids)
}

func (u *clusterUserUsecase) AddSystemUIDs(ctx context.Context, uids []string) error {
	if err := u.local.AddSystemUIDs(ctx, uids); err != nil {
		return err
	}
	return u.broadcastSystemUIDCache(ctx, uids, true)
}

func (u *clusterUserUsecase) RemoveSystemUIDs(ctx context.Context, uids []string) error {
	if err := u.local.RemoveSystemUIDs(ctx, uids); err != nil {
		return err
	}
	return u.broadcastSystemUIDCache(ctx, uids, false)
}

func (u *clusterUserUsecase) ListSystemUIDs(ctx context.Context) ([]string, error) {
	return u.local.ListSystemUIDs(ctx)
}

func (u *clusterUserUsecase) AddSystemUIDsToCache(uids []string) error {
	return u.local.AddSystemUIDsToCache(uids)
}

func (u *clusterUserUsecase) RemoveSystemUIDsFromCache(uids []string) error {
	return u.local.RemoveSystemUIDsFromCache(uids)
}

func (u *clusterUserUsecase) broadcastSystemUIDCache(ctx context.Context, uids []string, add bool) error {
	if len(uids) == 0 || u == nil || u.remote == nil {
		return nil
	}
	for _, nodeID := range uniqueRemoteSystemUIDCacheNodes(u.localNodeID, u.peerNodeIDs) {
		var err error
		if add {
			err = u.remote.AddSystemUIDsToCache(ctx, nodeID, uids)
		} else {
			err = u.remote.RemoveSystemUIDsFromCache(ctx, nodeID, uids)
		}
		if err != nil {
			return fmt.Errorf("sync system uid cache to node %d: %w", nodeID, err)
		}
	}
	return nil
}

func uniqueRemoteSystemUIDCacheNodes(localNodeID uint64, nodeIDs []uint64) []uint64 {
	seen := make(map[uint64]struct{}, len(nodeIDs))
	out := make([]uint64, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		if nodeID == 0 || nodeID == localNodeID {
			continue
		}
		if _, ok := seen[nodeID]; ok {
			continue
		}
		seen[nodeID] = struct{}{}
		out = append(out, nodeID)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
