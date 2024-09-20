package wkdb

import (
	"math"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb/key"
	"github.com/cockroachdb/pebble"
	"go.uber.org/zap"
)

func (wk *wukongDB) AddOrUpdateConversations(uid string, conversations []Conversation) error {

	// wk.dblock.conversationLock.lock(uid)
	// defer wk.dblock.conversationLock.unlock(uid)
	if wk.opts.EnableCost {
		start := time.Now()
		defer func() {
			end := time.Since(start)
			if end > time.Millisecond*500 {
				wk.Info("AddOrUpdateConversations cost too long", zap.Duration("cost", end), zap.String("uid", uid), zap.Int("conversations", len(conversations)))
			}
		}()
	}

	batch := wk.shardDB(uid).NewBatch()
	defer batch.Close()

	for _, cn := range conversations {
		oldConversation, err := wk.GetConversation(uid, cn.ChannelId, cn.ChannelType)
		if err != nil && err != ErrNotFound {
			return err
		}

		exist := !IsEmptyConversation(oldConversation)

		// 如果会话存在 则删除旧的索引
		if exist {
			oldConversation.CreatedAt = nil
			err = wk.deleteConversationIndex(oldConversation, batch)
			if err != nil {
				return err
			}
			cn.Id = oldConversation.Id
		}

		if exist {
			cn.CreatedAt = nil // 更新时不更新创建时间
		}

		if err := wk.writeConversation(cn, batch); err != nil {
			return err
		}
	}

	// err := wk.IncConversationCount(createCount)
	// if err != nil {
	// 	return err
	// }

	return batch.Commit(wk.sync)
}

// GetConversations 获取指定用户的最近会话
func (wk *wukongDB) GetConversations(uid string) ([]Conversation, error) {

	db := wk.shardDB(uid)
	iter := db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewConversationPrimaryKey(uid, 0),
		UpperBound: key.NewConversationPrimaryKey(uid, math.MaxUint64),
	})
	defer iter.Close()

	var conversations []Conversation
	err := wk.iterateConversation(iter, func(conversation Conversation) bool {
		conversations = append(conversations, conversation)
		return true
	})
	if err != nil {
		return nil, err
	}
	return conversations, nil
}

func (wk *wukongDB) GetConversationsByType(uid string, tp ConversationType) ([]Conversation, error) {

	db := wk.shardDB(uid)
	iter := db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewConversationPrimaryKey(uid, 0),
		UpperBound: key.NewConversationPrimaryKey(uid, math.MaxUint64),
	})
	defer iter.Close()

	var conversations []Conversation
	err := wk.iterateConversation(iter, func(conversation Conversation) bool {
		if conversation.Type == tp {
			conversations = append(conversations, conversation)
		}
		return true
	})
	if err != nil {
		return nil, err
	}
	return conversations, nil
}

func (wk *wukongDB) GetLastConversations(uid string, tp ConversationType, updatedAt uint64, limit int) ([]Conversation, error) {
	ids, err := wk.getLastConversationIds(uid, updatedAt, limit)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	conversations := make([]Conversation, 0, len(ids))

	for _, id := range ids {
		conversation, err := wk.getConversation(uid, id)
		if err != nil {
			return nil, err
		}
		if conversation.Type != tp {
			continue
		}
		conversations = append(conversations, conversation)
	}
	return conversations, nil
}

func (wk *wukongDB) getLastConversationIds(uid string, updatedAt uint64, limit int) ([]uint64, error) {
	db := wk.shardDB(uid)
	iter := db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewConversationSecondIndexKey(uid, key.TableConversation.SecondIndex.UpdatedAt, updatedAt, 0),
		UpperBound: key.NewConversationSecondIndexKey(uid, key.TableConversation.SecondIndex.UpdatedAt, math.MaxUint64, math.MaxUint64),
	})
	defer iter.Close()

	var (
		ids = make([]uint64, 0)
	)

	for iter.Last(); iter.Valid(); iter.Prev() {
		id, _, _, err := key.ParseConversationSecondIndexKey(iter.Key())
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
		if limit > 0 && len(ids) >= limit {
			break
		}
	}
	return ids, nil
}

