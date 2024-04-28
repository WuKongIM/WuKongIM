package clusterstore_test

import (
	"testing"
)

func TestAppendMessage(t *testing.T) {
	// s1, t1, s2, t2, s3, t3 := newTestClusterServerGroupThree()
	// defer s1.Close()
	// defer s2.Close()
	// defer t1.Stop()
	// defer t2.Stop()

	// defer s3.Close()
	// defer t3.Stop()

	// t1.server.MustWaitAllSlotLeaderReady(time.Second * 20)
	// t2.server.MustWaitAllSlotLeaderReady(time.Second * 20)
	// t3.server.MustWaitAllSlotLeaderReady(time.Second * 20)

	// channelID := "test"
	// var channelType uint8 = 2

	// // 节点1添加消息
	// msg := &testMessage{
	// 	data: []byte("hello"),
	// }
	// err := s1.AppendMessages(context.Background(), channelID, channelType, []wkstore.Message{msg})
	// assert.NoError(t, err)

	// time.Sleep(time.Millisecond * 100)

	// // 节点2获取消息
	// messages, err := s2.LoadNextRangeMsgs(channelID, channelType, 0, 0, 10)
	// assert.NoError(t, err)
	// assert.Equal(t, 1, len(messages))

	// assert.Equal(t, msg.data, messages[0].(*testMessage).data)

	// messages, err = s1.LoadNextRangeMsgs(channelID, channelType, 0, 0, 10)
	// assert.NoError(t, err)
	// assert.Equal(t, 1, len(messages))
	// assert.Equal(t, msg.data, messages[0].(*testMessage).data)

	// messages, err = s3.LoadNextRangeMsgs(channelID, channelType, 0, 0, 10)
	// assert.NoError(t, err)
	// assert.Equal(t, 1, len(messages))
	// assert.Equal(t, msg.data, messages[0].(*testMessage).data)
}
