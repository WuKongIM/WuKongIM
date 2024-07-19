package server

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/stretchr/testify/assert"
)

func TestConversationGet(t *testing.T) {
	s := NewTestServer(t)
	err := s.Start()
	assert.NoError(t, err)
	defer func() {
		_ = s.Stop()
	}()

	s.conversationManager.Push("u1@u2", 1, []string{"u1", "u2"}, []ReactorChannelMessage{
		{
			FromUid:    "u1",
			MessageSeq: 100,
			SendPacket: &wkproto.SendPacket{},
		},
		{
			FromUid:    "u1",
			MessageSeq: 102,
			SendPacket: &wkproto.SendPacket{},
		},
	})

	conversations1 := s.conversationManager.GetUserConversationFromCache("u1", wkdb.ConversationTypeChat)
	assert.Equal(t, 1, len(conversations1))

	conversations2 := s.conversationManager.GetUserConversationFromCache("u2", wkdb.ConversationTypeChat)
	assert.Equal(t, 1, len(conversations2))

	assert.Equal(t, uint64(102), conversations1[0].ReadedToMsgSeq)
	assert.Equal(t, uint64(0), conversations2[0].ReadedToMsgSeq)

}