// DeleteConversation 删除最近会话
func (wk *wukongDB) DeleteConversation(uid string, channelId string, channelType uint8) error {

	batch := wk.shardDB(uid).NewBatch()
	defer batch.Close()

	err := wk.deleteConversation(uid, channelId, channelType, batch)
	if err != nil {
		return err
	}

	return batch.Commit(wk.sync)

}

// DeleteConversations 批量删除最近会话
func (wk *wukongDB) DeleteConversations(uid string, channels []Channel) error {
	batch := wk.shardDB(uid).NewBatch()
	defer batch.Close()

	for _, channel := range channels {
		err := wk.deleteConversation(uid, channel.ChannelId, channel.ChannelType, batch)
		if err != nil {
			return err
		}
	}
	return batch.Commit(wk.sync)
}

func (wk *wukongDB) SearchConversation(req ConversationSearchReq) ([]Conversation, error) {
	if req.Uid != "" {
		return wk.GetConversations(req.Uid)
	}

	var conversations []Conversation
	currentSize := 0
	for _, db := range wk.dbs {
		iter := db.NewIter(&pebble.IterOptions{
			LowerBound: key.NewConversationUidHashKey(0),
			UpperBound: key.NewConversationUidHashKey(math.MaxUint64),
		})
		defer iter.Close()

		err := wk.iterateConversation(iter, func(conversation Conversation) bool {
			if currentSize > req.Limit*req.CurrentPage { // 大于当前页的消息终止遍历
				return false
			}
			currentSize++
			if currentSize > (req.CurrentPage-1)*req.Limit && currentSize <= req.CurrentPage*req.Limit {
				conversations = append(conversations, conversation)
				return true
			}
			return true
		})
		if err != nil {
			return nil, err
		}
	}
	return conversations, nil
}

func (wk *wukongDB) deleteConversation(uid string, channelId string, channelType uint8, w pebble.Writer) error {
	oldConversation, err := wk.GetConversation(uid, channelId, channelType)
	if err != nil && err != ErrNotFound {
		return err
	}
	if IsEmptyConversation(oldConversation) {
		return nil
	}
	// 删除索引
	err = wk.deleteConversationIndex(oldConversation, w)
	if err != nil {
		return err
	}

	// 删除数据
	err = w.DeleteRange(key.NewConversationColumnKey(uid, oldConversation.Id, key.MinColumnKey), key.NewConversationColumnKey(uid, oldConversation.Id, key.MaxColumnKey), wk.noSync)
	if err != nil {
		return err
	}
	return nil
}

// GetConversation 获取指定用户的指定会话
func (wk *wukongDB) GetConversation(uid string, channelId string, channelType uint8) (Conversation, error) {

	id, err := wk.getConversationByChannel(uid, channelId, channelType)
	if err != nil {
		return EmptyConversation, err
	}

	if id == 0 {
		return EmptyConversation, ErrNotFound
	}

	iter := wk.shardDB(uid).NewIter(&pebble.IterOptions{
		LowerBound: key.NewConversationColumnKey(uid, id, key.MinColumnKey),
		UpperBound: key.NewConversationColumnKey(uid, id, key.MaxColumnKey),
	})
	defer iter.Close()

	var conversation = EmptyConversation
	err = wk.iterateConversation(iter, func(cn Conversation) bool {
		conversation = cn
		return false
	})
	if err != nil {
		return EmptyConversation, err
	}

	if conversation == EmptyConversation {
		return EmptyConversation, ErrNotFound
	}

	return conversation, nil
}

