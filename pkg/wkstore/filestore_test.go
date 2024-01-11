package wkstore

import (
	"fmt"
	"io/ioutil"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestFileStoreMsg(t *testing.T) {
	dir, err := ioutil.TempDir("", "filestore")
	assert.NoError(t, err)
	store := NewFileStore(&StoreConfig{
		SlotNum: 1024,
		DataDir: dir,
	})

	store.AppendMessages("testtopic", 1, []Message{
		&testMessage{
			data: []byte("test1"),
		},
		&testMessage{
			data: []byte("test2"),
		},
	})
}

func TestFileWriteAndRead(t *testing.T) {

	f1, _ := os.OpenFile("test.txt", os.O_RDWR|os.O_CREATE|os.O_APPEND, 0755)

	defer f1.Close()

	f1.Write([]byte("test1"))

	f2, _ := os.OpenFile("test.txt", os.O_RDWR|os.O_CREATE|os.O_APPEND, 0755)

	testBytes := make([]byte, 100)
	n, _ := f2.ReadAt(testBytes, 0)
	fmt.Println("zzz--->", string(testBytes[:n]))

}

func TestAddOrUpdateChannel(t *testing.T) {
	dir, err := ioutil.TempDir("", "filestore")
	assert.NoError(t, err)

	fmt.Println("dir---->", dir)
	store := NewFileStore(&StoreConfig{
		SlotNum: 256,
		DataDir: dir,
	})
	err = store.Open()
	assert.NoError(t, err)

	defer store.Close()

	err = store.AddOrUpdateChannel(&ChannelInfo{
		ChannelID:   "testtopic",
		ChannelType: 1,
		Ban:         true,
		Large:       true,
	})
	assert.NoError(t, err)

	channelInfo, err := store.GetChannel("testtopic", 1)
	assert.NoError(t, err)

	assert.Equal(t, true, channelInfo.Ban)
	assert.Equal(t, true, channelInfo.Large)

}

func TestDeleteChannel(t *testing.T) {
	dir, err := ioutil.TempDir("", "filestore")
	assert.NoError(t, err)

	fmt.Println("dir---->", dir)

	store := NewFileStore(&StoreConfig{
		SlotNum: 256,
		DataDir: dir,
	})
	err = store.Open()
	assert.NoError(t, err)
	defer store.Close()

	err = store.AddOrUpdateChannel(&ChannelInfo{
		ChannelID:   "testtopic",
		ChannelType: 1,
		Ban:         true,
		Large:       true,
	})
	assert.NoError(t, err)

	channelInfo, err := store.GetChannel("testtopic", 1)
	assert.NoError(t, err)

	assert.Equal(t, true, channelInfo.Ban)
	assert.Equal(t, true, channelInfo.Large)

	err = store.DeleteChannel("testtopic", 1)
	assert.NoError(t, err)

	channelInfo, err = store.GetChannel("testtopic", 1)
	assert.NoError(t, err)
	assert.Nil(t, channelInfo)

}

func TestUpdateUserToken(t *testing.T) {
	dir, err := ioutil.TempDir("", "filestore")
	assert.NoError(t, err)

	fmt.Println("dir---->", dir)
	store := NewFileStore(&StoreConfig{
		SlotNum: 256,
		DataDir: dir,
	})
	err = store.Open()
	assert.NoError(t, err)

	defer store.Close()

	uid := "test"
	token := "token"
	var deviceFlag uint8 = 1
	var deviceLevel uint8 = 1

	err = store.UpdateUserToken(uid, deviceFlag, deviceLevel, token)
	assert.NoError(t, err)

	resultToken, level, err := store.GetUserToken(uid, deviceFlag)
	assert.NoError(t, err)
	assert.Equal(t, token, resultToken)
	assert.Equal(t, deviceLevel, level)
}

func TestAddSubscribers(t *testing.T) {
	dir, err := ioutil.TempDir("", "filestore")
	assert.NoError(t, err)

	fmt.Println("dir---->", dir)
	store := NewFileStore(&StoreConfig{
		SlotNum: 256,
		DataDir: dir,
	})
	err = store.Open()
	assert.NoError(t, err)

	defer store.Close()

	channelID := "ch1"
	channelType := uint8(2)

	uids := []string{"test1", "test2", "test3"}
	err = store.AddSubscribers(channelID, channelType, uids)
	assert.NoError(t, err)

	resultSubscribers, err := store.GetSubscribers(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, uids, resultSubscribers)
}

func TestRemoveSubscribers(t *testing.T) {
	dir, err := ioutil.TempDir("", "filestore")
	assert.NoError(t, err)

	fmt.Println("dir---->", dir)
	store := NewFileStore(&StoreConfig{
		SlotNum: 256,
		DataDir: dir,
	})
	err = store.Open()
	assert.NoError(t, err)

	defer store.Close()

	channelID := "ch1"
	channelType := uint8(2)

	uids := []string{"test1", "test2", "test3"}
	err = store.AddSubscribers(channelID, channelType, uids)
	assert.NoError(t, err)

	err = store.RemoveSubscribers(channelID, channelType, []string{"test1"})
	assert.NoError(t, err)

	resultSubscribers, err := store.GetSubscribers(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, []string{"test2", "test3"}, resultSubscribers)
}

func TestRemoveAllSubscriber(t *testing.T) {
	dir, err := ioutil.TempDir("", "filestore")
	assert.NoError(t, err)

	fmt.Println("dir---->", dir)
	store := NewFileStore(&StoreConfig{
		SlotNum: 256,
		DataDir: dir,
	})
	err = store.Open()
	assert.NoError(t, err)

	defer store.Close()

	channelID := "ch1"
	channelType := uint8(2)

	uids := []string{"test1", "test2", "test3"}
	err = store.AddSubscribers(channelID, channelType, uids)
	assert.NoError(t, err)

	err = store.RemoveAllSubscriber(channelID, channelType)
	assert.NoError(t, err)

	resultSubscribers, err := store.GetSubscribers(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, []string{}, resultSubscribers)
}

func TestAddAllowlist(t *testing.T) {
	dir, err := ioutil.TempDir("", "filestore")
	assert.NoError(t, err)

	fmt.Println("dir---->", dir)
	store := NewFileStore(&StoreConfig{
		SlotNum: 256,
		DataDir: dir,
	})
	err = store.Open()
	assert.NoError(t, err)

	defer store.Close()

	channelID := "ch1"
	channelType := uint8(2)

	uids := []string{"test1", "test2", "test3"}
	err = store.AddAllowlist(channelID, channelType, uids)
	assert.NoError(t, err)

	resultAllowlist, err := store.GetAllowlist(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, uids, resultAllowlist)
}

func TestRemoveAllowlist(t *testing.T) {
	dir, err := ioutil.TempDir("", "filestore")
	assert.NoError(t, err)

	fmt.Println("dir---->", dir)
	store := NewFileStore(&StoreConfig{
		SlotNum: 256,
		DataDir: dir,
	})
	err = store.Open()
	assert.NoError(t, err)

	defer store.Close()

	channelID := "ch1"
	channelType := uint8(2)

	uids := []string{"test1", "test2", "test3"}
	err = store.AddAllowlist(channelID, channelType, uids)
	assert.NoError(t, err)

	err = store.RemoveAllowlist(channelID, channelType, []string{"test1"})
	assert.NoError(t, err)

	resultAllowlist, err := store.GetAllowlist(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, []string{"test2", "test3"}, resultAllowlist)
}

func TestRemoveAllAllowlist(t *testing.T) {
	dir, err := ioutil.TempDir("", "filestore")
	assert.NoError(t, err)

	fmt.Println("dir---->", dir)
	store := NewFileStore(&StoreConfig{
		SlotNum: 256,
		DataDir: dir,
	})
	err = store.Open()
	assert.NoError(t, err)

	defer store.Close()

	channelID := "ch1"
	channelType := uint8(2)

	uids := []string{"test1", "test2", "test3"}
	err = store.AddAllowlist(channelID, channelType, uids)
	assert.NoError(t, err)

	err = store.RemoveAllAllowlist(channelID, channelType)
	assert.NoError(t, err)

	resultAllowlist, err := store.GetAllowlist(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, []string{}, resultAllowlist)
}

func TestAddDenylist(t *testing.T) {
	dir, err := ioutil.TempDir("", "filestore")
	assert.NoError(t, err)

	fmt.Println("dir---->", dir)
	store := NewFileStore(&StoreConfig{
		SlotNum: 256,
		DataDir: dir,
	})
	err = store.Open()
	assert.NoError(t, err)
	defer store.Close()

	channelID := "ch1"
	channelType := uint8(2)

	uids := []string{"test1", "test2", "test3"}
	err = store.AddDenylist(channelID, channelType, uids)
	assert.NoError(t, err)

	resultDenylist, err := store.GetDenylist(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, uids, resultDenylist)
}

func TestRemoveDenylist(t *testing.T) {
	dir, err := ioutil.TempDir("", "filestore")
	assert.NoError(t, err)

	fmt.Println("dir---->", dir)

	store := NewFileStore(&StoreConfig{
		SlotNum: 256,
		DataDir: dir,
	})
	err = store.Open()
	assert.NoError(t, err)
	defer store.Close()

	channelID := "ch1"
	channelType := uint8(2)

	uids := []string{"test1", "test2", "test3"}
	err = store.AddDenylist(channelID, channelType, uids)
	assert.NoError(t, err)

	err = store.RemoveDenylist(channelID, channelType, []string{"test1"})
	assert.NoError(t, err)

	resultDenylist, err := store.GetDenylist(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, []string{"test2", "test3"}, resultDenylist)
}

func TestRemoveAllDenylist(t *testing.T) {
	dir, err := ioutil.TempDir("", "filestore")
	assert.NoError(t, err)

	fmt.Println("dir---->", dir)

	store := NewFileStore(&StoreConfig{
		SlotNum: 256,
		DataDir: dir,
	})
	err = store.Open()
	assert.NoError(t, err)
	defer store.Close()

	channelID := "ch1"
	channelType := uint8(2)

	uids := []string{"test1", "test2", "test3"}
	err = store.AddDenylist(channelID, channelType, uids)
	assert.NoError(t, err)

	err = store.RemoveAllDenylist(channelID, channelType)
	assert.NoError(t, err)

	resultDenylist, err := store.GetDenylist(channelID, channelType)
	assert.NoError(t, err)
	assert.Equal(t, []string{}, resultDenylist)
}

func TestAppendMessagesOfUser(t *testing.T) {
	dir, err := ioutil.TempDir("", "filestore")
	assert.NoError(t, err)

	fmt.Println("dir---->", dir)

	storeCfg := NewStoreConfig()
	storeCfg.DecodeMessageFnc = func(data []byte) (Message, error) {
		tm := &testMessage{}
		err = tm.Decode(data)
		return tm, err
	}
	store := NewFileStore(storeCfg)
	err = store.Open()
	assert.NoError(t, err)
	defer store.Close()

	uid := "test"

	_, err = store.AppendMessagesOfUser(uid, []Message{
		&testMessage{
			data: []byte("test1"),
			seq:  1,
		},
		&testMessage{
			data: []byte("test2"),
			seq:  2,
		},
	})
	assert.NoError(t, err)

	messages, err := store.SyncMessageOfUser(uid, 0, 2)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(messages))
	assert.Equal(t, uint32(1), messages[0].GetSeq())
	assert.Equal(t, uint32(2), messages[1].GetSeq())

}

