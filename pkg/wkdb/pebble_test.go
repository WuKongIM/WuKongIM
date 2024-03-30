package wkdb

import (
	"testing"

	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/stretchr/testify/assert"
)

func TestLoadPrevRangeMsgs(t *testing.T) {

	d := NewPebbleDB(NewOptions(WithDir(t.TempDir())))
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	messages := []Message{}

	channelId := "channel"
	channelType := uint8(2)

	num := 100

	for i := 0; i < num; i++ {
		messages = append(messages, Message{
			RecvPacket: wkproto.RecvPacket{
				ChannelID:   channelId,
				ChannelType: channelType,
				MessageSeq:  uint32(i + 1),
				Payload:     []byte("hello"),
			},
		})
	}

	err = d.AppendMessages(channelId, channelType, messages)
	assert.NoError(t, err)

	resultMessages, err := d.LoadPrevRangeMsgs(channelId, channelType, uint64(num), 0, num)
	assert.NoError(t, err)
	assert.Len(t, resultMessages, num)

	assert.Equal(t, uint32(1), resultMessages[0].MessageSeq)
	assert.Equal(t, uint32(num), resultMessages[len(resultMessages)-1].MessageSeq)

	resultMessages, err = d.LoadPrevRangeMsgs(channelId, channelType, 40, 30, num)
	assert.NoError(t, err)

	assert.Len(t, resultMessages, 10)
	assert.Equal(t, uint32(31), resultMessages[0].MessageSeq)
	assert.Equal(t, uint32(40), resultMessages[len(resultMessages)-1].MessageSeq)

}

func TestGetChannelMaxMessageSeq(t *testing.T) {
	d := NewPebbleDB(NewOptions(WithDir(t.TempDir())))
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	messages := []Message{}

	channelId := "channel"
	channelType := uint8(2)

	num := 100

	for i := 0; i < num; i++ {
		messages = append(messages, Message{
			RecvPacket: wkproto.RecvPacket{
				ChannelID:   channelId,
				ChannelType: channelType,
				MessageSeq:  uint32(i + 1),
				Payload:     []byte("hello"),
			},
		})
	}

	err = d.AppendMessages(channelId, channelType, messages)
	assert.NoError(t, err)

	seq, err := d.GetChannelLastMessageSeq(channelId, channelType)
	assert.NoError(t, err)
	assert.Equal(t, uint64(num), seq)
}

func TestTruncateLogTo(t *testing.T) {
	d := NewPebbleDB(NewOptions(WithDir(t.TempDir())))
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	messages := []Message{}

	channelId := "channel"
	channelType := uint8(2)

	num := 100

	for i := 0; i < num; i++ {
		messages = append(messages, Message{
			RecvPacket: wkproto.RecvPacket{
				ChannelID:   channelId,
				ChannelType: channelType,
				MessageSeq:  uint32(i + 1),
				Payload:     []byte("hello"),
			},
		})
	}

	err = d.AppendMessages(channelId, channelType, messages)
	assert.NoError(t, err)

	err = d.TruncateLogTo(channelId, channelType, 50)
	assert.NoError(t, err)

	seq, err := d.GetChannelLastMessageSeq(channelId, channelType)
	assert.NoError(t, err)
	assert.Equal(t, uint64(50), seq)

	resultMessages, err := d.LoadPrevRangeMsgs(channelId, channelType, 50, 0, 50)
	assert.NoError(t, err)
	assert.Len(t, resultMessages, 50)
	assert.Equal(t, uint32(1), resultMessages[0].MessageSeq)
	assert.Equal(t, uint32(50), resultMessages[len(resultMessages)-1].MessageSeq)
}
