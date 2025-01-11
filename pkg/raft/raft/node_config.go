package raft

import (
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"go.uber.org/zap"
)

func (n *Node) switchConfig(newCfg types.Config) error {
	oldCfg := n.cfg

	if newCfg.Term < oldCfg.Term {
		n.Error("term is lower than current term", zap.Uint32("newTerm", newCfg.Term), zap.Uint32("currentTerm", oldCfg.Term))
		return errors.New("term is lower than current term")
	}

	n.votes = make(map[uint64]bool)
	n.replicaSync = make(map[uint64]*SyncInfo)
	n.resetRandomizedElectionTimeout()

	// 比较角色是否发生变化
	n.roleChangeIfNeed(oldCfg, newCfg)

	n.cfg = newCfg

	return nil
}

func (n *Node) roleChangeIfNeed(oldCfg, newCfg types.Config) {
	if oldCfg.Role == types.RoleUnknown && newCfg.Role == types.RoleUnknown && len(newCfg.Replicas) > 0 {
		onlySelf := false
		if len(newCfg.Replicas) == 1 {
			if newCfg.Replicas[0] == n.opts.NodeId {
				onlySelf = true
			}
		}
		if onlySelf {
			if newCfg.Term == 0 {
				newCfg.Term = 1
			}
			n.BecomeLeader(newCfg.Term)
		} else {
			if len(newCfg.Replicas) > 0 {
				if newCfg.Leader != 0 && newCfg.Leader == n.opts.NodeId {
					n.BecomeLeader(newCfg.Term)
				} else {
					n.BecomeFollower(newCfg.Term, newCfg.Leader)
				}
			}
		}
		return
	}

	if oldCfg.Role != newCfg.Role || oldCfg.Leader != newCfg.Leader || oldCfg.Term != newCfg.Term {
		n.Info("role change", zap.String("old", oldCfg.Role.String()), zap.String("new", newCfg.Role.String()))

		role := newCfg.Role
		if newCfg.Role == types.RoleUnknown {
			role = oldCfg.Role
		}
		switch role {
		case types.RoleLeader:
			n.BecomeLeader(newCfg.Term)
		case types.RoleFollower:
			n.BecomeFollower(newCfg.Term, newCfg.Leader)
		case types.RoleLearner:
			n.BecomeLearner(newCfg.Term, newCfg.Leader)
		}
	}
}
