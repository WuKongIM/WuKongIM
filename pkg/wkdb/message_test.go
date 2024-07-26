package wkdb_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/stretchr/testify/assert"
)

func TestLoadPrevRangeMsgs(t *testing.T) {

	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	messages := []wkdb.Message{}

	channelId := "channel"
	channelType := uint8(2)

	num := 100

	for i := 0; i < num; i++ {
		messages = append(messages, wkdb.Message{
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
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	messages := []wkdb.Message{}

	channelId := "channel"
	channelType := uint8(2)

	num := 100

	for i := 0; i < num; i++ {
		messages = append(messages, wkdb.Message{
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

	seq, _, err := d.GetChannelLastMessageSeq(channelId, channelType)
	assert.NoError(t, err)
	assert.Equal(t, uint64(num), seq)
}

func TestTruncateLogTo(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	messages := []wkdb.Message{}

	channelId := "channel"
	channelType := uint8(2)

	num := 100

	for i := 0; i < num; i++ {
		messages = append(messages, wkdb.Message{
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

	err = d.TruncateLogTo(channelId, channelType, 51)
	assert.NoError(t, err)

	resultMessages, err := d.LoadNextRangeMsgs(channelId, channelType, 51, 0, 0)
	assert.NoError(t, err)
	assert.Equal(t, 0, len(resultMessages))

	resultMessages, err = d.LoadNextRangeMsgs(channelId, channelType, 0, 51, 0)
	assert.NoError(t, err)
	assert.Equal(t, 50, len(resultMessages))
	assert.Equal(t, uint32(1), resultMessages[0].MessageSeq)
	assert.Equal(t, uint32(50), resultMessages[len(resultMessages)-1].MessageSeq)
}

func BenchmarkAppendMessages(b *testing.B) {
	d := newTestDB(b)
	err := d.Open()
	assert.NoError(b, err)

	defer func() {
		err := d.Close()
		assert.NoError(b, err)
	}()

	channelId := "channel"
	channelType := uint8(2)

	b.ResetTimer()

	b.RunParallel(func(p *testing.PB) {
		for p.Next() {
			err = d.AppendMessages(channelId, channelType, []wkdb.Message{
				{
					RecvPacket: wkproto.RecvPacket{
						ChannelID:   channelId,
						ChannelType: channelType,
						MessageSeq:  1,
						Payload:     []byte("hello"),
					},
				},
			})
			assert.NoError(b, err)
		}

	})
}

func TestSearchMessages(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	messages := []wkdb.Message{}

	channelId := "channel"
	channelType := uint8(2)

	num := 100

	for i := 0; i < num; i++ {
		messages = append(messages, wkdb.Message{
			RecvPacket: wkproto.RecvPacket{
				ChannelID:   channelId,
				ChannelType: channelType,
				MessageID:   int64(i + 1),
				MessageSeq:  uint32(i + 1),
				Payload:     []byte("hello"),
			},
		})
	}

	err = d.AppendMessages(channelId, channelType, messages)
	assert.NoError(t, err)

	resultMessages, err := d.SearchMessages(wkdb.MessageSearchReq{
		Limit:           10,
		OffsetMessageId: 0,
	})
	assert.NoError(t, err)
	assert.Equal(t, 10, len(resultMessages))

}
