package store

import (
	"context"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
)

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

// 处理开始序号和结束序号 向下查询不用处理 endMessageSeq = 0
func (s *Store) handleLoadNextRangeMsgStartEndSeq(channelID string, channelType uint8, startMessageSeq, endMessageSeq uint64) (uint64, uint64) {
	deleteMessageSeq := service.Store.GetMessageDeletedSeq(channelID, channelType)
	if deleteMessageSeq != 0 {
		if startMessageSeq < deleteMessageSeq {
			startMessageSeq = deleteMessageSeq
		}
		if endMessageSeq != 0 && endMessageSeq < deleteMessageSeq {
			endMessageSeq = deleteMessageSeq
		}
	}
	return startMessageSeq, endMessageSeq
}

// 处理开始序号和结束序号
func (s *Store) handleLoadPrevRangeMsgStartEndSeq(channelID string, channelType uint8, startMessageSeq, endMessageSeq uint64) (uint64, uint64) {
	deleteMessageSeq := service.Store.GetMessageDeletedSeq(channelID, channelType)
	if deleteMessageSeq != 0 {
		if endMessageSeq < deleteMessageSeq {
			endMessageSeq = deleteMessageSeq
		}
		if startMessageSeq < deleteMessageSeq {
			startMessageSeq = deleteMessageSeq
		}
	}
	return startMessageSeq, endMessageSeq
}

// 处理结束序号
func (s *Store) handleEndSeq(channelID string, channelType uint8, endMessageSeq uint64) uint64 {
	deleteMessageSeq := service.Store.GetMessageDeletedSeq(channelID, channelType)
	if deleteMessageSeq != 0 {
		if endMessageSeq < deleteMessageSeq {
			endMessageSeq = deleteMessageSeq
		}
	}
	return endMessageSeq
}
func (s *Store) LoadNextRangeMsgs(channelID string, channelType uint8, startMessageSeq, endMessageSeq uint64, limit int) ([]wkdb.Message, error) {
	startMessageSeq, endMessageSeq = s.handleLoadNextRangeMsgStartEndSeq(channelID, channelType, startMessageSeq, endMessageSeq)
	return s.wdb.LoadNextRangeMsgs(channelID, channelType, startMessageSeq, endMessageSeq, limit)
}

func (s *Store) LoadMsg(channelID string, channelType uint8, seq uint64) (wkdb.Message, error) {
	return s.wdb.LoadMsg(channelID, channelType, seq)
}

func (s *Store) LoadLastMsgs(channelID string, channelType uint8, end uint64, limit int) ([]wkdb.Message, error) {
	end = s.handleEndSeq(channelID, channelType, end)
	return s.wdb.LoadLastMsgs(channelID, channelType, end, limit)
}

func (s *Store) LoadLastMsgsWithEnd(channelID string, channelType uint8, end uint64, limit int) ([]wkdb.Message, error) {
	end = s.handleEndSeq(channelID, channelType, end)
	return s.wdb.LoadLastMsgsWithEnd(channelID, channelType, end, limit)
}

func (s *Store) LoadPrevRangeMsgs(channelID string, channelType uint8, start, end uint64, limit int) ([]wkdb.Message, error) {
	start, end = s.handleLoadPrevRangeMsgStartEndSeq(channelID, channelType, start, end)
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

// 范围删除消息
func (s *Store) DeleteMessageRange(channelId string, channelType uint8, startMessageSeq, endMessageSeq uint64) error {
	//新增或者修改频道删除记录
	err := s.wdb.AddOrUpdateMessageDeleted(wkdb.MessageDeleted{
		ChannelId:   channelId,
		ChannelType: channelType,
		MessageSeq:  endMessageSeq,
	})
	if err != nil {
		return err
	}
	return s.wdb.DeleteMessageRange(channelId, channelType, startMessageSeq, endMessageSeq)
}

// 根据channelId和channelType获取最大的删除消息序号
func (s *Store) GetMessageDeletedSeq(channelId string, channelType uint8) uint64 {
	return s.wdb.GetMessageDeletedSeq(channelId, channelType)
}
