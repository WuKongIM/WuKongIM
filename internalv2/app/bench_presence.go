package app

import (
	"context"

	accessapi "github.com/WuKongIM/WuKongIM/internalv2/access/api"
	clusterinfra "github.com/WuKongIM/WuKongIM/internalv2/infra/cluster"
	"github.com/WuKongIM/WuKongIM/internalv2/runtime/online"
	authoritypresence "github.com/WuKongIM/WuKongIM/internalv2/runtime/presence"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
)

type presenceBenchController struct {
	nodeID    uint64
	local     *online.Registry
	authority *authoritypresence.Directory
}

func (a *App) benchPresenceController() accessapi.PresenceBenchController {
	if a == nil || a.online == nil || a.presenceDirectory == nil {
		return nil
	}
	nodeID := uint64(0)
	if node, ok := a.cluster.(clusterinfra.PresenceNode); ok {
		nodeID = node.NodeID()
	}
	return presenceBenchController{
		nodeID:    nodeID,
		local:     a.online,
		authority: a.presenceDirectory,
	}
}

func (c presenceBenchController) Snapshot(context.Context) (model.PresenceSnapshot, error) {
	local := c.local.Snapshot()
	authority := c.authority.Snapshot()
	return model.PresenceSnapshot{
		Version:                   "bench/v1",
		NodeID:                    c.nodeID,
		OwnerRoutesActive:         local.Active,
		OwnerRoutesPending:        local.Pending,
		OwnerTouchedDirty:         local.TouchedDirty,
		AuthorityRoutesActive:     authority.Active,
		AuthorityRoutesByHashSlot: authority.ByHashSlot,
		TouchRoutesTotal:          authority.TouchRoutesTotal,
		ExpiredRoutesTotal:        authority.ExpiredRoutesTotal,
	}, nil
}
