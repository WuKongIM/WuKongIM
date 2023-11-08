package server

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
	"github.com/stretchr/testify/assert"
)

func TestEncodeAndDecodeCMDAddOrUpdateConversations(t *testing.T) {
	uid := "1234"
	var slotID uint32 = 1
	conversations := make([]*wkstore.Conversation, 0)
	conversations = append(conversations, &wkstore.Conversation{
		UID:         "123456",
		ChannelID:   "test",
		ChannelType: 1,
	})
	req := NewCMDReq(CMDAddOrUpdateConversations)
	req.SlotID = &slotID
	req.Param = EncodeCMDAddOrUpdateConversations(uid, conversations)

	resultUID, resultConversations, err := req.DecodeCMDAddOrUpdateConversations()
	assert.NoError(t, err)
	assert.Equal(t, resultUID, uid)
	assert.Equal(t, len(resultConversations), len(conversations))

}
