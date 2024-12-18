package reactor

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/reactor"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/stretchr/testify/assert"
)

func hasAction(t testing.TB, actionType reactor.UserActionType, actions []reactor.UserAction) {
	exist := false
	for _, action := range actions {
		if action.Type == actionType {
			exist = true
			break
		}
	}
	assert.True(t, exist)
}

func getAction(t testing.TB, actionType reactor.UserActionType, actions []reactor.UserAction) reactor.UserAction {
	for _, action := range actions {
		if action.Type == actionType {
			return action
		}
	}
	return reactor.UserAction{}
}

func TestUser(t *testing.T) {

	options = NewOptions()
	options.NodeId = 1
	options.NodeHeartbeatTick = 1
	options.OutboundForwardIntervalTick = 1
	options.NodeHeartbeatTimeoutTick = 2
	options.LeaderIdleTimeoutTick = 1
	options.NodeVersion = func() uint64 {
		return 1
	}

	u := NewUser("no", "uid")

	becomeLeader := func() {
		u.step(reactor.UserAction{
			Type: reactor.UserActionConfigUpdate,
			Cfg:  reactor.UserConfig{LeaderId: 1},
		})
		assert.True(t, u.isLeader())
	}

	becomeReplica := func() {
		u.step(reactor.UserAction{
			Type: reactor.UserActionConfigUpdate,
			Cfg:  reactor.UserConfig{LeaderId: 2},
		})
		assert.True(t, !u.isLeader())
	}

	t.Run("testElection", func(t *testing.T) {
		u.tick()
		actions := u.ready()
		hasAction(t, reactor.UserActionElection, actions)
	})
	t.Run("testBecomeLeader", func(t *testing.T) {
		becomeLeader()
	})

	t.Run("testJoin", func(t *testing.T) {
		becomeLeader()
		u.step(reactor.UserAction{
			Type: reactor.UserActionJoin,
			From: 2,
		})

		actions := u.ready()
		hasAction(t, reactor.UserActionJoinResp, actions)
	})

	t.Run("testAuth", func(t *testing.T) {
		becomeLeader()
		u.step(reactor.UserAction{
			Type: reactor.UserActionInboundAdd,
			Messages: []*reactor.UserMessage{
				&reactor.UserMessage{
					Conn: &reactor.Conn{
						FromNode: 1,
						ConnId:   1,
					},
					Frame: wkproto.ConnectPacket{},
				},
			},
		})
		actions := u.ready()
		hasAction(t, reactor.UserActionInbound, actions)

		action := getAction(t, reactor.UserActionInbound, actions)

		assert.Equal(t, 1, len(action.Messages))

		u.conns.updateConnAuth(1, 1, true)

		conn := u.conns.connByConnId(1, 1)
		assert.Equal(t, true, conn.Auth)

		// only test
		u.conns.conns = nil
	})

	t.Run("testInboundAdd", func(t *testing.T) {
		becomeLeader()

		u.step(reactor.UserAction{
			Type: reactor.UserActionInboundAdd,
			Messages: []*reactor.UserMessage{
				&reactor.UserMessage{},
			},
		})
		actions := u.ready()
		hasAction(t, reactor.UserActionInbound, actions)
		assert.Equal(t, 0, u.inbound.queue.len())

	})

	t.Run("testOutbound", func(t *testing.T) {

		becomeLeader()

		u.step(reactor.UserAction{
			Type: reactor.UserActionJoin,
			From: 2,
		})

		u.step(reactor.UserAction{
			Type: reactor.UserActionOutboundAdd,
			Messages: []*reactor.UserMessage{
				&reactor.UserMessage{},
				&reactor.UserMessage{},
			},
		})

		u.tick()

		actions := u.ready()
		hasAction(t, reactor.UserActionOutboundForward, actions)
		assert.Equal(t, 0, u.outbound.queue.len())

	})

	t.Run("testConnClose", func(t *testing.T) {
		becomeReplica()
		u.step(reactor.UserAction{
			Type: reactor.UserActionInboundAdd,
			Messages: []*reactor.UserMessage{
				&reactor.UserMessage{
					Conn: &reactor.Conn{
						ConnId:   1,
						FromNode: 1,
					},
				},
			},
		})
		assert.Equal(t, 1, u.conns.len())

		u.step(reactor.UserAction{
			Type: reactor.UserActionNodeHeartbeatReq,
			From: 2,
		})
		assert.Equal(t, 1, u.conns.len())

		u.conns.updateConnAuth(1, 1, true)

		u.step(reactor.UserAction{
			Type: reactor.UserActionNodeHeartbeatReq,
			From: 2,
		})
		actions := u.ready()
		hasAction(t, reactor.UserActionConnClose, actions)
	})

	t.Run("testReplicaUserClose", func(t *testing.T) {
		becomeReplica()
		u.tick()
		u.tick()
		actions := u.ready()
		hasAction(t, reactor.UserActionUserClose, actions)
	})

	t.Run("testLeaderUserClose", func(t *testing.T) {
		becomeLeader()
		u.tick()
		actions := u.ready()
		hasAction(t, reactor.UserActionUserClose, actions)
	})

}

func TestUserRoleChange(t *testing.T) {
	options = NewOptions()
	options.NodeId = 1
	options.NodeHeartbeatTick = 1
	options.OutboundForwardIntervalTick = 1
	options.NodeHeartbeatTimeoutTick = 2
	options.LeaderIdleTimeoutTick = 1
	options.NodeVersion = func() uint64 {
		return 1
	}
	u := NewUser("no", "uid")

	u.step(reactor.UserAction{
		Type: reactor.UserActionInboundAdd,
		Messages: []*reactor.UserMessage{
			&reactor.UserMessage{},
		},
	})
	u.step(reactor.UserAction{
		Type: reactor.UserActionOutboundAdd,
		Messages: []*reactor.UserMessage{
			&reactor.UserMessage{},
		},
	})

	becomeFollower := func() {
		u.step(reactor.UserAction{
			Type: reactor.UserActionConfigUpdate,
			Cfg: reactor.UserConfig{
				LeaderId: 2,
			},
		})
	}

	becomeLeader := func() {
		u.step(reactor.UserAction{
			Type: reactor.UserActionConfigUpdate,
			Cfg: reactor.UserConfig{
				LeaderId: 1,
			},
		})
	}

	becomeFollower()

	assert.Equal(t, 1, u.inbound.queue.len())
	assert.Equal(t, 1, u.outbound.queue.len())

	becomeLeader()

	assert.Equal(t, 0, u.inbound.queue.len())
	assert.Equal(t, 0, u.outbound.queue.len())

}
