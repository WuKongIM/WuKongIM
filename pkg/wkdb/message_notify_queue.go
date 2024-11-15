package wkdb

import (
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb/key"
	"github.com/cockroachdb/pebble"
)

// AppendMessageOfNotifyQueue 添加消息到通知队列
func (wk *wukongDB) AppendMessageOfNotifyQueue(messages []Message) error {

	wk.metrics.AppendMessageOfNotifyQueueAdd(1)

	batch := wk.defaultShardDB().NewBatch()
	defer batch.Close()
	for _, msg := range messages {
		if err := wk.writeMessageOfNotifyQueue(msg, batch); err != nil {
			return err
		}
	}
	return batch.Commit(wk.sync)
}

// GetMessagesOfNotifyQueue 获取通知队列的消息
func (wk *wukongDB) GetMessagesOfNotifyQueue(count int) ([]Message, error) {

	wk.metrics.GetMessagesOfNotifyQueueAdd(1)

	iter := wk.defaultShardDB().NewIter(&pebble.IterOptions{
		LowerBound: key.NewMessageNotifyQueueKey(0),
		UpperBound: key.NewMessageNotifyQueueKey(math.MaxUint64),
	})
	defer iter.Close()

	return wk.parseMessageOfNotifyQueue(iter, count)
}

// RemoveMessagesOfNotifyQueue 移除通知队列的消息
func (wk *wukongDB) RemoveMessagesOfNotifyQueue(messageIDs []int64) error {

	wk.metrics.RemoveMessagesOfNotifyQueueAdd(1)

	batch := wk.defaultShardDB().NewBatch()
	defer batch.Close()

	for _, messageID := range messageIDs {
		if err := batch.Delete(key.NewMessageNotifyQueueKey(uint64(messageID)), wk.sync); err != nil {
			return err
		}
	}
	return batch.Commit(wk.sync)
}

func (wk *wukongDB) writeMessageOfNotifyQueue(msg Message, w *pebble.Batch) error {
	data, err := msg.Marshal()
	if err != nil {
		return err
	}
	return w.Set(key.NewMessageNotifyQueueKey(uint64(msg.MessageID)), data, wk.sync)
}

func (wk *wukongDB) parseMessageOfNotifyQueue(iter *pebble.Iterator, limit int) ([]Message, error) {

	msgs := make([]Message, 0, limit)
	for iter.First(); iter.Valid(); iter.Next() {
		value := iter.Value()
		// 解析消息
		var msg Message
		if err := msg.Unmarshal(value); err != nil {
			return nil, err
		}
		msgs = append(msgs, msg)
	}

	return msgs, nil
}