func (wk *wukongDB) ExistConversation(uid string, channelId string, channelType uint8) (bool, error) {
	idBytes, closer, err := wk.shardDB(uid).Get(key.NewConversationIndexChannelKey(uid, channelId, channelType))
	if err != nil {
		if err == pebble.ErrNotFound {
			return false, nil
		}
		return false, err
	}
	defer closer.Close()

	if len(idBytes) == 0 {
		return false, nil
	}
	return true, nil
}

func (wk *wukongDB) getConversation(uid string, id uint64) (Conversation, error) {
	iter := wk.shardDB(uid).NewIter(&pebble.IterOptions{
		LowerBound: key.NewConversationColumnKey(uid, id, key.MinColumnKey),
		UpperBound: key.NewConversationColumnKey(uid, id, key.MaxColumnKey),
	})
	defer iter.Close()

	var conversation = EmptyConversation
	err := wk.iterateConversation(iter, func(cn Conversation) bool {
		conversation = cn
		return false
	})
	if err != nil {
		return EmptyConversation, err
	}

	if conversation == EmptyConversation {
		return EmptyConversation, ErrNotFound
	}

	return conversation, nil
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

// func (wk *wukongDB) updateOrAddReadedToMsgSeq(uid string, sessionId uint64, msgSeq uint64, w pebble.Writer) error {
// 	id, err := wk.getConversationIdBySession(uid, sessionId)
// 	if err != nil {
// 		return err
// 	}
// 	if id == 0 {
// 		id = uint64(wk.prmaryKeyGen.Generate().Int64())
// 		if err := wk.writeConversation(uint64(id), uid, Conversation{
// 			Id:             id,
// 			Uid:            uid,
// 			SessionId:      sessionId,
// 			UnreadCount:    0,
// 			ReadToMsgSeq: msgSeq,
// 		}, w); err != nil {
// 			return err
// 		}
// 	}
// 	var msgSeqBytes = make([]byte, 8)
// 	wk.endian.PutUint64(msgSeqBytes, msgSeq)
// 	return w.Set(key.NewConversationColumnKey(uid, id, key.TableConversation.Column.ReadToMsgSeq), msgSeqBytes, wk.noSync)
// }

func (wk *wukongDB) getConversationByChannel(uid string, channelId string, channelType uint8) (uint64, error) {
	idBytes, closer, err := wk.shardDB(uid).Get(key.NewConversationIndexChannelKey(uid, channelId, channelType))
	if err != nil {
		if err == pebble.ErrNotFound {
			return 0, nil
		}
		return 0, err
	}
	defer closer.Close()
	return wk.endian.Uint64(idBytes), nil

	// builder := strings.Builder{}
	// builder.WriteString(uid)
	// builder.WriteString(channelId)
	// builder.WriteByte(channelType)

	// return key.HashWithString(builder.String()), nil

}

func (wk *wukongDB) writeConversation(conversation Conversation, w pebble.Writer) error {
	var (
		err error
	)

	id := conversation.Id
	uid := conversation.Uid
	// uid
	if err = w.Set(key.NewConversationColumnKey(uid, id, key.TableConversation.Column.Uid), []byte(uid), wk.noSync); err != nil {
		return err
	}

	// channelId
	if err = w.Set(key.NewConversationColumnKey(uid, id, key.TableConversation.Column.ChannelId), []byte(conversation.ChannelId), wk.noSync); err != nil {
		return err
	}

	// channelType
	if err = w.Set(key.NewConversationColumnKey(uid, id, key.TableConversation.Column.ChannelType), []byte{conversation.ChannelType}, wk.noSync); err != nil {
		return err
	}

	// type
	if err = w.Set(key.NewConversationColumnKey(uid, id, key.TableConversation.Column.Type), []byte{byte(conversation.Type)}, wk.noSync); err != nil {
		return err
	}

	// unreadCount
	var unreadCountBytes = make([]byte, 4)
	wk.endian.PutUint32(unreadCountBytes, conversation.UnreadCount)
	if err = w.Set(key.NewConversationColumnKey(uid, id, key.TableConversation.Column.UnreadCount), unreadCountBytes, wk.noSync); err != nil {
		return err
	}

	// readedToMsgSeq
	var msgSeqBytes = make([]byte, 8)
	wk.endian.PutUint64(msgSeqBytes, conversation.ReadToMsgSeq)
	if err = w.Set(key.NewConversationColumnKey(uid, id, key.TableConversation.Column.ReadedToMsgSeq), msgSeqBytes, wk.noSync); err != nil {
		return err
	}

	// createdAt
	if conversation.CreatedAt != nil {
		createdAtBytes := make([]byte, 8)
		createdAt := uint64(conversation.CreatedAt.UnixNano())
		wk.endian.PutUint64(createdAtBytes, createdAt)
		if err = w.Set(key.NewConversationColumnKey(uid, id, key.TableConversation.Column.CreatedAt), createdAtBytes, wk.noSync); err != nil {
			return err
		}
	}

	if conversation.UpdatedAt != nil {
		// updatedAt
		updatedAtBytes := make([]byte, 8)
		updatedAt := uint64(conversation.UpdatedAt.UnixNano())
		wk.endian.PutUint64(updatedAtBytes, updatedAt)
		if err = w.Set(key.NewConversationColumnKey(uid, id, key.TableConversation.Column.UpdatedAt), updatedAtBytes, wk.noSync); err != nil {
			return err
		}
	}

	// write index
	if err = wk.writeConversationIndex(conversation, w); err != nil {
		return err
	}

	return nil
}

func (wk *wukongDB) writeConversationIndex(conversation Conversation, w pebble.Writer) error {

	idBytes := make([]byte, 8)
	wk.endian.PutUint64(idBytes, conversation.Id)

	// channel index
	if err := w.Set(key.NewConversationIndexChannelKey(conversation.Uid, conversation.ChannelId, conversation.ChannelType), idBytes, wk.noSync); err != nil {
		return err
	}

	//  type second index
	if err := w.Set(key.NewConversationSecondIndexKey(conversation.Uid, key.TableConversation.SecondIndex.Type, uint64(conversation.Type), conversation.Id), nil, wk.noSync); err != nil {
		return err
	}

	if conversation.CreatedAt != nil {
		// createdAt second index
		if err := w.Set(key.NewConversationSecondIndexKey(conversation.Uid, key.TableConversation.SecondIndex.CreatedAt, uint64(conversation.CreatedAt.UnixNano()), conversation.Id), nil, wk.noSync); err != nil {
			return err
		}
	}

	if conversation.UpdatedAt != nil {
		// updatedAt second index
		if err := w.Set(key.NewConversationSecondIndexKey(conversation.Uid, key.TableConversation.SecondIndex.UpdatedAt, uint64(conversation.UpdatedAt.UnixNano()), conversation.Id), nil, wk.noSync); err != nil {
			return err
		}
	}

	return nil
}

func (wk *wukongDB) deleteConversationIndex(conversation Conversation, w pebble.Writer) error {
	// channel index
	if err := w.Delete(key.NewConversationIndexChannelKey(conversation.Uid, conversation.ChannelId, conversation.ChannelType), wk.noSync); err != nil {
		return err
	}

	// type second index
	if err := w.Delete(key.NewConversationSecondIndexKey(conversation.Uid, key.TableConversation.SecondIndex.Type, uint64(conversation.Type), conversation.Id), wk.noSync); err != nil {
		return err
	}

	if conversation.CreatedAt != nil {
		// createdAt second index
		if err := w.Delete(key.NewConversationSecondIndexKey(conversation.Uid, key.TableConversation.SecondIndex.CreatedAt, uint64(conversation.CreatedAt.UnixNano()), conversation.Id), wk.noSync); err != nil {
			return err
		}
	}

	if conversation.UpdatedAt != nil {
		// updatedAt second index
		if err := w.Delete(key.NewConversationSecondIndexKey(conversation.Uid, key.TableConversation.SecondIndex.UpdatedAt, uint64(conversation.UpdatedAt.UnixNano()), conversation.Id), wk.noSync); err != nil {
			return err
		}
	}

	return nil
}

func (wk *wukongDB) iterateConversation(iter *pebble.Iterator, iterFnc func(conversation Conversation) bool) error {
	var (
		preId           uint64
		preConversation Conversation
		lastNeedAppend  bool = true
		hasData         bool = false
	)

	for iter.First(); iter.Valid(); iter.Next() {

		id, columnName, err := key.ParseConversationColumnKey(iter.Key())
		if err != nil {
			return err
		}
		if preId != id {
			if preId != 0 {
				if !iterFnc(preConversation) {
					lastNeedAppend = false
					break
				}
			}

			preId = id
			preConversation = Conversation{
				Id: id,
			}
		}
		switch columnName {
		case key.TableConversation.Column.Uid:
			preConversation.Uid = string(iter.Value())
		case key.TableConversation.Column.Type:
			preConversation.Type = ConversationType(iter.Value()[0])
		case key.TableConversation.Column.ChannelId:
			preConversation.ChannelId = string(iter.Value())
		case key.TableConversation.Column.ChannelType:
			preConversation.ChannelType = iter.Value()[0]
		case key.TableConversation.Column.UnreadCount:
			preConversation.UnreadCount = wk.endian.Uint32(iter.Value())
		case key.TableConversation.Column.ReadedToMsgSeq:
			preConversation.ReadToMsgSeq = wk.endian.Uint64(iter.Value())
		case key.TableConversation.Column.CreatedAt:
			tm := int64(wk.endian.Uint64(iter.Value()))
			if tm > 0 {
				t := time.Unix(tm/1e9, tm%1e9)
				preConversation.CreatedAt = &t
			}

		case key.TableConversation.Column.UpdatedAt:
			tm := int64(wk.endian.Uint64(iter.Value()))
			if tm > 0 {
				t := time.Unix(tm/1e9, tm%1e9)
				preConversation.UpdatedAt = &t
			}

		}
		hasData = true
	}
	if lastNeedAppend && hasData {
		_ = iterFnc(preConversation)
	}

	return nil
}

// func (wk *wukongDB) parseConversations(iter *pebble.Iterator, limit int) ([]Conversation, error) {
// 	var (
// 		conversations   = make([]Conversation, 0)
// 		preId           uint64
// 		preConversation Conversation
// 		lastNeedAppend  bool = true
// 		hasData         bool = false
// 	)

// 	for iter.First(); iter.Valid(); iter.Next() {

// 		id, coulmnName, err := key.ParseConversationColumnKey(iter.Key())
// 		if err != nil {
// 			return nil, err
// 		}
// 		if preId != id {
// 			if preId != 0 {
// 				conversations = append(conversations, preConversation)
// 				if limit > 0 && len(conversations) >= limit {
// 					lastNeedAppend = false
// 					break
// 				}
// 			}

// 			preId = id
// 			preConversation = Conversation{
// 				Id: id,
// 			}
// 		}
// 		switch coulmnName {
// 		case key.TableConversation.Column.Uid:
// 			preConversation.Uid = string(iter.Value())
// 		case key.TableConversation.Column.SessionId:
// 			preConversation.SessionId = wk.endian.Uint64(iter.Value())
// 		case key.TableConversation.Column.UnreadCount:
// 			preConversation.UnreadCount = wk.endian.Uint32(iter.Value())
// 		case key.TableConversation.Column.ReadToMsgSeq:
// 			preConversation.ReadToMsgSeq = wk.endian.Uint64(iter.Value())

// 		}
// 		hasData = true
// 	}
// 	if lastNeedAppend && hasData {
// 		conversations = append(conversations, preConversation)
// 	}

// 	return conversations, nil
// }
