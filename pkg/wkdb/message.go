package wkdb

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb/key"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/cockroachdb/pebble"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

func (wk *wukongDB) AppendMessages(channelId string, channelType uint8, msgs []Message) error {

	wk.metrics.AppendMessagesAdd(1)

	if wk.opts.EnableCost {
		start := time.Now()
		defer func() {
			cost := time.Since(start)
			if cost.Milliseconds() > 1000 {
				wk.Info("appendMessages done", zap.Duration("cost", cost), zap.String("channelId", channelId), zap.Uint8("channelType", channelType), zap.Int("msgCount", len(msgs)))
			}
		}()
	}

	batch := wk.channelBatchDb(channelId, channelType).NewBatch()
	for _, msg := range msgs {
		if err := wk.writeMessage(channelId, channelType, msg, batch); err != nil {
			return err
		}

	}
	lastMsg := msgs[len(msgs)-1]
	err := wk.setChannelLastMessageSeq(channelId, channelType, uint64(lastMsg.MessageSeq), batch)
	if err != nil {
		return err
	}

	return batch.CommitWait()
}

// func (wk *wukongDB) AppendMessagesByLogs(reqs []reactor.AppendLogReq) {
// 	batchMap := make(map[uint32]*Batch)
// 	newBatchs := make([]*Batch, 0, len(reqs))

// 	for _, req := range reqs {

// 		channelId, channelType := wkutil.ChannelFromlKey(req.HandleKey)

// 		shardId := wk.channelDbIndex(channelId, channelType)

// 		batch := batchMap[shardId]
// 		if batch == nil {
// 			batch = wk.shardBatchDBById(shardId).NewBatch()
// 			batchMap[shardId] = batch
// 			newBatchs = append(newBatchs, batch)
// 		}

// 		for _, log := range req.Logs {
// 			msg := Message{}
// 			err := msg.Unmarshal(log.Data)
// 			if err != nil {
// 				wk.Panic("message unmarshal failed", zap.Error(err))
// 				return
// 			}
// 			msg.MessageSeq = uint32(log.Index)
// 			msg.Term = uint64(log.Term)

// 			if err := wk.writeMessage(channelId, channelType, msg, batch); err != nil {
// 				wk.Panic("write message failed", zap.Error(err))
// 				return
// 			}
// 			err = wk.setChannelLastMessageSeq(channelId, channelType, uint64(msg.MessageSeq), batch)
// 			if err != nil {
// 				wk.Panic("setChannelLastMessageSeq failed", zap.Error(err))
// 				return
// 			}

// 			if len(batch.setKvs) > wk.opts.BatchPerSize {
// 				batch = wk.shardBatchDBById(shardId).NewBatch()
// 				batchMap[shardId] = batch
// 				newBatchs = append(newBatchs, batch)
// 			}
// 		}
// 	}

// 	err := Commits(newBatchs)
// 	if err != nil {
// 		wk.Error("AppendMessagesByLogs commits failed", zap.Error(err))
// 	}

// 	for _, req := range reqs {
// 		req.WaitC <- err
// 	}
// }

func (wk *wukongDB) channelDb(channelId string, channelType uint8) *pebble.DB {
	dbIndex := wk.channelDbIndex(channelId, channelType)
	return wk.shardDBById(uint32(dbIndex))
}

func (wk *wukongDB) channelBatchDb(channelId string, channelType uint8) *BatchDB {
	dbIndex := wk.channelDbIndex(channelId, channelType)
	return wk.shardBatchDBById(uint32(dbIndex))
}

func (wk *wukongDB) channelDbIndex(channelId string, channelType uint8) uint32 {
	return uint32(key.ChannelToNum(channelId, channelType) % uint64(len(wk.dbs)))
}