func TestUpdateMessageOfUserCursorIfNeed(t *testing.T) {
	dir, err := ioutil.TempDir("", "filestore")
	assert.NoError(t, err)

	fmt.Println("dir---->", dir)

	storeCfg := NewStoreConfig()
	storeCfg.DataDir = dir
	store := NewFileStore(storeCfg)
	err = store.Open()
	assert.NoError(t, err)
	defer store.Close()

	uid := "test"

	_, err = store.AppendMessagesOfUser(uid, []Message{
		&testMessage{
			data:      []byte("test1"),
			messageID: 1001,
			seq:       1,
		},
		&testMessage{
			data:      []byte("test2"),
			messageID: 10002,
			seq:       2,
		},
	})
	assert.NoError(t, err)

	err = store.UpdateMessageOfUserCursorIfNeed(uid, 1)
	assert.NoError(t, err)

	cursor, err := store.GetMessageOfUserCursor(uid)
	assert.NoError(t, err)
	assert.Equal(t, uint32(1), cursor)

	err = store.UpdateMessageOfUserCursorIfNeed(uid, 2)
	assert.NoError(t, err)

	cursor, err = store.GetMessageOfUserCursor(uid)
	assert.NoError(t, err)
	assert.Equal(t, uint32(2), cursor)

}

