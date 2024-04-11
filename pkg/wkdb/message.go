package wkdb

import (
	"fmt"
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb/key"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/cockroachdb/pebble"
	"go.uber.org/zap"
)

func (wk *wukongDB) AppendMessages(channelId string, channelType uint8, msgs []Message) error {

	db := wk.shardDB(channelId)
	batch := db.NewBatch()
	defer batch.Close()
	for _, msg := range msgs {
		if err := wk.writeMessage(channelId, channelType, msg, batch); err != nil {
			return err
		}
	}

	return batch.Commit(wk.wo)
}

// 情况1: startMessageSeq=100, endMessageSeq=0, limit=10 返回的消息seq为91-100的消息 (limit生效)
// 情况2: startMessageSeq=5, endMessageSeq=0, limit=10 返回的消息seq为1-5的消息（消息无）

// 情况3: startMessageSeq=100, endMessageSeq=95, limit=10 返回的消息seq为96-100的消息（endMessageSeq生效）
// 情况4: startMessageSeq=100, endMessageSeq=50, limit=10 返回的消息seq为91-100的消息（limit生效）
func (wk *wukongDB) LoadPrevRangeMsgs(channelId string, channelType uint8, startMessageSeq, endMessageSeq uint64, limit int) ([]Message, error) {

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
	lastSeq, err := wk.GetChannelLastMessageSeq(channelId, channelType)
	if err != nil {
		return nil, err
	}

	if maxSeq > lastSeq {
		maxSeq = lastSeq + 1
	}

	db := wk.shardDB(channelId)

	iter := db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewMessagePrimaryKey(channelId, channelType, minSeq),
		UpperBound: key.NewMessagePrimaryKey(channelId, channelType, maxSeq),
	})
	defer iter.Close()

	return wk.parseChannelMessages(iter, limit)
}

func (wk *wukongDB) LoadNextRangeMsgs(channelId string, channelType uint8, startMessageSeq, endMessageSeq uint64, limit int) ([]Message, error) {
	minSeq := startMessageSeq
	maxSeq := endMessageSeq
	if endMessageSeq == 0 {
		maxSeq = math.MaxUint64
	}

	// 获取频道的最大的messageSeq，超过这个的消息都视为无效
	lastSeq, err := wk.GetChannelLastMessageSeq(channelId, channelType)
	if err != nil {
		return nil, err
	}

	if maxSeq > lastSeq {
		maxSeq = lastSeq + 1
	}

	db := wk.shardDB(channelId)

	iter := db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewMessagePrimaryKey(channelId, channelType, minSeq),
		UpperBound: key.NewMessagePrimaryKey(channelId, channelType, maxSeq),
	})
	defer iter.Close()
	return wk.parseChannelMessages(iter, limit)

}

func (wk *wukongDB) LoadMsg(channelId string, channelType uint8, seq uint64) (Message, error) {

	db := wk.shardDB(channelId)

	iter := db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewMessagePrimaryKey(channelId, channelType, seq),
		UpperBound: key.NewMessagePrimaryKey(channelId, channelType, seq+1),
	})
	defer iter.Close()
	msgs, err := wk.parseChannelMessages(iter, 1)
	if err != nil {
		return EmptyMessage, err
	}
	if len(msgs) == 0 {
		return EmptyMessage, fmt.Errorf("message not found")
	}
	return msgs[0], nil

}

func (wk *wukongDB) LoadLastMsgs(channelID string, channelType uint8, limit int) ([]Message, error) {
	lastSeq, err := wk.GetChannelLastMessageSeq(channelID, channelType)
	if err != nil {
		return nil, err
	}
	return wk.LoadPrevRangeMsgs(channelID, channelType, lastSeq, 0, limit)

}

func (wk *wukongDB) LoadLastMsgsWithEnd(channelID string, channelType uint8, endMessageSeq uint64, limit int) ([]Message, error) {
	lastSeq, err := wk.GetChannelLastMessageSeq(channelID, channelType)
	if err != nil {
		return nil, err
	}
	return wk.LoadPrevRangeMsgs(channelID, channelType, lastSeq, endMessageSeq, limit)
}

