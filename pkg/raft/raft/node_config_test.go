package raft

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"github.com/stretchr/testify/assert"
)

func TestSwitchConfig_LowerVersion_Rejected(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 2)
	n.cfg.Version = 5
	err := n.switchConfig(types.Config{Version: 3, Replicas: []uint64{1, 2}})
	assert.Error(t, err)
}

func TestSwitchConfig_HigherVersion_Accepted(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 2)
	n.cfg.Version = 1
	err := n.switchConfig(types.Config{
		Version:  2,
		Term:     3,
		Replicas: []uint64{1, 2, 3, 4},
		Role:     types.RoleFollower,
		Leader:   2,
	})
	assert.NoError(t, err)
	assert.Equal(t, 4, len(n.cfg.Replicas))
}

func TestSwitchConfig_LowerTerm_KeepsCurrentTerm(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 5, 2)
	err := n.switchConfig(types.Config{
		Version:  1,
		Term:     3, // lower than current term 5
		Replicas: []uint64{1, 2, 3},
		Role:     types.RoleFollower,
		Leader:   2,
	})
	assert.NoError(t, err)
	assert.Equal(t, uint32(5), n.cfg.Term)
}

func TestSwitchConfig_ZeroTerm_KeepsCurrentTerm(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 5, 2)
	err := n.switchConfig(types.Config{
		Version:  1,
		Term:     0,
		Replicas: []uint64{1, 2, 3},
		Role:     types.RoleFollower,
		Leader:   2,
	})
	assert.NoError(t, err)
	assert.Equal(t, uint32(5), n.cfg.Term)
}

func TestRoleChangeIfNeed_BothUnknown_OnlySelf_BecomeLeader(t *testing.T) {
	n := newTestNode(1, []uint64{})
	oldCfg := types.Config{Role: types.RoleUnknown}
	newCfg := types.Config{
		Role:     types.RoleUnknown,
		Replicas: []uint64{1},
		Term:     1,
	}
	n.roleChangeIfNeed(oldCfg, newCfg)
	assert.Equal(t, types.RoleLeader, n.cfg.Role)
}

func TestRoleChangeIfNeed_BothUnknown_MultiNode_LeaderSelf_BecomeLeader(t *testing.T) {
	n := newTestNode(1, []uint64{})
	oldCfg := types.Config{Role: types.RoleUnknown}
	newCfg := types.Config{
		Role:     types.RoleUnknown,
		Replicas: []uint64{1, 2, 3},
		Leader:   1,
		Term:     1,
	}
	n.roleChangeIfNeed(oldCfg, newCfg)
	assert.Equal(t, types.RoleLeader, n.cfg.Role)
}

func TestRoleChangeIfNeed_BothUnknown_MultiNode_NotLeader_BecomeFollower(t *testing.T) {
	n := newTestNode(1, []uint64{})
	oldCfg := types.Config{Role: types.RoleUnknown}
	newCfg := types.Config{
		Role:     types.RoleUnknown,
		Replicas: []uint64{1, 2, 3},
		Leader:   2,
		Term:     1,
	}
	n.roleChangeIfNeed(oldCfg, newCfg)
	assert.Equal(t, types.RoleFollower, n.cfg.Role)
}

func TestRoleChangeIfNeed_BothUnknown_Learner(t *testing.T) {
	n := newTestNode(1, []uint64{})
	oldCfg := types.Config{Role: types.RoleUnknown}
	newCfg := types.Config{
		Role:     types.RoleUnknown,
		Replicas: []uint64{2, 3},
		Learners: []uint64{1},
		Leader:   2,
		Term:     1,
	}
	n.roleChangeIfNeed(oldCfg, newCfg)
	assert.Equal(t, types.RoleLearner, n.cfg.Role)
}

func TestRoleChangeIfNeed_RoleChanged_FollowerToLeader(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 2)
	oldCfg := types.Config{Role: types.RoleFollower, Leader: 2, Term: 3}
	newCfg := types.Config{Role: types.RoleFollower, Leader: 1, Term: 3}
	n.roleChangeIfNeed(oldCfg, newCfg)
	assert.Equal(t, types.RoleLeader, n.cfg.Role)
}

func TestRoleChangeIfNeed_RoleChanged_LeaderToFollower(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeLeader(n, 3)
	oldCfg := types.Config{Role: types.RoleLeader, Leader: 1, Term: 3}
	newCfg := types.Config{Role: types.RoleFollower, Leader: 2, Term: 4}
	n.roleChangeIfNeed(oldCfg, newCfg)
	assert.Equal(t, types.RoleFollower, n.cfg.Role)
}

func TestRoleChangeIfNeed_TermChanged(t *testing.T) {
	n := newTestNode(1, []uint64{1, 2, 3})
	makeFollower(n, 3, 2)
	oldCfg := types.Config{Role: types.RoleFollower, Leader: 2, Term: 3}
	newCfg := types.Config{Role: types.RoleFollower, Leader: 2, Term: 5}
	n.roleChangeIfNeed(oldCfg, newCfg)
	assert.Equal(t, uint32(5), n.cfg.Term)
}
