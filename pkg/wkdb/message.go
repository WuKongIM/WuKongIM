package wkdb

import (
	"fmt"
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb/key"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/cockroachdb/pebble"
	"go.uber.org/zap"
)

func (p *pebbleDB) AppendMessages(channelId string, channelType uint8, msgs []Message) error {

	batch := p.db.NewBatch()
	defer batch.Close()
	for _, msg := range msgs {
		if err := p.writeMessage(msg, batch); err != nil {
			return err
		}
	}

	return batch.Commit(p.wo)
}

// 情况1: startMessageSeq=100, endMessageSeq=0, limit=10 返回的消息seq为91-100的消息 (limit生效)
// 情况2: startMessageSeq=5, endMessageSeq=0, limit=10 返回的消息seq为1-5的消息（消息无）

// 情况3: startMessageSeq=100, endMessageSeq=95, limit=10 返回的消息seq为96-100的消息（endMessageSeq生效）
// 情况4: startMessageSeq=100, endMessageSeq=50, limit=10 返回的消息seq为91-100的消息（limit生效）
func (p *pebbleDB) LoadPrevRangeMsgs(channelId string, channelType uint8, startMessageSeq, endMessageSeq uint64, limit int) ([]Message, error) {

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
	lastSeq, err := p.GetChannelLastMessageSeq(channelId, channelType)
	if err != nil {
		return nil, err
	}

	if maxSeq > lastSeq {
		maxSeq = lastSeq + 1
	}

	iter := p.db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewMessagePrimaryKey(channelId, channelType, minSeq),
		UpperBound: key.NewMessagePrimaryKey(channelId, channelType, maxSeq),
	})
	defer iter.Close()

	return p.parseChannelMessages(iter, limit)
}

func (p *pebbleDB) LoadNextRangeMsgs(channelId string, channelType uint8, startMessageSeq, endMessageSeq uint64, limit int) ([]Message, error) {
	minSeq := startMessageSeq
	maxSeq := endMessageSeq
	if endMessageSeq == 0 {
		maxSeq = math.MaxUint64
	}

	// 获取频道的最大的messageSeq，超过这个的消息都视为无效
	lastSeq, err := p.GetChannelLastMessageSeq(channelId, channelType)
	if err != nil {
		return nil, err
	}

	if maxSeq > lastSeq {
		maxSeq = lastSeq + 1
	}

	iter := p.db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewMessagePrimaryKey(channelId, channelType, minSeq),
		UpperBound: key.NewMessagePrimaryKey(channelId, channelType, maxSeq),
	})
	defer iter.Close()
	return p.parseChannelMessages(iter, limit)

}

func (p *pebbleDB) LoadNextRangeMsgsForSize(channelId string, channelType uint8, startMessageSeq, endMessageSeq uint64, limitSize uint64) ([]Message, error) {

	minSeq := startMessageSeq
	maxSeq := endMessageSeq
	if endMessageSeq == 0 {
		maxSeq = math.MaxUint64
	}
	iter := p.db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewMessagePrimaryKey(channelId, channelType, minSeq),
		UpperBound: key.NewMessagePrimaryKey(channelId, channelType, maxSeq),
	})
	defer iter.Close()
	return p.parseChannelMessagesWithLimitSize(iter, limitSize)
}

func (p *pebbleDB) TruncateLogTo(channelId string, channelType uint8, messageSeq uint64) error {
	if messageSeq == 0 {
		return fmt.Errorf("messageSeq[%d] must be greater than 0", messageSeq)

	}
	lastMsgSeq, err := p.GetChannelLastMessageSeq(channelId, channelType)
	if err != nil {
		return err
	}
	err = p.db.DeleteRange(key.NewMessagePrimaryKey(channelId, channelType, messageSeq), key.NewMessagePrimaryKey(channelId, channelType, math.MaxUint64), p.wo)
	if err != nil {
		return err
	}
	batch := p.db.NewBatch()
	defer batch.Close()

	err = batch.DeleteRange(key.NewMessagePrimaryKey(channelId, channelType, messageSeq), key.NewMessagePrimaryKey(channelId, channelType, math.MaxUint64), p.wo)
	if err != nil {
		return err
	}
	err = p.setChannelLastMessageSeq(channelId, channelType, min(messageSeq-1, lastMsgSeq), batch)
	if err != nil {
		return err
	}

	return batch.Commit(p.wo)
}