func (wk *wukongDB) AppendMessagesBatch(reqs []AppendMessagesReq) error {

	wk.metrics.AppendMessagesBatchAdd(1)

	if len(reqs) == 0 {
		return nil
	}

	if len(reqs) == 1 {
		req := reqs[0]
		return wk.AppendMessages(req.ChannelId, req.ChannelType, req.Messages)
	}

	// 监控
	trace.GlobalTrace.Metrics.DB().MessageAppendBatchCountAdd(1)

	if wk.opts.EnableCost {
		start := time.Now()
		defer func() {
			cost := time.Since(start)
			if cost > time.Millisecond*1000 {
				msgCount := 0
				for _, req := range reqs {
					msgCount += len(req.Messages)
				}
				wk.Info("appendMessagesBatch done", zap.Duration("cost", cost), zap.Int("reqs", len(reqs)), zap.Int("msgCount", msgCount))
			}
		}()
	}

	// 按照db进行分组
	dbMap := make(map[uint32][]AppendMessagesReq)
	var msgTotalCount int
	for _, req := range reqs {
		shardId := wk.channelDbIndex(req.ChannelId, req.ChannelType)
		dbMap[shardId] = append(dbMap[shardId], req)
		msgTotalCount += len(req.Messages)
	}

	batchs := make([]*Batch, 0, len(dbMap))

	for _, req := range reqs {
		batch := wk.channelBatchDb(req.ChannelId, req.ChannelType).NewBatch()
		for _, msg := range req.Messages {
			if err := wk.writeMessage(req.ChannelId, req.ChannelType, msg, batch); err != nil {
				return err
			}
		}
		err := wk.setChannelLastMessageSeq(req.ChannelId, req.ChannelType, uint64(req.Messages[len(req.Messages)-1].MessageSeq), batch)
		if err != nil {
			return err
		}
		batchs = append(batchs, batch)
	}
	// for shardId, reqs := range dbMap {
	// 	batch := wk.shardBatchDBById(shardId).NewBatch()
	// 	err := wk.writeMessagesBatch(batch, reqs)
	// 	if err != nil {
	// 		return err
	// 	}
	// 	batchs = append(batchs, batch)
	// }

	timeoutCtx, cancel := context.WithTimeout(wk.cancelCtx, time.Second*5)
	defer cancel()
	requestGroup, _ := errgroup.WithContext(timeoutCtx)

	for _, batch := range batchs {
		bt := batch
		requestGroup.Go(func() error {
			return bt.CommitWait()
		})
	}

	err := requestGroup.Wait()
	if err != nil {
		wk.Error("exec appendMessagesBatch failed", zap.Error(err), zap.Int("reqs", len(reqs)))
	}

	// // 消息总数量增加
	// err := wk.IncMessageCount(msgTotalCount)
	// if err != nil {
	// 	return err
	// }

	return nil

}

// func (wk *wukongDB) writeMessagesBatch(batch *Batch, reqs []AppendMessagesReq) error {
// 	for _, req := range reqs {
// 		lastMsg := req.Messages[len(req.Messages)-1]
// 		for _, msg := range req.Messages {
// 			if err := wk.writeMessage(req.ChannelId, req.ChannelType, msg, batch); err != nil {
// 				return err
// 			}
// 		}
// 		err := wk.setChannelLastMessageSeq(req.ChannelId, req.ChannelType, uint64(lastMsg.MessageSeq), batch)
// 		if err != nil {
// 			return err
// 		}
// 	}
// 	return nil
// }

func (wk *wukongDB) GetMessage(messageId uint64) (Message, error) {

	wk.metrics.GetMessageAdd(1)

	messageIdKey := key.NewMessageIndexMessageIdKey(messageId)

	for _, db := range wk.dbs {
		result, closer, err := db.Get(messageIdKey)
		if err != nil {
			if err == pebble.ErrNotFound {
				continue
			}
			return EmptyMessage, err
		}
		defer closer.Close()

		if len(result) != 16 {
			return EmptyMessage, fmt.Errorf("invalid message index key")
		}
		var arr [16]byte
		copy(arr[:], result)
		iter := db.NewIter(&pebble.IterOptions{
			LowerBound: key.NewMessageColumnKeyWithPrimary(arr, key.MinColumnKey),
			UpperBound: key.NewMessageColumnKeyWithPrimary(arr, key.MaxColumnKey),
		})
		defer iter.Close()

		var msg Message
		err = wk.iteratorChannelMessages(iter, 0, func(m Message) bool {
			msg = m
			return false
		})

		if err != nil {
			return EmptyMessage, err
		}
		if IsEmptyMessage(msg) {
			return EmptyMessage, ErrNotFound
		}
		return msg, nil
	}
	return EmptyMessage, ErrNotFound
}

// 情况1: startMessageSeq=100, endMessageSeq=0, limit=10 返回的消息seq为91-100的消息 (limit生效)
// 情况2: startMessageSeq=5, endMessageSeq=0, limit=10 返回的消息seq为1-5的消息（消息无）