func TestAddSystemUIDs(t *testing.T) {
	dir, err := ioutil.TempDir("", "filestore")
	assert.NoError(t, err)

	storeCfg := NewStoreConfig()
	storeCfg.DataDir = dir
	store := NewFileStore(storeCfg)
	err = store.Open()
	assert.NoError(t, err)
	defer store.Close()

	uids := []string{"test1", "test2", "test3"}
	err = store.AddSystemUIDs(uids)
	assert.NoError(t, err)

	resultUids, err := store.GetSystemUIDs()
	assert.NoError(t, err)
	assert.Equal(t, uids, resultUids)
}

func TestAddOrUpdateConversations(t *testing.T) {
	dir, err := ioutil.TempDir("", "filestore")
	assert.NoError(t, err)

	storeCfg := NewStoreConfig()
	storeCfg.DataDir = dir
	store := NewFileStore(storeCfg)
	err = store.Open()
	assert.NoError(t, err)
	defer store.Close()

	tm := time.Now().Unix()
	conv := &Conversation{
		ChannelID:       "ch1",
		ChannelType:     1,
		LastMsgSeq:      1,
		Timestamp:       tm,
		LastMsgID:       1,
		LastClientMsgNo: "123",
		UnreadCount:     12,
		Version:         1,
	}

	uid := "u1"

	err = store.AddOrUpdateConversations(uid, []*Conversation{conv})
	assert.NoError(t, err)

	resultConv, err := store.GetConversation(uid, conv.ChannelID, conv.ChannelType)
	assert.NoError(t, err)

	assert.Equal(t, conv.ChannelID, resultConv.ChannelID)
	assert.Equal(t, conv.ChannelType, resultConv.ChannelType)
	assert.Equal(t, conv.LastMsgSeq, resultConv.LastMsgSeq)
	assert.Equal(t, conv.Timestamp, resultConv.Timestamp)
	assert.Equal(t, conv.LastMsgID, resultConv.LastMsgID)
	assert.Equal(t, conv.LastClientMsgNo, resultConv.LastClientMsgNo)
	assert.Equal(t, conv.UnreadCount, resultConv.UnreadCount)
	assert.Equal(t, conv.Version, resultConv.Version)

}

