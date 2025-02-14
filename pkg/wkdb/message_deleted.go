package wkdb

import (
	"github.com/WuKongIM/WuKongIM/pkg/wkdb/key"
	"github.com/cockroachdb/pebble"
)

// 新增或者修改一条渠道删除记录
func (wk *wukongDB) AddOrUpdateMessageDeleted(messageDeleted MessageDeleted) error {

	batch := wk.defaultShardBatchDB().NewBatch()
	primaryKey, err := wk.getChannelPrimaryKey(messageDeleted.ChannelId, messageDeleted.ChannelType)
	if err != nil {
		return err
	}
	messageDeleted.Id = primaryKey
	if err := wk.writeMessageDeleted(batch, messageDeleted); err != nil {
		return err
	}
	return batch.CommitWait()
}

// GetMessageDeletedSeq 根据channelId和channelType获取最大的删除消息序号
func (wk *wukongDB) GetMessageDeletedSeq(channelId string, channelType uint8) uint64 {
	id, err := wk.getChannelPrimaryKey(channelId, channelType)
	if err != nil {
		return 0
	}
	db := wk.defaultShardDB()
	iter := db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewMessageDeletedColumnKey(id, key.MinColumnKey),
		UpperBound: key.NewMessageDeletedColumnKey(id, key.MaxColumnKey),
	})
	defer iter.Close()

	var messageDeleted MessageDeleted
	err2 := wk.iteratorMessageDeleted(iter, func(t MessageDeleted) bool {
		messageDeleted = t
		return false
	})
	if err2 != nil {
		return 0
	}
	return messageDeleted.MessageSeq
}

func (wk *wukongDB) iteratorMessageDeleted(iter *pebble.Iterator, iterFnc func(messageDeleted MessageDeleted) bool) error {
	var (
		preId             uint64
		preMessageDeleted MessageDeleted
		lastNeedAppend    bool = true
		hasData           bool = false
	)

	for iter.First(); iter.Valid(); iter.Next() {
		primaryKey, columnName, err := key.ParseMessageDeletedColumnKey(iter.Key())
		if err != nil {
			return err
		}

		if preId != primaryKey {
			if preId != 0 {
				if !iterFnc(preMessageDeleted) {
					lastNeedAppend = false
					break
				}
			}
			preId = primaryKey
			preMessageDeleted = MessageDeleted{Id: primaryKey}
		}

		switch columnName {
		case key.TableMessageDeleted.Column.ChannelId:
			preMessageDeleted.ChannelId = string(iter.Value())
		case key.TableMessageDeleted.Column.ChannelType:
			preMessageDeleted.ChannelType = iter.Value()[0]
		case key.TableMessageDeleted.Column.MessageSeq:
			preMessageDeleted.MessageSeq = wk.endian.Uint64(iter.Value())
		}
		lastNeedAppend = true
		hasData = true
	}

	if lastNeedAppend && hasData {
		_ = iterFnc(preMessageDeleted)
	}
	return nil
}

// writeMessageDeleted
func (wk *wukongDB) writeMessageDeleted(w *Batch, messageDeleted MessageDeleted) error {
	w.Set(key.NewMessageDeletedColumnKey(messageDeleted.Id, key.TableMessage.Column.ChannelId), []byte(messageDeleted.ChannelId))

	// channelType
	channelTypeBytes := make([]byte, 1)
	channelTypeBytes[0] = messageDeleted.ChannelType
	w.Set(key.NewMessageDeletedColumnKey(messageDeleted.Id, key.TableMessageDeleted.Column.ChannelType), channelTypeBytes)

	// messageSeq
	messageSeqBytes := make([]byte, 8)
	wk.endian.PutUint64(messageSeqBytes, messageDeleted.MessageSeq)
	w.Set(key.NewMessageDeletedColumnKey(messageDeleted.Id, key.TableMessageDeleted.Column.MessageSeq), messageSeqBytes)
	return nil
}