func (wk *wukongDB) LoadNextRangeMsgsForSize(channelId string, channelType uint8, startMessageSeq, endMessageSeq uint64, limitSize uint64) ([]Message, error) {

	minSeq := startMessageSeq
	maxSeq := endMessageSeq
	if endMessageSeq == 0 {
		maxSeq = math.MaxUint64
	}
	db := wk.shardDB(channelId)

	iter := db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewMessagePrimaryKey(channelId, channelType, minSeq),
		UpperBound: key.NewMessagePrimaryKey(channelId, channelType, maxSeq),
	})
	defer iter.Close()
	return wk.parseChannelMessagesWithLimitSize(iter, limitSize)
}

func (wk *wukongDB) TruncateLogTo(channelId string, channelType uint8, messageSeq uint64) error {
	if messageSeq == 0 {
		return fmt.Errorf("messageSeq[%d] must be greater than 0", messageSeq)

	}
	lastMsgSeq, err := wk.GetChannelLastMessageSeq(channelId, channelType)
	if err != nil {
		return err
	}
	db := wk.shardDB(channelId)
	err = db.DeleteRange(key.NewMessagePrimaryKey(channelId, channelType, messageSeq), key.NewMessagePrimaryKey(channelId, channelType, math.MaxUint64), wk.wo)
	if err != nil {
		return err
	}
	batch := db.NewBatch()
	defer batch.Close()

	err = batch.DeleteRange(key.NewMessagePrimaryKey(channelId, channelType, messageSeq), key.NewMessagePrimaryKey(channelId, channelType, math.MaxUint64), wk.wo)
	if err != nil {
		return err
	}
	err = wk.setChannelLastMessageSeq(channelId, channelType, min(messageSeq-1, lastMsgSeq), batch)
	if err != nil {
		return err
	}

	return batch.Commit(wk.wo)
}

func min(x, y uint64) uint64 {
	if x < y {
		return x
	}
	return y
}
func (wk *wukongDB) GetChannelLastMessageSeq(channelId string, channelType uint8) (uint64, error) {
	db := wk.shardDB(channelId)
	result, closer, err := db.Get(key.NewChannelLastMessageSeqKey(channelId, channelType))
	if err != nil {
		if err == pebble.ErrNotFound {
			return 0, nil
		}
		return 0, err
	}
	defer closer.Close()
	return wk.endian.Uint64(result), nil
}

func (wk *wukongDB) SetChannelLastMessageSeq(channelId string, channelType uint8, seq uint64) error {
	db := wk.shardDB(channelId)
	return wk.setChannelLastMessageSeq(channelId, channelType, seq, db)
}

func (wk *wukongDB) setChannelLastMessageSeq(channelId string, channelType uint8, seq uint64, w pebble.Writer) error {
	seqBytes := make([]byte, 8)
	wk.endian.PutUint64(seqBytes, seq)
	return w.Set(key.NewChannelLastMessageSeqKey(channelId, channelType), seqBytes, wk.wo)
}

func (wk *wukongDB) parseChannelMessages(iter *pebble.Iterator, limit int) ([]Message, error) {
	var (
		msgs           = make([]Message, 0, limit)
		preMessageSeq  uint64
		preMessage     Message
		lastNeedAppend bool = true
		hasData        bool = false
	)

	for iter.First(); iter.Valid(); iter.Next() {
		messageSeq, coulmnName, err := key.ParseMessageColumnKey(iter.Key())
		if err != nil {
			return nil, err
		}

		if preMessageSeq != messageSeq {
			if preMessageSeq != 0 {
				msgs = append(msgs, preMessage)
				if len(msgs) >= limit {
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
			preMessage.Payload = iter.Value()
		case key.TableMessage.Column.Term:
			preMessage.Term = wk.endian.Uint64(iter.Value())

		}
		hasData = true
	}

	if lastNeedAppend && hasData {
		msgs = append(msgs, preMessage)
	}

	return msgs, nil

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
			preMessage.Payload = iter.Value()
		case key.TableMessage.Column.Term:
			preMessage.Term = wk.endian.Uint64(iter.Value())
		}
	}

	if lastNeedAppend {
		msgs = append(msgs, preMessage)
	}

	return msgs, nil

}

