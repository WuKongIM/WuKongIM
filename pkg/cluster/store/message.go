package store

import (
	"context"

	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"

	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
)

// DeleteRangeMessages 范围删除消息
func (s *Store) DeleteRangeMessages(channelId string, channelType uint8, startMessageSeq, endMessageSeq uint64) error {
	data := EncodeCMDDeleteRangeMessages(channelId, channelType, startMessageSeq, endMessageSeq)
	cmd := NewCMD(CMDDeleteRangeMessages, data)
	cmdData, err := cmd.Marshal()
	if err != nil {
		return err
	}
	slotId := s.opts.Slot.GetSlotId(channelId)
	_, err = s.opts.Slot.ProposeUntilApplied(slotId, cmdData)
	if err != nil {
		s.Error("DeleteRangeMessages failed", zap.Error(err),
			zap.String("channelId", channelId),
			zap.Uint8("channelType", channelType),
			zap.Uint64("startMessageSeq", startMessageSeq),
			zap.Uint64("endMessageSeq", endMessageSeq))
		return err
	}
	return nil
}

// EncodeCMDDeleteRangeMessages 编码删除范围消息命令
func EncodeCMDDeleteRangeMessages(channelId string, channelType uint8, startMessageSeq, endMessageSeq uint64) []byte {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteString(channelId)
	enc.WriteUint8(channelType)
	enc.WriteUint64(startMessageSeq)
	enc.WriteUint64(endMessageSeq)
	return enc.Bytes()
}

// AppendMessages 追加消息
func (s *Store) AppendMessages(ctx context.Context, channelId string, channelType uint8, msgs []wkdb.Message) (types.ProposeRespSet, error) {

	if len(msgs) == 0 {
		return nil, nil
	}
	reqs := make([]types.ProposeReq, 0, len(msgs))
	for _, msg := range msgs {
		data, err := msg.Marshal()
		if err != nil {
			return nil, err
		}

		reqs = append(reqs, types.ProposeReq{
			Id:   uint64(msg.MessageID),
			Data: data,
		})
	}

	results, err := s.opts.Channel.ProposeBatchUntilAppliedTimeout(ctx, channelId, channelType, reqs)
	if err != nil {
		return nil, err
	}
	return results, nil
}

func (s *Store) LoadNextRangeMsgs(channelID string, channelType uint8, startMessageSeq, endMessageSeq uint64, limit int) ([]wkdb.Message, error) {
	return s.wdb.LoadNextRangeMsgs(channelID, channelType, startMessageSeq, endMessageSeq, limit)
}

func (s *Store) LoadMsg(channelID string, channelType uint8, seq uint64) (wkdb.Message, error) {
	return s.wdb.LoadMsg(channelID, channelType, seq)
}

func (s *Store) LoadLastMsgs(channelID string, channelType uint8, limit int) ([]wkdb.Message, error) {
	return s.wdb.LoadLastMsgs(channelID, channelType, limit)
}

func (s *Store) LoadLastMsgsWithEnd(channelID string, channelType uint8, end uint64, limit int) ([]wkdb.Message, error) {
	return s.wdb.LoadLastMsgsWithEnd(channelID, channelType, end, limit)
}

func (s *Store) LoadPrevRangeMsgs(channelID string, channelType uint8, start, end uint64, limit int) ([]wkdb.Message, error) {
	return s.wdb.LoadPrevRangeMsgs(channelID, channelType, start, end, limit)
}

func (s *Store) GetLastMsgSeq(channelID string, channelType uint8) (uint64, error) {
	seq, _, err := s.wdb.GetChannelLastMessageSeq(channelID, channelType)
	return seq, err
}

func (s *Store) GetMessagesOfNotifyQueue(count int) ([]wkdb.Message, error) {
	return s.wdb.GetMessagesOfNotifyQueue(count)
}

func (s *Store) AppendMessageOfNotifyQueue(messages []wkdb.Message) error {
	return s.wdb.AppendMessageOfNotifyQueue(messages)
}

func (s *Store) RemoveMessagesOfNotifyQueue(messageIDs []int64) error {
	return s.wdb.RemoveMessagesOfNotifyQueue(messageIDs)
}

func (s *Store) DeleteChannelAndClearMessages(channelID string, channelType uint8) error {
	return nil
}

// 搜索消息
func (s *Store) SearchMessages(req wkdb.MessageSearchReq) ([]wkdb.Message, error) {
	return s.wdb.SearchMessages(req)
}