func min(x, y uint64) uint64 {
	if x < y {
		return x
	}
	return y
}
func (p *pebbleDB) GetChannelLastMessageSeq(channelId string, channelType uint8) (uint64, error) {
	result, closer, err := p.db.Get(key.NewChannelLastMessageSeqKey(channelId, channelType))
	if err != nil {
		if err == pebble.ErrNotFound {
			return 0, nil
		}
		return 0, err
	}
	defer closer.Close()
	return p.endian.Uint64(result), nil
}

func (p *pebbleDB) SetChannelLastMessageSeq(channelId string, channelType uint8, seq uint64) error {
	return p.setChannelLastMessageSeq(channelId, channelType, seq, p.db)
}

func (p *pebbleDB) setChannelLastMessageSeq(channelId string, channelType uint8, seq uint64, w pebble.Writer) error {
	seqBytes := make([]byte, 8)
	p.endian.PutUint64(seqBytes, seq)
	return w.Set(key.NewChannelLastMessageSeqKey(channelId, channelType), seqBytes, p.wo)
}

func (p *pebbleDB) parseChannelMessages(iter *pebble.Iterator, limit int) ([]Message, error) {
	var (
		msgs           = make([]Message, 0, limit)
		preMessageSeq  uint64
		preMessage     Message
		lastNeedAppend bool = true
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
			preMessage.RecvPacket.Expire = p.endian.Uint32(iter.Value())
		case key.TableMessage.Column.MessageId:
			preMessage.MessageID = int64(p.endian.Uint64(iter.Value()))
		case key.TableMessage.Column.ClientMsgNo:
			preMessage.ClientMsgNo = string(iter.Value())
		case key.TableMessage.Column.Timestamp:
			preMessage.Timestamp = int32(p.endian.Uint32(iter.Value()))
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
			preMessage.Term = p.endian.Uint64(iter.Value())

		}
	}

	if lastNeedAppend {
		msgs = append(msgs, preMessage)
	}

	return msgs, nil

}

func (p *pebbleDB) parseChannelMessagesWithLimitSize(iter *pebble.Iterator, limitSize uint64) ([]Message, error) {
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
			p.Panic("messageSeq is 0", zap.Any("key", iter.Key()), zap.Any("coulmnName", coulmnName))
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
			preMessage.RecvPacket.Expire = p.endian.Uint32(iter.Value())
		case key.TableMessage.Column.MessageId:
			preMessage.MessageID = int64(p.endian.Uint64(iter.Value()))
		case key.TableMessage.Column.ClientMsgNo:
			preMessage.ClientMsgNo = string(iter.Value())
		case key.TableMessage.Column.Timestamp:
			preMessage.Timestamp = int32(p.endian.Uint32(iter.Value()))
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
			preMessage.Term = p.endian.Uint64(iter.Value())
		}
	}

	if lastNeedAppend {
		msgs = append(msgs, preMessage)
	}

	return msgs, nil

}