func (wk *wukongDB) writeMessage(channelId string, channelType uint8, msg Message, w pebble.Writer) error {

	var (
		messageIdBytes = make([]byte, 8)
		err            error
	)

	// header
	header := wkproto.ToFixHeaderUint8(msg.RecvPacket.Framer)
	if err = w.Set(key.NewMessageColumnKey(channelId, channelType, uint64(msg.MessageSeq), key.TableMessage.Column.Header), []byte{header}, wk.wo); err != nil {
		return err
	}
	// setting
	setting := msg.RecvPacket.Setting.Uint8()
	if err = w.Set(key.NewMessageColumnKey(channelId, channelType, uint64(msg.MessageSeq), key.TableMessage.Column.Setting), []byte{setting}, wk.wo); err != nil {
		return err
	}

	// expire
	expireBytes := make([]byte, 4)
	wk.endian.PutUint32(expireBytes, msg.RecvPacket.Expire)
	if err = w.Set(key.NewMessageColumnKey(channelId, channelType, uint64(msg.MessageSeq), key.TableMessage.Column.Expire), expireBytes, wk.wo); err != nil {
		return err
	}

	// messageId
	wk.endian.PutUint64(messageIdBytes, uint64(msg.MessageID))
	if err = w.Set(key.NewMessageColumnKey(channelId, channelType, uint64(msg.MessageSeq), key.TableMessage.Column.MessageId), messageIdBytes, wk.wo); err != nil {
		return err
	}

	// messageSeq
	messageSeqBytes := make([]byte, 8)
	wk.endian.PutUint64(messageSeqBytes, uint64(msg.MessageSeq))
	if err = w.Set(key.NewMessageColumnKey(channelId, channelType, uint64(msg.MessageSeq), key.TableMessage.Column.MessageSeq), messageSeqBytes, wk.wo); err != nil {
		return err
	}

	// clientMsgNo
	if err = w.Set(key.NewMessageColumnKey(channelId, channelType, uint64(msg.MessageSeq), key.TableMessage.Column.ClientMsgNo), []byte(msg.ClientMsgNo), wk.wo); err != nil {
		return err
	}

	// timestamp
	timestampBytes := make([]byte, 4)
	wk.endian.PutUint32(timestampBytes, uint32(msg.Timestamp))
	if err = w.Set(key.NewMessageColumnKey(channelId, channelType, uint64(msg.MessageSeq), key.TableMessage.Column.Timestamp), timestampBytes, wk.wo); err != nil {
		return err
	}

	// channelId
	if err = w.Set(key.NewMessageColumnKey(channelId, channelType, uint64(msg.MessageSeq), key.TableMessage.Column.ChannelId), []byte(msg.ChannelID), wk.wo); err != nil {
		return err
	}

	// channelType
	if err = w.Set(key.NewMessageColumnKey(channelId, channelType, uint64(msg.MessageSeq), key.TableMessage.Column.ChannelType), []byte{msg.ChannelType}, wk.wo); err != nil {
		return err
	}

	// topic
	if err = w.Set(key.NewMessageColumnKey(channelId, channelType, uint64(msg.MessageSeq), key.TableMessage.Column.Topic), []byte(msg.Topic), wk.wo); err != nil {
		return err
	}

	// fromUid
	if err = w.Set(key.NewMessageColumnKey(channelId, channelType, uint64(msg.MessageSeq), key.TableMessage.Column.FromUid), []byte(msg.RecvPacket.FromUID), wk.wo); err != nil {
		return err
	}

	// payload
	if err = w.Set(key.NewMessageColumnKey(channelId, channelType, uint64(msg.MessageSeq), key.TableMessage.Column.Payload), msg.Payload, wk.wo); err != nil {
		return err
	}

	// term
	termBytes := make([]byte, 8)
	wk.endian.PutUint64(termBytes, msg.Term)
	if err = w.Set(key.NewMessageColumnKey(channelId, channelType, uint64(msg.MessageSeq), key.TableMessage.Column.Term), termBytes, wk.wo); err != nil {
		return err
	}

	return nil
}
