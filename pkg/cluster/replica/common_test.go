package replica

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func hasMsg(messages []Message, msgType MsgType) bool {
	for _, m := range messages {
		if m.MsgType == msgType {
			return true
		}
	}
	return false
}

func getMsg(messages []Message, msgType MsgType) Message {
	for _, m := range messages {
		if m.MsgType == msgType {
			return m
		}
	}
	panic("not found")
}

func initReplica(r *Replica, cfg Config, t *testing.T) {
	has := r.HasReady()
	assert.True(t, has)

	rd := r.Ready()
	assert.True(t, hasMsg(rd.Messages, MsgInit))

	err := r.Step(Message{
		MsgType: MsgInitResp,
		Config:  cfg,
	})
	assert.NoError(t, err)
}