// 情况3: startMessageSeq=100, endMessageSeq=95, limit=10 返回的消息seq为96-100的消息（endMessageSeq生效）
// 情况4: startMessageSeq=100, endMessageSeq=50, limit=10 返回的消息seq为91-100的消息（limit生效）
func (wk *wukongDB) LoadPrevRangeMsgs(channelId string, channelType uint8, startMessageSeq, endMessageSeq uint64, limit int) ([]Message, error) {

	wk.metrics.LoadPrevRangeMsgsAdd(1)

	if startMessageSeq == 0 {
		return nil, fmt.Errorf("start messageSeq[%d] must be greater than 0", startMessageSeq)

	}
	if endMessageSeq != 0 && endMessageSeq > startMessageSeq {
		return nil, fmt.Errorf("end messageSeq[%d] must be less than start messageSeq[%d]", endMessageSeq, startMessageSeq)
	}

	var minSeq uint64
	var maxSeq uint64

	if endMessageSeq == 0 {
		maxSeq = startMessageSeq + 1
		if startMessageSeq < uint64(limit) {
			minSeq = 1
		} else {
			minSeq = startMessageSeq - uint64(limit) + 1
		}
	} else {
		maxSeq = startMessageSeq + 1
		if startMessageSeq-endMessageSeq > uint64(limit) {
			minSeq = startMessageSeq - uint64(limit) + 1
		} else {
			minSeq = endMessageSeq + 1
		}

	}

	// 获取频道的最大的messageSeq，超过这个的消息都视为无效
	lastSeq, _, err := wk.GetChannelLastMessageSeq(channelId, channelType)
	if err != nil {
		return nil, err
	}

	if maxSeq > lastSeq {
		maxSeq = lastSeq + 1
	}

	db := wk.channelDb(channelId, channelType)

	iter := db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewMessagePrimaryKey(channelId, channelType, minSeq),
		UpperBound: key.NewMessagePrimaryKey(channelId, channelType, maxSeq),
	})
	defer iter.Close()

	msgs := make([]Message, 0)
	err = wk.iteratorChannelMessages(iter, limit, func(m Message) bool {
		msgs = append(msgs, m)
		return true
	})
	if err != nil {
		return nil, err
	}
	return msgs, nil
}

func (wk *wukongDB) LoadNextRangeMsgs(channelId string, channelType uint8, startMessageSeq, endMessageSeq uint64, limit int) ([]Message, error) {

	wk.metrics.LoadNextRangeMsgsAdd(1)

	minSeq := startMessageSeq
	maxSeq := endMessageSeq
	if endMessageSeq == 0 {
		maxSeq = math.MaxUint64
	}

	// 获取频道的最大的messageSeq，超过这个的消息都视为无效
	lastSeq, _, err := wk.GetChannelLastMessageSeq(channelId, channelType)
	if err != nil {
		return nil, err
	}

	if maxSeq > lastSeq {
		maxSeq = lastSeq + 1
	}

	db := wk.channelDb(channelId, channelType)

	iter := db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewMessagePrimaryKey(channelId, channelType, minSeq),
		UpperBound: key.NewMessagePrimaryKey(channelId, channelType, maxSeq),
	})
	defer iter.Close()

	msgs := make([]Message, 0)

	err = wk.iteratorChannelMessages(iter, limit, func(m Message) bool {
		msgs = append(msgs, m)
		return true
	})
	if err != nil {
		return nil, err
	}
	return msgs, nil

}

func (wk *wukongDB) LoadMsg(channelId string, channelType uint8, seq uint64) (Message, error) {

	wk.metrics.LoadMsgAdd(1)

	db := wk.channelDb(channelId, channelType)

	iter := db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewMessagePrimaryKey(channelId, channelType, seq),
		UpperBound: key.NewMessagePrimaryKey(channelId, channelType, seq+1),
	})
	defer iter.Close()
	var msg Message
	err := wk.iteratorChannelMessages(iter, 1, func(m Message) bool {
		msg = m
		return false
	})
	if err != nil {
		return EmptyMessage, err
	}
	if IsEmptyMessage(msg) {
		return EmptyMessage, ErrNotFound
	}
	return msg, nil

}

func (wk *wukongDB) LoadLastMsgs(channelId string, channelType uint8, limit int) ([]Message, error) {

	wk.metrics.LoadLastMsgsAdd(1)

	lastSeq, _, err := wk.GetChannelLastMessageSeq(channelId, channelType)
	if err != nil {
		return nil, err
	}
	if lastSeq == 0 {
		return nil, nil
	}
	return wk.LoadPrevRangeMsgs(channelId, channelType, lastSeq, 0, limit)

}

// 获取最新的一条消息
func (wk *wukongDB) GetLastMsg(channelId string, channelType uint8) (Message, error) {
	lastSeq, _, err := wk.GetChannelLastMessageSeq(channelId, channelType)
	if err != nil {
		return EmptyMessage, err
	}
	if lastSeq == 0 {
		return EmptyMessage, nil
	}
	return wk.LoadMsg(channelId, channelType, lastSeq)
}

func (wk *wukongDB) LoadLastMsgsWithEnd(channelID string, channelType uint8, endMessageSeq uint64, limit int) ([]Message, error) {

	wk.metrics.LoadLastMsgsWithEndAdd(1)

	lastSeq, _, err := wk.GetChannelLastMessageSeq(channelID, channelType)
	if err != nil {
		return nil, err
	}
	if lastSeq == 0 {
		return nil, nil
	}
	return wk.LoadPrevRangeMsgs(channelID, channelType, lastSeq, endMessageSeq, limit)
}

