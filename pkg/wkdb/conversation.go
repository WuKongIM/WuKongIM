package wkdb

import (
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb/key"
	"github.com/cockroachdb/pebble"
)

func (wk *wukongDB) AddOrUpdateConversations(uid string, conversations []Conversation) error {
	batch := wk.shardDB(uid).NewBatch()
	defer batch.Close()
	for _, cn := range conversations {

		id, err := wk.getConversationIdBySession(uid, cn.SessionId)
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

	db := wk.shardDB(uid)
	iter := db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewConversationPrimaryKey(uid, 0),
		UpperBound: key.NewConversationPrimaryKey(uid, math.MaxUint64),
	})
	defer iter.Close()
	conversations, err := wk.parseConversations(iter, 0)
	if err != nil {
		return nil, err
	}
	return conversations, nil
}

// DeleteConversation 删除最近会话
func (wk *wukongDB) DeleteConversation(uid string, sessionId uint64) error {

	batch := wk.shardDB(uid).NewBatch()
	defer batch.Close()

	err := wk.deleteConversation(uid, sessionId, batch)
	if err != nil {
		return err
	}
	return batch.Commit(wk.wo)

}

func (wk *wukongDB) deleteConversation(uid string, sessionId uint64, w pebble.Writer) error {
	id, err := wk.getConversationIdBySession(uid, sessionId)
	if err != nil {
		return err
	}
	if id == 0 {
		return nil
	}
	// 删除索引
	err = w.Delete(key.NewConversationIndexSessionIdKey(uid, sessionId), wk.noSync)
	if err != nil {
		return err
	}

	// 删除数据
	err = w.DeleteRange(key.NewConversationColumnKey(uid, id, [2]byte{0x00, 0x00}), key.NewConversationColumnKey(uid, id, [2]byte{0xff, 0xff}), wk.noSync)
	if err != nil {
		return err
	}
	return nil
}

// GetConversation 获取指定用户的指定会话
func (wk *wukongDB) GetConversation(uid string, sessionId uint64) (Conversation, error) {

	id, err := wk.getConversationIdBySession(uid, sessionId)
	if err != nil {
		return EmptyConversation, err
	}

	if id == 0 {
		return EmptyConversation, nil
	}

	iter := wk.shardDB(uid).NewIter(&pebble.IterOptions{
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

func (wk *wukongDB) GetConversationBySessionIds(uid string, sessionIds []uint64) ([]Conversation, error) {
	if len(sessionIds) == 0 {
		return nil, nil
	}
	db := wk.shardDB(uid)

	conversations := make([]Conversation, 0)

	for _, sessionId := range sessionIds {
		id, err := wk.getConversationIdBySession(uid, sessionId)
		if err != nil {
			return nil, err
		}

		if id == 0 {
			continue
		}

		iter := db.NewIter(&pebble.IterOptions{
			LowerBound: key.NewConversationColumnKey(uid, id, [2]byte{0x00, 0x00}),
			UpperBound: key.NewConversationColumnKey(uid, id, [2]byte{0xff, 0xff}),
		})
		defer iter.Close()

		cns, err := wk.parseConversations(iter, 1)
		if err != nil {
			return nil, err
		}
		if len(cns) > 0 {
			conversations = append(conversations, cns[0])
		}
	}

	return conversations, nil
}

// func (wk *wukongDB) getConversationIdsByUid(uid string) ([]uint64, error) {
// 	iter := wk.shardDB(uid).NewIter(&pebble.IterOptions{
// 		LowerBound: key.NewConversationPrimaryKey(uid, 0),
// 		UpperBound: key.NewConversationPrimaryKey(uid, math.MaxUint64),
// 	})
// 	defer iter.Close()

// 	ids := make([]uint64, 0)

// 	for iter.Last(); iter.Valid(); iter.Prev() {
// 		_, id, err := key.ParseConversationSecondIndexTimestampKey(iter.Key())
// 		if err != nil {
// 			return nil, err
// 		}
// 		ids = append(ids, id)
// 	}
// 	return ids, nil
// }

func (wk *wukongDB) getConversationIdBySession(uid string, sessionId uint64) (uint64, error) {
	idBytes, closer, err := wk.shardDB(uid).Get(key.NewConversationIndexSessionIdKey(uid, sessionId))
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

	// sessionId
	sessionIdBytes := make([]byte, 8)
	wk.endian.PutUint64(sessionIdBytes, conversation.SessionId)
	if err = w.Set(key.NewConversationColumnKey(uid, id, key.TableConversation.Column.SessionId), sessionIdBytes, wk.wo); err != nil {
		return err
	}

	// unreadCount
	var unreadCountBytes = make([]byte, 4)
	wk.endian.PutUint32(unreadCountBytes, conversation.UnreadCount)
	if err = w.Set(key.NewConversationColumnKey(uid, id, key.TableConversation.Column.UnreadCount), unreadCountBytes, wk.wo); err != nil {
		return err
	}

	// readedToMsgSeq
	var msgSeqBytes = make([]byte, 8)
	wk.endian.PutUint64(msgSeqBytes, conversation.ReadedToMsgSeq)
	if err = w.Set(key.NewConversationColumnKey(uid, id, key.TableConversation.Column.ReadedToMsgSeq), msgSeqBytes, wk.wo); err != nil {
		return err
	}

	// session index
	idBytes := make([]byte, 8)
	wk.endian.PutUint64(idBytes, id)
	if err = w.Set(key.NewConversationIndexSessionIdKey(uid, conversation.SessionId), idBytes, wk.wo); err != nil {
		return err
	}

	return nil
}

func (wk *wukongDB) parseConversations(iter *pebble.Iterator, limit int) ([]Conversation, error) {
	var (
		conversations   = make([]Conversation, 0)
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
				if limit > 0 && len(conversations) >= limit {
					lastNeedAppend = false
					break
				}
			}

			preId = id
			preConversation = Conversation{
				Id: id,
			}
		}
		switch coulmnName {
		case key.TableConversation.Column.Uid:
			preConversation.Uid = string(iter.Value())
		case key.TableConversation.Column.SessionId:
			preConversation.SessionId = wk.endian.Uint64(iter.Value())
		case key.TableConversation.Column.UnreadCount:
			preConversation.UnreadCount = wk.endian.Uint32(iter.Value())
		case key.TableConversation.Column.ReadedToMsgSeq:
			preConversation.ReadedToMsgSeq = wk.endian.Uint64(iter.Value())

		}
		hasData = true
	}
	if lastNeedAppend && hasData {
		conversations = append(conversations, preConversation)
	}

	return conversations, nil
}
