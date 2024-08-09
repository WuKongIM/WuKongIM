package server

import (
	"testing"

	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/stretchr/testify/assert"
)

func TestChannelMessagesSetMarshal(t *testing.T) {
	channelMessages := ChannelMessagesSet{}
	channelMessages = append(channelMessages, &ChannelMessages{
		ChannelId:   "test",
		ChannelType: 1,
		TagKey:      "test",
		Messages: ReactorChannelMessageSet{
			ReactorChannelMessage{
				MessageId:  1,
				FromConnId: 1,
				FromUid:    "test",
				SendPacket: &wkproto.SendPacket{
					ChannelID:   "test",
					ChannelType: 1,
					Payload:     []byte("testtesttesttesttesttesttesttesttesttesttesttest"),
				},
			},
		},
	})
	data, err := channelMessages.Marshal()
	assert.Nil(t, err)

	channelMessages = ChannelMessagesSet{}
	err = channelMessages.Unmarshal(data)
	assert.Nil(t, err)
	assert.Equal(t, 1, len(channelMessages))
}
