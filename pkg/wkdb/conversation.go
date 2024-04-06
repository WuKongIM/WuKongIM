package wkdb

import (
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb/key"
	"github.com/cockroachdb/pebble"
)

func (wk *wukongDB) AddOrUpdateConversations(uid string, conversations []Conversation) error {
	batch := wk.db.NewBatch()
	defer batch.Close()
	for _, cn := range conversations {
		id, err := wk.getConversationIdByChannel(uid, cn.ChannelId, cn.ChannelType)
		if err != nil {
			return err
		}
		if id == 0 {
			id = uint64(wk.prmaryKeyGen.Generate().Int64())
		}
		if err := wk.writeConversation(uint64(id), uid, cn, batch); err != nil {
			return err
		}
	}

	return batch.Commit(wk.wo)
}

// GetConversations 获取指定用户的最近会话
func (wk *wukongDB) GetConversations(uid string) ([]Conversation, error) {

	ids, err := wk.getConversationIdsByTimestamp(uid) // 通过时间倒序，获取到ids
	if err != nil {
		return nil, err
	}
	results := make([]Conversation, 0, len(ids))
	for _, id := range ids {
		iter := wk.db.NewIter(&pebble.IterOptions{
			LowerBound: key.NewConversationColumnKey(uid, id, [2]byte{0x00, 0x00}),
			UpperBound: key.NewConversationColumnKey(uid, id, [2]byte{0xff, 0xff}),
		})
		defer iter.Close()
		conversations, err := wk.parseConversations(iter, 1)
		if err != nil {
			return nil, err
		}
		if len(conversations) > 0 {
			results = append(results, conversations[0])
		}
	}
	return results, nil
}

// DeleteConversation 删除最近会话
func (wk *wukongDB) DeleteConversation(uid string, channelId string, channelType uint8) error {

	id, err := wk.getConversationIdByChannel(uid, channelId, channelType)
	if err != nil {
		return err
	}
	if id == 0 {
		return nil
	}
	// 删除索引
	batch := wk.db.NewBatch()
	err = batch.Delete(key.NewConversationIndexChannelKey(uid, channelId, channelType), wk.wo)
	if err != nil {
		return err
	}

	// 删除数据
	err = batch.DeleteRange(key.NewConversationColumnKey(uid, id, [2]byte{0x00, 0x00}), key.NewConversationColumnKey(uid, id, [2]byte{0xff, 0xff}), wk.wo)
	if err != nil {
		return err
	}
	return batch.Commit(wk.wo)

}

// GetConversation 获取指定用户的指定会话
func (wk *wukongDB) GetConversation(uid string, channelId string, channelType uint8) (Conversation, error) {

	id, err := wk.getConversationIdByChannel(uid, channelId, channelType)
	if err != nil {
		return EmptyConversation, err
	}

	if id == 0 {
		return EmptyConversation, nil
	}

	iter := wk.db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewConversationColumnKey(uid, id, [2]byte{0x00, 0x00}),
		UpperBound: key.NewConversationColumnKey(uid, id, [2]byte{0xff, 0xff}),
	})
	defer iter.Close()

	conversations, err := wk.parseConversations(iter, 1)
	if err != nil {
		return EmptyConversation, err
	}

	if len(conversations) == 0 {
		return EmptyConversation, nil
	}

	return conversations[0], nil
}