func (wk *wukongDB) LoadNextRangeMsgsForSize(channelId string, channelType uint8, startMessageSeq, endMessageSeq uint64, limitSize uint64) ([]Message, error) {

	wk.metrics.LoadNextRangeMsgsForSizeAdd(1)

	if wk.opts.EnableCost {
		start := time.Now()
		defer func() {
			cost := time.Since(start)
			if cost.Milliseconds() > 200 {
				wk.Info("loadNextRangeMsgsForSize done", zap.Duration("cost", time.Since(start)), zap.String("channelId", channelId), zap.Uint8("channelType", channelType), zap.Uint64("startMessageSeq", startMessageSeq), zap.Uint64("endMessageSeq", endMessageSeq))
			}
		}()
	}

	minSeq := startMessageSeq
	maxSeq := endMessageSeq

	if endMessageSeq == 0 {
		maxSeq = math.MaxUint64
	}
	db := wk.channelDb(channelId, channelType)

	iter := db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewMessagePrimaryKey(channelId, channelType, minSeq),
		UpperBound: key.NewMessagePrimaryKey(channelId, channelType, maxSeq),
	})
	defer iter.Close()
	return wk.parseChannelMessagesWithLimitSize(iter, limitSize)
}

func (wk *wukongDB) TruncateLogTo(channelId string, channelType uint8, messageSeq uint64) error {

	wk.metrics.TruncateLogToAdd(1)

	if messageSeq == 0 {
		return fmt.Errorf("messageSeq[%d] must be greater than 0", messageSeq)

	}

	// 获取最新的消息seq
	lastMsgSeq, _, err := wk.GetChannelLastMessageSeq(channelId, channelType)
	if err != nil {
		wk.Error("TruncateLogTo: getChannelLastMessageSeq", zap.Error(err))
		return err
	}

	if messageSeq >= lastMsgSeq {
		return nil
	}

	wk.Warn("truncateLogTo message", zap.Uint64("messageSeq", messageSeq), zap.Uint64("lastMsgSeq", lastMsgSeq), zap.String("channelId", channelId), zap.Uint8("channelType", channelType), zap.Uint64("messageSeq", messageSeq))

	if wk.opts.EnableCost {
		start := time.Now()
		defer func() {
			wk.Info("truncateLogTo done", zap.Duration("cost", time.Since(start)), zap.String("channelId", channelId), zap.Uint8("channelType", channelType), zap.Uint64("messageSeq", messageSeq))
		}()
	}

	db := wk.channelBatchDb(channelId, channelType)
	batch := db.NewBatch()
	batch.DeleteRange(key.NewMessagePrimaryKey(channelId, channelType, messageSeq+1), key.NewMessagePrimaryKey(channelId, channelType, math.MaxUint64))

	err = wk.setChannelLastMessageSeq(channelId, channelType, messageSeq, batch)
	if err != nil {
		return err
	}

	return batch.CommitWait()
}

