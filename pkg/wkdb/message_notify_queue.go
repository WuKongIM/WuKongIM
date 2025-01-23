package wkdb

import (
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb/key"
	"github.com/cockroachdb/pebble"
	"go.uber.org/zap"
)

// AppendMessageOfNotifyQueue 添加消息到通知队列
func (wk *wukongDB) AppendMessageOfNotifyQueue(messages []Message) error {

	wk.metrics.AppendMessageOfNotifyQueueAdd(1)

	batch := wk.defaultShardBatchDB().NewBatch()
	for _, msg := range messages {
		if err := wk.writeMessageOfNotifyQueue(msg, batch); err != nil {
			return err
		}
	}
	return batch.CommitWait()
}

// GetMessagesOfNotifyQueue 获取通知队列的消息
func (wk *wukongDB) GetMessagesOfNotifyQueue(count int) ([]Message, error) {

	wk.metrics.GetMessagesOfNotifyQueueAdd(1)

	iter := wk.defaultShardDB().NewIter(&pebble.IterOptions{
		LowerBound: key.NewMessageNotifyQueueKey(0),
		UpperBound: key.NewMessageNotifyQueueKey(math.MaxUint64),
	})
	defer iter.Close()

	messages, errorKeys, err := wk.parseMessageOfNotifyQueue(iter, count)
	if err != nil {
		return nil, err
	}

	if len(errorKeys) > 0 {
		batch := wk.defaultShardDB().NewBatch()
		defer batch.Close()
		for _, key := range errorKeys {
			if err := batch.Delete(key, wk.noSync); err != nil {
				return nil, err
			}
		}
		if err := batch.Commit(wk.sync); err != nil {
			return nil, err
		}
	}
	return messages, nil
}

// RemoveMessagesOfNotifyQueueCount 移除指定数量的通知队列的消息
func (wk *wukongDB) RemoveMessagesOfNotifyQueueCount(count int) error {

	iter := wk.defaultShardDB().NewIter(&pebble.IterOptions{
		LowerBound: key.NewMessageNotifyQueueKey(0),
		UpperBound: key.NewMessageNotifyQueueKey(math.MaxUint64),
	})
	defer iter.Close()

	batch := wk.defaultShardDB().NewBatch()
	defer batch.Close()

	for i := 0; iter.First() && i < count; i++ {
		if err := batch.Delete(iter.Key(), wk.noSync); err != nil {
			return err
		}
		iter.Next()
	}
	return batch.Commit(wk.sync)
}

// RemoveMessagesOfNotifyQueue 移除通知队列的消息
func (wk *wukongDB) RemoveMessagesOfNotifyQueue(messageIDs []int64) error {

	wk.metrics.RemoveMessagesOfNotifyQueueAdd(1)

	batch := wk.defaultShardDB().NewBatch()
	defer batch.Close()

	for _, messageID := range messageIDs {
		if err := batch.Delete(key.NewMessageNotifyQueueKey(uint64(messageID)), wk.noSync); err != nil {
			return err
		}
	}
	return batch.Commit(wk.sync)
}

func (wk *wukongDB) writeMessageOfNotifyQueue(msg Message, w *Batch) error {
	data, err := msg.Marshal()
	if err != nil {
		return err
	}
	w.Set(key.NewMessageNotifyQueueKey(uint64(msg.MessageID)), data)
	return nil
}

func (wk *wukongDB) parseMessageOfNotifyQueue(iter *pebble.Iterator, limit int) ([]Message, [][]byte, error) {

	msgs := make([]Message, 0, limit)
	errorKeys := make([][]byte, 0, limit)
	for iter.First(); iter.Valid(); iter.Next() {
		value := iter.Value()
		// 解析消息
		var msg Message
		if err := msg.Unmarshal(value); err != nil {
			wk.Warn("queue message unmarshal failed", zap.Error(err), zap.Int("len", len(value)))
			errorKeys = append(errorKeys, iter.Key())
			continue
		}
		msgs = append(msgs, msg)
	}

	return msgs, errorKeys, nil
}