func TestGetConversations(t *testing.T) {
	dir, err := ioutil.TempDir("", "filestore")
	assert.NoError(t, err)

	storeCfg := NewStoreConfig()
	storeCfg.DataDir = dir
	store := NewFileStore(storeCfg)
	err = store.Open()
	assert.NoError(t, err)
	defer store.Close()

	uid := "u1"

	resultConvs, err := store.GetConversations(uid)
	assert.NoError(t, err)
	assert.Equal(t, 0, len(resultConvs))

	tm := time.Now().Unix()
	conv := &Conversation{
		ChannelID:       "ch1",
		ChannelType:     1,
		LastMsgSeq:      1,
		Timestamp:       tm,
		LastMsgID:       1,
		LastClientMsgNo: "123",
		UnreadCount:     12,
		Version:         1,
	}

	err = store.AddOrUpdateConversations(uid, []*Conversation{conv})
	assert.NoError(t, err)

	resultConvs, err = store.GetConversations(uid)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(resultConvs))
	assert.Equal(t, conv.ChannelID, resultConvs[0].ChannelID)
	assert.Equal(t, conv.ChannelType, resultConvs[0].ChannelType)
	assert.Equal(t, conv.LastMsgSeq, resultConvs[0].LastMsgSeq)
	assert.Equal(t, conv.Timestamp, resultConvs[0].Timestamp)
	assert.Equal(t, conv.LastMsgID, resultConvs[0].LastMsgID)
	assert.Equal(t, conv.LastClientMsgNo, resultConvs[0].LastClientMsgNo)
	assert.Equal(t, conv.UnreadCount, resultConvs[0].UnreadCount)
	assert.Equal(t, conv.Version, resultConvs[0].Version)

}

