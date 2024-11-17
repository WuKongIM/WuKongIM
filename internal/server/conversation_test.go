package server

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/stretchr/testify/assert"
)

func TestConversationUpdateForPersonChannel(t *testing.T) {
	s := NewTestServer(t)
	err := s.Start()
	assert.NoError(t, err)
	defer func() {
		_ = s.Stop()
	}()

	s.MustWaitAllSlotsReady(time.Second * 10)

	req := &conversationReq{
		channelId:   "u1@u2",
		channelType: wkproto.ChannelTypePerson,
		messages: []ReactorChannelMessage{
			{
				FromUid:    "u1",
				MessageSeq: 1,
				SendPacket: &wkproto.SendPacket{},
			},
			{
				FromUid:    "u1",
				MessageSeq: 2,
				SendPacket: &wkproto.SendPacket{},
			},
		},
	}
	s.conversationManager.Push(req)

	time.Sleep(time.Millisecond * 100)

	conversations1 := s.conversationManager.GetUserConversationFromCache("u1", wkdb.ConversationTypeChat)
	assert.Equal(t, 1, len(conversations1))

	conversations2 := s.conversationManager.GetUserConversationFromCache("u2", wkdb.ConversationTypeChat)
	assert.Equal(t, 1, len(conversations2))

	assert.Equal(t, uint64(2), conversations1[0].ReadToMsgSeq)
	assert.Equal(t, uint64(0), conversations2[0].ReadToMsgSeq)

}

func TestConversationUpdateForGroupChannel(t *testing.T) {
	s := NewTestServer(t)
	err := s.Start()
	assert.NoError(t, err)
	defer func() {
		_ = s.Stop()
	}()

	s.MustWaitAllSlotsReady(time.Second * 10)

	channelId := "g1"
	channelType := wkproto.ChannelTypeGroup

	err = s.store.AddSubscribers(channelId, channelType, []wkdb.Member{
		{
			Uid: "u1",
		},
		{
			Uid: "u2",
		},
	})
	assert.NoError(t, err)

	req := &conversationReq{
		channelId:   channelId,
		channelType: wkproto.ChannelTypeGroup,
		messages: []ReactorChannelMessage{
			{
				FromUid:    "u1",
				MessageSeq: 1,
				SendPacket: &wkproto.SendPacket{},
			},
			{
				FromUid:    "u2",
				MessageSeq: 2,
				SendPacket: &wkproto.SendPacket{},
			},
		},
	}
	s.conversationManager.Push(req)

	time.Sleep(time.Millisecond * 100)

	conversations1 := s.conversationManager.GetUserConversationFromCache("u1", wkdb.ConversationTypeChat)
	assert.Equal(t, 1, len(conversations1))

	conversations2 := s.conversationManager.GetUserConversationFromCache("u2", wkdb.ConversationTypeChat)
	assert.Equal(t, 1, len(conversations2))

	assert.Equal(t, uint64(1), conversations1[0].ReadToMsgSeq)
	assert.Equal(t, uint64(2), conversations2[0].ReadToMsgSeq)

}

func TestConversationUpdateForCMDChannel(t *testing.T) {
	s := NewTestServer(t)
	err := s.Start()
	assert.NoError(t, err)
	defer func() {
		_ = s.Stop()
	}()

	s.MustWaitAllSlotsReady(time.Second * 10)

	channelId := "g1"
	channelType := wkproto.ChannelTypeGroup

	cmdChannelId := s.opts.OrginalConvertCmdChannel(channelId)

	err = s.store.AddSubscribers(channelId, channelType, []wkdb.Member{
		{
			Uid: "u1",
		},
		{
			Uid: "u2",
		},
	})
	assert.NoError(t, err)

	tagKey := "tagtest"
	s.tagManager.addOrUpdateReceiverTag(tagKey, []*nodeUsers{
		{
			nodeId: s.opts.Cluster.NodeId,
			uids:   []string{"u1", "u2"},
		},
	}, channelId, channelType)

	req := &conversationReq{
		channelId:   cmdChannelId,
		channelType: wkproto.ChannelTypeGroup,
		tagKey:      tagKey,
		messages: []ReactorChannelMessage{
			{
				FromUid:    "u1",
				MessageSeq: 2,
				SendPacket: &wkproto.SendPacket{},
			},
		},
	}
	s.conversationManager.Push(req)

	time.Sleep(time.Millisecond * 100)

	conversations1 := s.conversationManager.GetUserConversationFromCache("u1", wkdb.ConversationTypeCMD)
	assert.Equal(t, 1, len(conversations1))

	conversations2 := s.conversationManager.GetUserConversationFromCache("u2", wkdb.ConversationTypeCMD)
	assert.Equal(t, 1, len(conversations2))

	assert.Equal(t, uint64(2), conversations1[0].ReadToMsgSeq)
	assert.Equal(t, uint64(0), conversations2[0].ReadToMsgSeq)

}

func TestConversationUpdateForPropose(t *testing.T) {
	s := NewTestServer(t)
	err := s.Start()
	assert.NoError(t, err)
	defer func() {
		_ = s.Stop()
	}()

	s.MustWaitAllSlotsReady(time.Second * 10)

	channelId := "g1"
	channelType := wkproto.ChannelTypeGroup

	err = s.store.AddSubscribers(channelId, channelType, []wkdb.Member{
		{
			Uid: "u1",
		},
		{
			Uid: "u2",
		},
	})
	assert.NoError(t, err)

	tagKey := "tagtest"
	s.tagManager.addOrUpdateReceiverTag(tagKey, []*nodeUsers{
		{
			nodeId: s.opts.Cluster.NodeId,
			uids:   []string{"u1", "u2"},
		},
	}, channelId, channelType)

	req := &conversationReq{
		channelId:   channelId,
		channelType: wkproto.ChannelTypeGroup,
		tagKey:      tagKey,
		messages: []ReactorChannelMessage{
			{
				FromUid:    "u1",
				MessageSeq: 1,
				SendPacket: &wkproto.SendPacket{},
			},
			{
				FromUid:    "u2",
				MessageSeq: 2,
				SendPacket: &wkproto.SendPacket{},
			},
		},
	}
	s.conversationManager.Push(req)

	time.Sleep(time.Millisecond * 100)

	conversations1 := s.conversationManager.GetUserConversationFromCache("u1", wkdb.ConversationTypeChat)
	assert.Equal(t, 1, len(conversations1))

	conversations2 := s.conversationManager.GetUserConversationFromCache("u2", wkdb.ConversationTypeChat)
	assert.Equal(t, 1, len(conversations2))

	assert.Equal(t, uint64(1), conversations1[0].ReadToMsgSeq)
	assert.Equal(t, uint64(2), conversations2[0].ReadToMsgSeq)

	s.conversationManager.ForcePropose()

	conversations1 = s.conversationManager.GetUserConversationFromCache("u1", wkdb.ConversationTypeChat)
	assert.Equal(t, 0, len(conversations1))

	conversations2 = s.conversationManager.GetUserConversationFromCache("u2", wkdb.ConversationTypeChat)
	assert.Equal(t, 0, len(conversations2))

	conversations1, err = s.store.GetConversationsByType("u1", wkdb.ConversationTypeChat)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(conversations1))

	conversations2, err = s.store.GetConversationsByType("u2", wkdb.ConversationTypeChat)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(conversations1))

	assert.Equal(t, uint64(1), conversations1[0].ReadToMsgSeq)
	assert.Equal(t, uint64(2), conversations2[0].ReadToMsgSeq)

}
