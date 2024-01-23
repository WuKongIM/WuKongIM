package clusterconfig

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNodeElection(t *testing.T) {
	opts := NewOptions()
	opts.NodeId = 1
	opts.Replicas = []uint64{1, 2}
	n := NewNode(opts)
	n.campaign()

	rd := n.Ready()
	for _, msg := range rd.Messages {
		fmt.Println("msg--->", msg.Type)
		if msg.To == opts.NodeId {
			err := n.Step(msg)
			assert.NoError(t, err)
		} else if msg.Type == EventVote {
			err := n.Step(Message{
				From: msg.To,
				To:   msg.From,
				Type: EventVoteResp,
				Term: msg.Term,
			})
			assert.NoError(t, err)

		}
	}
	assert.Equal(t, RoleLeader, n.role)

}

func TestNodeApplyConfig(t *testing.T) {
	opts := NewOptions()
	opts.NodeId = 1
	opts.Replicas = []uint64{1, 2}

	n1 := NewNode(opts)
	becomeLeader(n1, t)

	opts = NewOptions()
	opts.NodeId = 2
	opts.Replicas = []uint64{1, 2}
	n2 := NewNode(opts)

	err := n1.ProposeConfigChange(1, []byte("hello"))
	assert.NoError(t, err)

	err = n1.Step(Message{
		From:          2,
		To:            1,
		Type:          EventSync,
		Term:          1,
		ConfigVersion: 2,
	})
	assert.NoError(t, err)

	rd := n1.Ready()
	for _, msg := range rd.Messages {
		if msg.To == 1 {
			if msg.Type == EventApply {
				err = n1.Step(Message{
					From:          msg.To,
					To:            msg.From,
					Type:          EventApplyResp,
					Term:          msg.Term,
					ConfigVersion: msg.ConfigVersion,
				})
				assert.NoError(t, err)
			} else {
				err := n1.Step(msg)
				assert.NoError(t, err)
			}

		} else if msg.To == 2 {
			err := n2.Step(msg)
			assert.NoError(t, err)
		}
	}
	n1.AcceptReady(rd)

	n1.Tick()

	rd = n1.Ready()
	for _, msg := range rd.Messages {
		if msg.To == 1 {
			err := n1.Step(msg)
			assert.NoError(t, err)
		} else if msg.To == 2 {
			err := n2.Step(msg)
			assert.NoError(t, err)
		}
	}
	n1.AcceptReady(rd)

	assert.Equal(t, []byte("hello"), n2.GetConfigData())

}

func becomeLeader(n *Node, t *testing.T) {
	n.campaign()

	rd := n.Ready()
	for _, msg := range rd.Messages {
		if msg.To == n.opts.NodeId {
			err := n.Step(msg)
			assert.NoError(t, err)
		} else if msg.Type == EventVote {
			err := n.Step(Message{
				From: msg.To,
				To:   msg.From,
				Type: EventVoteResp,
				Term: msg.Term,
			})
			assert.NoError(t, err)

		}
	}
	assert.Equal(t, RoleLeader, n.role)
	n.AcceptReady(rd)
}

func getMessageByType(msgs []Message, msgType EventType) Message {
	for _, msg := range msgs {
		if msg.Type == msgType {
			return msg
		}
	}
	return Message{}
}