func (wk *wukongDB) getConversationIdsByTimestamp(uid string) ([]uint64, error) {
	iter := wk.db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewConversationSecondIndexTimestampKey(uid, 0, 0),
		UpperBound: key.NewConversationSecondIndexTimestampKey(uid, math.MaxUint64, math.MaxUint64),
	})
	defer iter.Close()

	ids := make([]uint64, 0)

	for iter.Last(); iter.Valid(); iter.Prev() {
		_, id, err := key.ParseConversationSecondIndexTimestampKey(iter.Key())
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func (wk *wukongDB) getConversationIdByChannel(uid, channelId string, channelType uint8) (uint64, error) {
	idBytes, closer, err := wk.db.Get(key.NewConversationIndexChannelKey(uid, channelId, channelType))
	if err != nil {
		if err == pebble.ErrNotFound {
			return 0, nil
		}
		return 0, err
	}
	defer closer.Close()
	return wk.endian.Uint64(idBytes), nil
}

func (wk *wukongDB) writeConversation(id uint64, uid string, conversation Conversation, w pebble.Writer) error {
	var (
		err error
	)

	// uid
	if err = w.Set(key.NewConversationColumnKey(uid, id, key.TableConversation.Column.Uid), []byte(uid), wk.wo); err != nil {
		return err
	}

	// channelId
	if err = w.Set(key.NewConversationColumnKey(uid, id, key.TableConversation.Column.ChannelId), []byte(conversation.ChannelId), wk.wo); err != nil {
		return err
	}

	// channelType
	if err = w.Set(key.NewConversationColumnKey(uid, id, key.TableConversation.Column.ChannelType), []byte{conversation.ChannelType}, wk.wo); err != nil {
		return err
	}

	// unreadCount
	var unreadCountBytes = make([]byte, 4)
	wk.endian.PutUint32(unreadCountBytes, conversation.UnreadCount)
	if err = w.Set(key.NewConversationColumnKey(uid, id, key.TableConversation.Column.UnreadCount), unreadCountBytes, wk.wo); err != nil {
		return err
	}

	// timestamp
	var timestampBytes = make([]byte, 8)
	wk.endian.PutUint64(timestampBytes, uint64(conversation.Timestamp))
	if err = w.Set(key.NewConversationColumnKey(uid, id, key.TableConversation.Column.Timestamp), timestampBytes, wk.wo); err != nil {
		return err
	}

	// lastMsgSeq
	var lastMsgSeqBytes = make([]byte, 8)
	wk.endian.PutUint64(lastMsgSeqBytes, conversation.LastMsgSeq)
	if err = w.Set(key.NewConversationColumnKey(uid, id, key.TableConversation.Column.LastMsgSeq), lastMsgSeqBytes, wk.wo); err != nil {
		return err
	}

	// lastMsgClientNo
	if err = w.Set(key.NewConversationColumnKey(uid, id, key.TableConversation.Column.LastMsgClientNo), []byte(conversation.LastClientMsgNo), wk.wo); err != nil {
		return err
	}

	// lastMsgId
	var lastMsgIdBytes = make([]byte, 8)
	wk.endian.PutUint64(lastMsgIdBytes, uint64(conversation.LastMsgID))
	if err = w.Set(key.NewConversationColumnKey(uid, id, key.TableConversation.Column.LastMsgId), lastMsgIdBytes, wk.wo); err != nil {
		return err
	}

	// version
	var versionBytes = make([]byte, 8)
	wk.endian.PutUint64(versionBytes, uint64(conversation.Version))
	if err = w.Set(key.NewConversationColumnKey(uid, id, key.TableConversation.Column.Version), versionBytes, wk.wo); err != nil {
		return err
	}

	// channel index
	idBytes := make([]byte, 8)
	wk.endian.PutUint64(idBytes, id)
	if err = w.Set(key.NewConversationIndexChannelKey(uid, conversation.ChannelId, conversation.ChannelType), idBytes, wk.wo); err != nil {
		return err
	}

	// timestamp second index
	if err = w.Set(key.NewConversationSecondIndexTimestampKey(uid, uint64(conversation.Timestamp), id), nil, wk.wo); err != nil {
		return err
	}
	return nil
}

func (wk *wukongDB) parseConversations(iter *pebble.Iterator, limit int) ([]Conversation, error) {
	var (
		conversations   = make([]Conversation, 0, limit)
		preId           uint64
		preConversation Conversation
		lastNeedAppend  bool = true
		hasData         bool = false
	)

	for iter.First(); iter.Valid(); iter.Next() {

		id, coulmnName, err := key.ParseConversationColumnKey(iter.Key())
		if err != nil {
			return nil, err
		}
		if preId != id {
			if preId != 0 {
				conversations = append(conversations, preConversation)
				if len(conversations) >= limit {
					lastNeedAppend = false
					break
				}
			}

			preId = id
			preConversation = Conversation{}
		}
		switch coulmnName {
		case key.TableConversation.Column.Uid:
			preConversation.UID = string(iter.Value())
		case key.TableConversation.Column.ChannelId:
			preConversation.ChannelId = string(iter.Value())
		case key.TableConversation.Column.ChannelType:
			preConversation.ChannelType = uint8(iter.Value()[0])
		case key.TableConversation.Column.UnreadCount:
			preConversation.UnreadCount = wk.endian.Uint32(iter.Value())
		case key.TableConversation.Column.Timestamp:
			preConversation.Timestamp = int64(wk.endian.Uint64(iter.Value()))
		case key.TableConversation.Column.LastMsgSeq:
			preConversation.LastMsgSeq = wk.endian.Uint64(iter.Value())
		case key.TableConversation.Column.LastMsgClientNo:
			preConversation.LastClientMsgNo = string(iter.Value())
		case key.TableConversation.Column.LastMsgId:
			preConversation.LastMsgID = int64(wk.endian.Uint64(iter.Value()))
		case key.TableConversation.Column.Version:
			preConversation.Version = int64(wk.endian.Uint64(iter.Value()))

		}
		hasData = true
	}
	if lastNeedAppend && hasData {
		conversations = append(conversations, preConversation)
	}

	return conversations, nil
}
