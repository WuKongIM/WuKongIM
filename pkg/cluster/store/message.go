package store

import (
	"context"

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

// AppendMessageChunks 追加消息chunks
func (s *Store) AppendMessageChunks(ctx context.Context, channelId string, channelType uint8, msgs []wkdb.Message) error {
	return nil
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

// LoadMsgByClientMsgNo 通过 clientMsgNo 加载消息
func (s *Store) LoadMsgByClientMsgNo(channelId string, channelType uint8, clientMsgNo string) (wkdb.Message, error) {
	return s.wdb.LoadMsgByClientMsgNo(channelId, channelType, clientMsgNo)
}

// GetUserLastMsgSeq 获取用户在指定频道内发送的最新一条消息的seq
func (s *Store) GetUserLastMsgSeq(fromUid string, channelId string, channelType uint8) (uint64, error) {
	return s.wdb.GetUserLastMsgSeq(fromUid, channelId, channelType)
}

// LoadMsgsBatch 批量获取多个频道的消息
func (s *Store) LoadMsgsBatch(requests []wkdb.BatchMsgRequest) ([]wkdb.BatchMsgResponse, error) {
	return s.wdb.LoadMsgsBatch(requests)
}

// GetUserLastMsgSeqBatch 批量获取用户在多个频道的最后消息序号
func (s *Store) GetUserLastMsgSeqBatch(fromUid string, channels []wkdb.Channel) (map[string]uint64, error) {
	return s.wdb.GetUserLastMsgSeqBatch(fromUid, channels)
}