func (wk *wukongDB) GetChannelLastMessageSeq(channelId string, channelType uint8) (uint64, uint64, error) {

	wk.metrics.GetChannelLastMessageSeqAdd(1)

	db := wk.channelDb(channelId, channelType)
	result, closer, err := db.Get(key.NewChannelLastMessageSeqKey(channelId, channelType))
	if err != nil {
		if err == pebble.ErrNotFound {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	defer closer.Close()

	seq := wk.endian.Uint64(result)
	setTime := wk.endian.Uint64(result[8:])

	return seq, setTime, nil
}

func (wk *wukongDB) SetChannelLastMessageSeq(channelId string, channelType uint8, seq uint64) error {

	wk.metrics.SetChannelLastMessageSeqAdd(1)

	if wk.opts.EnableCost {
		start := time.Now()
		defer func() {
			cost := time.Since(start)
			if cost.Milliseconds() > 200 {
				wk.Info("SetChannelLastMessageSeq done", zap.Duration("cost", cost), zap.String("channelId", channelId), zap.Uint8("channelType", channelType))
			}
		}()
	}
	batch := wk.channelBatchDb(channelId, channelType).NewBatch()
	err := wk.setChannelLastMessageSeq(channelId, channelType, seq, batch)
	if err != nil {
		return err
	}
	return batch.CommitWait()
}

// func (wk *wukongDB) SetChannellastMessageSeqBatch(reqs []SetChannelLastMessageSeqReq) error {
// 	if len(reqs) == 0 {
// 		return nil
// 	}
// 	if wk.opts.EnableCost {
// 		start := time.Now()
// 		defer func() {
// 			cost := time.Since(start)
// 			if cost.Milliseconds() > 200 {
// 				wk.Info("SetChannellastMessageSeqBatch done", zap.Duration("cost", cost), zap.Int("reqs", len(reqs)))
// 			}
// 		}()
// 	}
// 	// 按照db进行分组
// 	dbMap := make(map[uint32][]SetChannelLastMessageSeqReq)
// 	for _, req := range reqs {
// 		shardId := wk.channelDbIndex(req.ChannelId, req.ChannelType)
// 		dbMap[shardId] = append(dbMap[shardId], req)
// 	}

// 	for shardId, reqs := range dbMap {
// 		db := wk.shardBatchDBById(shardId)
// 		batch := db.NewBatch()
// 		for _, req := range reqs {
// 			if err := wk.setChannelLastMessageSeq(req.ChannelId, req.ChannelType, req.Seq, batch); err != nil {
// 				return err
// 			}
// 		}
// 		if err := batch.CommitWait(); err != nil {
// 			return err
// 		}
// 	}
// 	return nil
// }

var minMessagePrimaryKey = [16]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
var maxMessagePrimaryKey = [16]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}

func (wk *wukongDB) searchMessageByIndex(req MessageSearchReq, db *pebble.DB, iterFnc func(m Message) bool) (bool, error) {
	var lowKey []byte
	var highKey []byte

	var existKey = false

	if strings.TrimSpace(req.FromUid) != "" {

		lowKey = key.NewMessageSecondIndexFromUidKey(req.FromUid, minMessagePrimaryKey)
		highKey = key.NewMessageSecondIndexFromUidKey(req.FromUid, maxMessagePrimaryKey)
		existKey = true
	}

	// if req.MessageId > 0 && !existKey {
	// 	lowKey = key.NewMessageIndexMessageIdKey(uint64(req.MessageId), minMessagePrimaryKey)
	// 	highKey = key.NewMessageIndexMessageIdKey(uint64(req.MessageId), maxMessagePrimaryKey)
	// 	existKey = true
	// }

	if strings.TrimSpace(req.ClientMsgNo) != "" && !existKey {
		lowKey = key.NewMessageSecondIndexClientMsgNoKey(req.ClientMsgNo, minMessagePrimaryKey)
		highKey = key.NewMessageSecondIndexClientMsgNoKey(req.ClientMsgNo, maxMessagePrimaryKey)
		existKey = true
	}

	if !existKey {
		return false, nil
	}

	iter := db.NewIter(&pebble.IterOptions{
		LowerBound: lowKey,
		UpperBound: highKey,
	})
	defer iter.Close()

	for iter.Last(); iter.Valid(); iter.Prev() {
		primaryBytes, err := key.ParseMessageSecondIndexKey(iter.Key())
		if err != nil {
			wk.Error("parseMessageIndexKey", zap.Error(err))
			continue
		}

		iter := db.NewIter(&pebble.IterOptions{
			LowerBound: key.NewMessageColumnKeyWithPrimary(primaryBytes, key.MinColumnKey),
			UpperBound: key.NewMessageColumnKeyWithPrimary(primaryBytes, key.MaxColumnKey),
		})

		defer iter.Close()

		var msg Message
		err = wk.iteratorChannelMessages(iter, 0, func(m Message) bool {
			msg = m
			return false
		})
		if err != nil {
			return false, err
		}
		if iterFnc != nil {
			if !iterFnc(msg) {
				break
			}
		}
	}

	return true, nil

}

func (wk *wukongDB) SearchMessages(req MessageSearchReq) ([]Message, error) {

	wk.metrics.SearchMessagesAdd(1)

	if req.MessageId > 0 { // 如果指定了messageId，则直接查询messageId，这种情况要么没有要么只有一条
		msg, err := wk.GetMessage(uint64(req.MessageId))
		if err != nil {
			if err == ErrNotFound {
				return nil, nil
			}
			return nil, err
		}
		return []Message{msg}, nil
	}

	iterFnc := func(msgs *[]Message) func(m Message) bool {
		currSize := 0
		return func(m Message) bool {
			if strings.TrimSpace(req.ChannelId) != "" && m.ChannelID != req.ChannelId {
				return true
			}

			if req.ChannelType != 0 && req.ChannelType != m.ChannelType {
				return true
			}

			if strings.TrimSpace(req.FromUid) != "" && m.FromUID != req.FromUid {
				return true
			}

			if strings.TrimSpace(req.ClientMsgNo) != "" && m.ClientMsgNo != req.ClientMsgNo {
				return true
			}

			if len(req.Payload) > 0 && !bytes.Contains(m.Payload, req.Payload) {
				return true
			}

			if req.MessageId > 0 && req.MessageId != m.MessageID {
				return true
			}

			if req.Pre {
				if req.OffsetMessageId > 0 && m.MessageID <= req.OffsetMessageId { // 当前消息小于等于req.MessageId时停止查询
					return false
				}
			} else {
				if req.OffsetMessageId > 0 && m.MessageID >= req.OffsetMessageId { // 当前消息小于等于req.MessageId时停止查询
					return false
				}
			}

			if currSize >= req.Limit { // 消息数量大于等于limit时停止查询
				return false
			}
			currSize++

			*msgs = append(*msgs, m)

			return true
		}
	}

	if strings.TrimSpace(req.ChannelId) != "" && req.ChannelType != 0 {
		db := wk.channelDb(req.ChannelId, req.ChannelType)
		msgs := make([]Message, 0, req.Limit)
		fnc := iterFnc(&msgs)

		startSeq := req.OffsetMessageSeq
		var endSeq uint64 = math.MaxUint64

		if req.OffsetMessageSeq > 0 {
			if req.Pre {
				startSeq = req.OffsetMessageSeq + 1
				endSeq = math.MaxUint64
			} else {
				startSeq = 0
				endSeq = req.OffsetMessageSeq
			}
		}

		iter := db.NewIter(&pebble.IterOptions{
			LowerBound: key.NewMessagePrimaryKey(req.ChannelId, req.ChannelType, startSeq),
			UpperBound: key.NewMessagePrimaryKey(req.ChannelId, req.ChannelType, endSeq),
		})
		defer iter.Close()

		err := wk.iteratorChannelMessagesDirection(iter, 0, !req.Pre, fnc)
		if err != nil {
			return nil, err
		}

		return msgs, nil

	}

	allMsgs := make([]Message, 0, req.Limit*len(wk.dbs))
	for _, db := range wk.dbs {
		msgs := make([]Message, 0)
		fnc := iterFnc(&msgs)
		// 通过索引查询
		has, err := wk.searchMessageByIndex(req, db, fnc)
		if err != nil {
			return nil, err
		}

		if !has { // 如果有触发索引，则无需全局查询
			startMessageId := uint64(req.OffsetMessageId)
			var endMessageId uint64 = math.MaxUint64

			if req.OffsetMessageId > 0 {
				if req.Pre {
					startMessageId = uint64(req.OffsetMessageId + 1)
					endMessageId = math.MaxUint64
				} else {
					startMessageId = 0
					endMessageId = uint64(req.OffsetMessageId)
				}
			}

			iter := db.NewIter(&pebble.IterOptions{
				LowerBound: key.NewMessageIndexMessageIdKey(startMessageId),
				UpperBound: key.NewMessageIndexMessageIdKey(endMessageId),
			})
			defer iter.Close()

			var pkey [16]byte
			var iterStepFnc func() bool
			if req.Pre {
				if !iter.First() {
					continue
				}
				iterStepFnc = iter.Next
			} else {
				if !iter.Last() {
					continue
				}
				iterStepFnc = iter.Prev
			}

			for ; iter.Valid(); iterStepFnc() {
				copy(pkey[:], iter.Value())
				resultIter := db.NewIter(&pebble.IterOptions{
					LowerBound: key.NewMessageColumnKeyWithPrimary(pkey, key.MinColumnKey),
					UpperBound: key.NewMessageColumnKeyWithPrimary(pkey, key.MaxColumnKey),
				})
				defer resultIter.Close()
				err = wk.iteratorChannelMessages(resultIter, 0, fnc)
				if err != nil {
					return nil, err
				}
				if len(msgs) >= req.Limit {
					break
				}
			}
		}

		// 将msgs里消息时间比allMsgs里的消息时间早的消息插入到allMsgs里
		allMsgs = append(allMsgs, msgs...)
	}

	// 按照messageId降序排序
	sort.Slice(allMsgs, func(i, j int) bool {
		return allMsgs[i].MessageID > allMsgs[j].MessageID
	})

	// 如果allMsgs的数量大于limit，则截取前limit个
	if req.Limit > 0 && len(allMsgs) > req.Limit {
		if req.Pre {
			allMsgs = allMsgs[len(allMsgs)-req.Limit:]
		} else {
			allMsgs = allMsgs[:req.Limit]
		}

	}

	return allMsgs, nil
}

func (wk *wukongDB) setChannelLastMessageSeq(channelId string, channelType uint8, seq uint64, w *Batch) error {
	data := make([]byte, 16)
	wk.endian.PutUint64(data, seq)
	setTime := time.Now().UnixNano()
	wk.endian.PutUint64(data[8:], uint64(setTime))

	w.Set(key.NewChannelLastMessageSeqKey(channelId, channelType), data)
	return nil
}

func (wk *wukongDB) iteratorChannelMessages(iter *pebble.Iterator, limit int, iterFnc func(m Message) bool) error {
	return wk.iteratorChannelMessagesDirection(iter, limit, false, iterFnc)
}

func (wk *wukongDB) iteratorChannelMessagesDirection(iter *pebble.Iterator, limit int, reverse bool, iterFnc func(m Message) bool) error {
	var (
		size           int
		preMessageSeq  uint64
		preMessage     Message
		lastNeedAppend bool = true
		hasData        bool = false
	)

	if reverse {
		if !iter.Last() {
			return nil
		}
	} else {
		if !iter.First() {
			return nil
		}
	}
	for iter.Valid() {
		if reverse {
			if !iter.Prev() {
				break
			}
		} else {
			if !iter.Next() {
				break
			}
		}
		messageSeq, coulmnName, err := key.ParseMessageColumnKey(iter.Key())
		if err != nil {
			return err
		}

		if preMessageSeq != messageSeq {
			if preMessageSeq != 0 {
				size++
				if iterFnc != nil {
					if !iterFnc(preMessage) {
						lastNeedAppend = false
						break
					}
				}
				if limit != 0 && size >= limit {
					lastNeedAppend = false
					break
				}
			}

			preMessageSeq = messageSeq
			preMessage = Message{}
			preMessage.MessageSeq = uint32(messageSeq)
		}

		switch coulmnName {
		case key.TableMessage.Column.Header:
			preMessage.Framer = wkproto.FramerFromUint8(iter.Value()[0])
		case key.TableMessage.Column.Setting:
			preMessage.Setting = wkproto.Setting(iter.Value()[0])
		case key.TableMessage.Column.Expire:
			preMessage.Expire = wk.endian.Uint32(iter.Value())
		case key.TableMessage.Column.MessageId:
			preMessage.MessageID = int64(wk.endian.Uint64(iter.Value()))
		case key.TableMessage.Column.ClientMsgNo:
			preMessage.ClientMsgNo = string(iter.Value())
		case key.TableMessage.Column.Timestamp:
			preMessage.Timestamp = int32(wk.endian.Uint32(iter.Value()))
		case key.TableMessage.Column.ChannelId:
			preMessage.ChannelID = string(iter.Value())
		case key.TableMessage.Column.ChannelType:
			preMessage.ChannelType = iter.Value()[0]
		case key.TableMessage.Column.Topic:
			preMessage.Topic = string(iter.Value())
		case key.TableMessage.Column.FromUid:
			preMessage.FromUID = string(iter.Value())
		case key.TableMessage.Column.Payload:
			// 这里必须复制一份，否则会被pebble覆盖
			var payload = make([]byte, len(iter.Value()))
			copy(payload, iter.Value())
			preMessage.Payload = payload
		case key.TableMessage.Column.Term:
			preMessage.Term = wk.endian.Uint64(iter.Value())

		}
		hasData = true
	}
	if lastNeedAppend && hasData {
		if iterFnc != nil {

			_ = iterFnc(preMessage)
		}
	}

	return nil

}

func (wk *wukongDB) parseChannelMessagesWithLimitSize(iter *pebble.Iterator, limitSize uint64) ([]Message, error) {
	var (
		msgs           = make([]Message, 0)
		preMessageSeq  uint64
		preMessage     Message
		lastNeedAppend bool = false
	)

	var size uint64 = 0
	for iter.First(); iter.Valid(); iter.Next() {
		lastNeedAppend = true
		messageSeq, coulmnName, err := key.ParseMessageColumnKey(iter.Key())
		if err != nil {
			return nil, err
		}

		if messageSeq == 0 {
			wk.Panic("messageSeq is 0", zap.Any("key", iter.Key()), zap.Any("coulmnName", coulmnName))
		}

		if preMessageSeq != messageSeq {
			if preMessageSeq != 0 {
				size += uint64(preMessage.Size())
				msgs = append(msgs, preMessage)
				if limitSize != 0 && size >= limitSize {
					lastNeedAppend = false
					break
				}
			}

			preMessageSeq = messageSeq
			preMessage = Message{}
			preMessage.MessageSeq = uint32(messageSeq)
		}

		switch coulmnName {
		case key.TableMessage.Column.Header:
			preMessage.RecvPacket.Framer = wkproto.FramerFromUint8(iter.Value()[0])
		case key.TableMessage.Column.Setting:
			preMessage.RecvPacket.Setting = wkproto.Setting(iter.Value()[0])
		case key.TableMessage.Column.Expire:
			preMessage.RecvPacket.Expire = wk.endian.Uint32(iter.Value())
		case key.TableMessage.Column.MessageId:
			preMessage.MessageID = int64(wk.endian.Uint64(iter.Value()))
		case key.TableMessage.Column.ClientMsgNo:
			preMessage.ClientMsgNo = string(iter.Value())
		case key.TableMessage.Column.Timestamp:
			preMessage.Timestamp = int32(wk.endian.Uint32(iter.Value()))
		case key.TableMessage.Column.ChannelId:
			preMessage.ChannelID = string(iter.Value())
		case key.TableMessage.Column.ChannelType:
			preMessage.ChannelType = iter.Value()[0]
		case key.TableMessage.Column.Topic:
			preMessage.Topic = string(iter.Value())
		case key.TableMessage.Column.FromUid:
			preMessage.RecvPacket.FromUID = string(iter.Value())
		case key.TableMessage.Column.Payload:
			// 这里必须复制一份，否则会被pebble覆盖
			var payload = make([]byte, len(iter.Value()))
			copy(payload, iter.Value())
			preMessage.Payload = payload
		case key.TableMessage.Column.Term:
			preMessage.Term = wk.endian.Uint64(iter.Value())
		}
	}

	if lastNeedAppend {
		msgs = append(msgs, preMessage)
	}

	return msgs, nil

}

func (wk *wukongDB) writeMessage(channelId string, channelType uint8, msg Message, w *Batch) error {

	var (
		messageIdBytes = make([]byte, 8)
	)

	// header
	header := wkproto.ToFixHeaderUint8(msg.RecvPacket.Framer)
	w.Set(key.NewMessageColumnKey(channelId, channelType, uint64(msg.MessageSeq), key.TableMessage.Column.Header), []byte{header})

	// setting
	setting := msg.RecvPacket.Setting.Uint8()
	w.Set(key.NewMessageColumnKey(channelId, channelType, uint64(msg.MessageSeq), key.TableMessage.Column.Setting), []byte{setting})

	// expire
	expireBytes := make([]byte, 4)
	wk.endian.PutUint32(expireBytes, msg.RecvPacket.Expire)
	w.Set(key.NewMessageColumnKey(channelId, channelType, uint64(msg.MessageSeq), key.TableMessage.Column.Expire), expireBytes)

	// messageId
	wk.endian.PutUint64(messageIdBytes, uint64(msg.MessageID))
	w.Set(key.NewMessageColumnKey(channelId, channelType, uint64(msg.MessageSeq), key.TableMessage.Column.MessageId), messageIdBytes)

	// messageSeq
	messageSeqBytes := make([]byte, 8)
	wk.endian.PutUint64(messageSeqBytes, uint64(msg.MessageSeq))
	w.Set(key.NewMessageColumnKey(channelId, channelType, uint64(msg.MessageSeq), key.TableMessage.Column.MessageSeq), messageSeqBytes)

	// clientMsgNo
	w.Set(key.NewMessageColumnKey(channelId, channelType, uint64(msg.MessageSeq), key.TableMessage.Column.ClientMsgNo), []byte(msg.ClientMsgNo))

	// timestamp
	timestampBytes := make([]byte, 4)
	wk.endian.PutUint32(timestampBytes, uint32(msg.Timestamp))
	w.Set(key.NewMessageColumnKey(channelId, channelType, uint64(msg.MessageSeq), key.TableMessage.Column.Timestamp), timestampBytes)

	// channelId
	w.Set(key.NewMessageColumnKey(channelId, channelType, uint64(msg.MessageSeq), key.TableMessage.Column.ChannelId), []byte(msg.ChannelID))

	// channelType
	w.Set(key.NewMessageColumnKey(channelId, channelType, uint64(msg.MessageSeq), key.TableMessage.Column.ChannelType), []byte{msg.ChannelType})

	// topic
	w.Set(key.NewMessageColumnKey(channelId, channelType, uint64(msg.MessageSeq), key.TableMessage.Column.Topic), []byte(msg.Topic))

	// fromUid
	w.Set(key.NewMessageColumnKey(channelId, channelType, uint64(msg.MessageSeq), key.TableMessage.Column.FromUid), []byte(msg.RecvPacket.FromUID))

	// payload
	w.Set(key.NewMessageColumnKey(channelId, channelType, uint64(msg.MessageSeq), key.TableMessage.Column.Payload), msg.Payload)

	// term
	termBytes := make([]byte, 8)
	wk.endian.PutUint64(termBytes, msg.Term)
	w.Set(key.NewMessageColumnKey(channelId, channelType, uint64(msg.MessageSeq), key.TableMessage.Column.Term), termBytes)

	var primaryValue = [16]byte{}
	wk.endian.PutUint64(primaryValue[:], key.ChannelToNum(channelId, channelType))
	wk.endian.PutUint64(primaryValue[8:], uint64(msg.MessageSeq))

	// index fromUid
	w.Set(key.NewMessageSecondIndexFromUidKey(msg.FromUID, primaryValue), nil)

	// index messageId
	w.Set(key.NewMessageIndexMessageIdKey(uint64(msg.MessageID)), primaryValue[:])

	// index clientMsgNo
	w.Set(key.NewMessageSecondIndexClientMsgNoKey(msg.ClientMsgNo, primaryValue), nil)

	// index timestamp
	w.Set(key.NewMessageIndexTimestampKey(uint64(msg.Timestamp), primaryValue), nil)

	return nil
}