func TestDeleteConversation(t *testing.T) {
	dir, err := ioutil.TempDir("", "filestore")
	assert.NoError(t, err)

	storeCfg := NewStoreConfig()
	storeCfg.DataDir = dir
	store := NewFileStore(storeCfg)
	err = store.Open()
	assert.NoError(t, err)
	defer store.Close()

	tm := time.Now().Unix()
	conv := &Conversation{
		ChannelID:       "ch1",
		ChannelType:     1,
		LastMsgSeq:      1,
		Timestamp:       tm,
		LastMsgID:       1,
		LastClientMsgNo: "123",
		UnreadCount:     12,
		Version:         1,
	}

	uid := "u1"

	err = store.AddOrUpdateConversations(uid, []*Conversation{conv})
	assert.NoError(t, err)

	resultConv, err := store.GetConversation(uid, conv.ChannelID, conv.ChannelType)
	assert.NoError(t, err)

	assert.Equal(t, conv.ChannelID, resultConv.ChannelID)
	assert.Equal(t, conv.ChannelType, resultConv.ChannelType)
	assert.Equal(t, conv.LastMsgSeq, resultConv.LastMsgSeq)
	assert.Equal(t, conv.Timestamp, resultConv.Timestamp)
	assert.Equal(t, conv.LastMsgID, resultConv.LastMsgID)
	assert.Equal(t, conv.LastClientMsgNo, resultConv.LastClientMsgNo)
	assert.Equal(t, conv.UnreadCount, resultConv.UnreadCount)
	assert.Equal(t, conv.Version, resultConv.Version)

	err = store.DeleteConversation(uid, conv.ChannelID, conv.ChannelType)
	assert.NoError(t, err)

	resultConv, err = store.GetConversation(uid, conv.ChannelID, conv.ChannelType)
	assert.NoError(t, err)
	assert.Nil(t, resultConv)

}

func TestAppendMessageOfNotifyQueue(t *testing.T) {
	dir, err := ioutil.TempDir("", "filestore")
	assert.NoError(t, err)

	storeCfg := NewStoreConfig()
	storeCfg.DataDir = dir
	storeCfg.DecodeMessageFnc = func(data []byte) (Message, error) {
		tm := &testMessage{}
		err = tm.Decode(data)
		return tm, err
	}
	store := NewFileStore(storeCfg)
	err = store.Open()
	assert.NoError(t, err)
	defer store.Close()

	messages := []Message{
		&testMessage{
			data:      []byte("test1"),
			seq:       1,
			messageID: 1001,
		},
	}

	err = store.AppendMessageOfNotifyQueue(messages)
	assert.NoError(t, err)

	resultNotify, err := store.GetMessagesOfNotifyQueue(1)
	assert.NoError(t, err)

	assert.Equal(t, 1, len(resultNotify))
}

func TestRemoveMessagesOfNotifyQueue(t *testing.T) {
	dir, err := ioutil.TempDir("", "filestore")
	assert.NoError(t, err)

	storeCfg := NewStoreConfig()
	storeCfg.DataDir = dir
	storeCfg.DecodeMessageFnc = func(data []byte) (Message, error) {
		tm := &testMessage{}
		err = tm.Decode(data)
		return tm, err
	}
	store := NewFileStore(storeCfg)
	err = store.Open()
	assert.NoError(t, err)
	defer store.Close()

	messages := []Message{
		&testMessage{
			data:      []byte("test1"),
			seq:       1,
			messageID: 1001,
		},
	}

	err = store.AppendMessageOfNotifyQueue(messages)
	assert.NoError(t, err)

	resultNotify, err := store.GetMessagesOfNotifyQueue(1)
	assert.NoError(t, err)

	assert.Equal(t, 1, len(resultNotify))

	err = store.RemoveMessagesOfNotifyQueue([]int64{1001})
	assert.NoError(t, err)

	resultNotify, err = store.GetMessagesOfNotifyQueue(1)
	assert.NoError(t, err)
	assert.Equal(t, 0, len(resultNotify))
}