func (p *pebbleDB) writeMessage(msg Message, w pebble.Writer) error {

	var (
		messageIdBytes = make([]byte, 8)
		err            error
	)

	// header
	header := wkproto.ToFixHeaderUint8(msg.RecvPacket.Framer)
	if err = w.Set(key.NewMessageColumnKey(msg.ChannelID, msg.ChannelType, uint64(msg.MessageSeq), key.TableMessage.Column.Header), []byte{header}, p.wo); err != nil {
		return err
	}
	// setting
	setting := msg.RecvPacket.Setting.Uint8()
	if err = w.Set(key.NewMessageColumnKey(msg.ChannelID, msg.ChannelType, uint64(msg.MessageSeq), key.TableMessage.Column.Setting), []byte{setting}, p.wo); err != nil {
		return err
	}

	// expire
	expireBytes := make([]byte, 4)
	p.endian.PutUint32(expireBytes, msg.RecvPacket.Expire)
	if err = w.Set(key.NewMessageColumnKey(msg.ChannelID, msg.ChannelType, uint64(msg.MessageSeq), key.TableMessage.Column.Expire), expireBytes, p.wo); err != nil {
		return err
	}

	// messageId
	p.endian.PutUint64(messageIdBytes, uint64(msg.MessageID))
	if err = w.Set(key.NewMessageColumnKey(msg.ChannelID, msg.ChannelType, uint64(msg.MessageSeq), key.TableMessage.Column.MessageId), messageIdBytes, p.wo); err != nil {
		return err
	}

	// messageSeq
	messageSeqBytes := make([]byte, 8)
	p.endian.PutUint64(messageSeqBytes, uint64(msg.MessageSeq))
	if err = w.Set(key.NewMessageColumnKey(msg.ChannelID, msg.ChannelType, uint64(msg.MessageSeq), key.TableMessage.Column.MessageSeq), messageSeqBytes, p.wo); err != nil {
		return err
	}

	// clientMsgNo
	if err = w.Set(key.NewMessageColumnKey(msg.ChannelID, msg.ChannelType, uint64(msg.MessageSeq), key.TableMessage.Column.ClientMsgNo), []byte(msg.ClientMsgNo), p.wo); err != nil {
		return err
	}

	// timestamp
	timestampBytes := make([]byte, 4)
	p.endian.PutUint32(timestampBytes, uint32(msg.Timestamp))
	if err = w.Set(key.NewMessageColumnKey(msg.ChannelID, msg.ChannelType, uint64(msg.MessageSeq), key.TableMessage.Column.Timestamp), timestampBytes, p.wo); err != nil {
		return err
	}

	// channelId
	if err = w.Set(key.NewMessageColumnKey(msg.ChannelID, msg.ChannelType, uint64(msg.MessageSeq), key.TableMessage.Column.ChannelId), []byte(msg.ChannelID), p.wo); err != nil {
		return err
	}

	// channelType
	if err = w.Set(key.NewMessageColumnKey(msg.ChannelID, msg.ChannelType, uint64(msg.MessageSeq), key.TableMessage.Column.ChannelType), []byte{msg.ChannelType}, p.wo); err != nil {
		return err
	}

	// topic
	if err = w.Set(key.NewMessageColumnKey(msg.ChannelID, msg.ChannelType, uint64(msg.MessageSeq), key.TableMessage.Column.Topic), []byte(msg.Topic), p.wo); err != nil {
		return err
	}

	// fromUid
	if err = w.Set(key.NewMessageColumnKey(msg.ChannelID, msg.ChannelType, uint64(msg.MessageSeq), key.TableMessage.Column.FromUid), []byte(msg.RecvPacket.FromUID), p.wo); err != nil {
		return err
	}

	// payload
	if err = w.Set(key.NewMessageColumnKey(msg.ChannelID, msg.ChannelType, uint64(msg.MessageSeq), key.TableMessage.Column.Payload), msg.Payload, p.wo); err != nil {
		return err
	}

	// term
	termBytes := make([]byte, 8)
	p.endian.PutUint64(termBytes, msg.Term)
	if err = w.Set(key.NewMessageColumnKey(msg.ChannelID, msg.ChannelType, uint64(msg.MessageSeq), key.TableMessage.Column.Term), termBytes, p.wo); err != nil {
		return err
	}

	return nil
}
