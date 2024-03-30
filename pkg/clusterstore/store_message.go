package clusterstore

import (
	"context"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

func (s *Store) AppendMessages(ctx context.Context, channelID string, channelType uint8, msgs []wkdb.Message) error {

	if len(msgs) == 0 {
		return nil
	}
	logs := make([]replica.Log, len(msgs))
	for i, msg := range msgs {
		data, err := msg.Marshal()
		if err != nil {
			return err
		}
		logs[i] = replica.Log{
			MessageId: uint64(msg.MessageID),
			Data:      data,
		}
	}

	messageIdMap, err := s.opts.Cluster.ProposeChannelMessages(ctx, channelID, channelType, logs)
	if err != nil {
		return err
	}

	for _, msg := range msgs {
		msg.MessageSeq = uint32(messageIdMap[uint64(msg.MessageID)])
	}
	return nil
}

func (s *Store) LoadNextRangeMsgs(channelID string, channelType uint8, startMessageSeq, endMessageSeq uint32, limit int) ([]wkstore.Message, error) {
	return s.db.LoadNextRangeMsgs(channelID, channelType, startMessageSeq, endMessageSeq, limit)
}

func (s *Store) LoadMsg(channelID string, channelType uint8, seq uint32) (wkstore.Message, error) {
	return s.db.LoadMsg(channelID, channelType, seq)
}

func (s *Store) LoadLastMsgs(channelID string, channelType uint8, limit int) ([]wkstore.Message, error) {
	return s.db.LoadLastMsgs(channelID, channelType, limit)
}

func (s *Store) LoadLastMsgsWithEnd(channelID string, channelType uint8, end uint32, limit int) ([]wkstore.Message, error) {
	return s.db.LoadLastMsgsWithEnd(channelID, channelType, end, limit)
}

func (s *Store) LoadPrevRangeMsgs(channelID string, channelType uint8, start, end uint32, limit int) ([]wkstore.Message, error) {
	return s.db.LoadPrevRangeMsgs(channelID, channelType, start, end, limit)
}

func (s *Store) GetLastMsgSeq(channelID string, channelType uint8) (uint32, error) {
	return s.db.GetLastMsgSeq(channelID, channelType)
}

func (s *Store) GetMessagesOfNotifyQueue(count int) ([]wkstore.Message, error) {
	return s.db.GetMessagesOfNotifyQueue(count)
}

func (s *Store) AppendMessageOfNotifyQueue(messages []wkstore.Message) error {
	return s.db.AppendMessageOfNotifyQueue(messages)
}

func (s *Store) RemoveMessagesOfNotifyQueue(messageIDs []int64) error {
	return s.db.RemoveMessagesOfNotifyQueue(messageIDs)
}

func (s *Store) GetMessageShardLogStorage() *MessageShardLogStorage {
	return s.messageShardLogStorage
}

// SaveStreamMeta 保存消息流元数据
func (s *Store) SaveStreamMeta(meta *wkstore.StreamMeta) error {
	return nil
}

// StreamEnd 结束流
func (s *Store) StreamEnd(channelID string, channelType uint8, streamNo string) error {
	return nil
}

func (s *Store) GetStreamMeta(channelID string, channelType uint8, streamNo string) (*wkstore.StreamMeta, error) {
	return nil, nil
}

func (s *Store) GetStreamItems(channelID string, channelType uint8, streamNo string) ([]*wkstore.StreamItem, error) {
	return nil, nil
}

// AppendStreamItem 追加消息流
func (s *Store) AppendStreamItem(channelID string, channelType uint8, streamNo string, item *wkstore.StreamItem) (uint32, error) {
	return 0, nil
}

func (s *Store) UpdateMessageOfUserCursorIfNeed(uid string, messageSeq uint32) error {
	return nil
}

func (s *Store) GetMessageOfUserCursor(uid string) (uint32, error) {
	return 0, nil
}

func (s *Store) AppendMessagesOfUser(subscriber string, messages []wkstore.Message) error {
	if len(messages) == 0 {
		return nil
	}
	err := s.setSeqForMessages(subscriber, messages)
	if err != nil {
		return err
	}

	data, err := EncodeCMDAppendMessagesOfUser(subscriber, messages)
	if err != nil {
		return err
	}
	cmd := NewCMD(CMDAppendMessagesOfUser, data)
	cmdData, err := cmd.Marshal()
	if err != nil {
		return err
	}

	slotId := s.getChannelSlotId(subscriber)
	return s.opts.Cluster.ProposeToSlot(slotId, cmdData)
}

func (s *Store) setSeqForMessages(subscriber string, messages []wkstore.Message) error {
	s.lock.Lock(subscriber)
	defer s.lock.Unlock(subscriber)
	maxSeq, _, err := s.db.GetChannelMaxMessageSeq(subscriber, wkproto.ChannelTypePerson)
	if err != nil {
		return err
	}
	for i, msg := range messages {
		msg.SetSeq(maxSeq + uint32(i) + 1)
	}
	// 保存当前用户的最大消息序号（用户的消息messageSeq不要求严格递增连续，只要递增就行，所以这里保存成功后续执行失败，也不会影响程序正常逻辑）
	err = s.db.SaveChannelMaxMessageSeq(subscriber, wkproto.ChannelTypePerson, maxSeq+uint32(len(messages)))
	if err != nil {
		return err
	}
	return nil

}

func (s *Store) SyncMessageOfUser(uid string, messageSeq uint32, limit uint32) ([]wkstore.Message, error) {
	return s.db.GetMessagesOfUserQueue(uid, messageSeq, limit)
}

func (s *Store) DeleteChannelAndClearMessages(channelID string, channelType uint8) error {
	return nil
}

// 获取频道的槽id
func (s *Store) getChannelSlotId(channelId string) uint32 {
	return wkutil.GetSlotNum(int(s.opts.SlotCount), channelId)
}

type MessageShardLogStorage struct {
	db wkdb.DB
	wklog.Log
}

func NewMessageShardLogStorage(db wkdb.DB) *MessageShardLogStorage {
	return &MessageShardLogStorage{
		db:  db,
		Log: wklog.NewWKLog("MessageShardLogStorage"),
	}
}

func (m *MessageShardLogStorage) Open() error {

	return m.db.Open()
}

func (m *MessageShardLogStorage) Close() error {
	return m.db.Close()
}

func (s *MessageShardLogStorage) AppendLog(shardNo string, logs []replica.Log) error {
	channelID, channelType := cluster.ChannelFromChannelKey(shardNo)
	lastIndex, err := s.LastIndex(shardNo)
	if err != nil {
		s.Error("get last index err", zap.Error(err))
		return err
	}
	lastLog := logs[len(logs)-1]
	if lastLog.Index <= lastIndex {
		s.Panic("log index is less than last index", zap.Uint64("logIndex", lastLog.Index), zap.Uint64("lastIndex", lastIndex))
		return nil
	}

	msgs := make([]wkdb.Message, len(logs))
	for idx, log := range logs {
		msg := &wkdb.Message{}
		err = msg.Unmarshal(log.Data)
		if err != nil {
			return err
		}
		msg.MessageSeq = uint32(log.Index)
		msg.Term = uint64(log.Term)
		msgs[idx] = *msg
	}

	err = s.db.AppendMessages(channelID, channelType, msgs)
	if err != nil {
		s.Error("AppendMessages err", zap.Error(err))
		return err
	}

	return nil
}

// 获取日志
func (s *MessageShardLogStorage) Logs(shardNo string, startLogIndex, endLogIndex uint64, limitSize uint64) ([]replica.Log, error) {

	channelID, channelType := cluster.ChannelFromChannelKey(shardNo)
	var (
		messages []wkdb.Message
		err      error
	)
	messages, err = s.db.LoadNextRangeMsgsForSize(channelID, channelType, startLogIndex, endLogIndex, limitSize)
	if err != nil {
		return nil, err
	}

	if len(messages) == 0 {
		return nil, nil
	}
	logs := make([]replica.Log, len(messages))
	for i, msg := range messages {
		data, err := msg.Marshal()
		if err != nil {
			return nil, err
		}
		logs[i] = replica.Log{
			MessageId: uint64(msg.MessageID),
			Index:     uint64(msg.MessageSeq),
			Term:      uint32(msg.Term),
			Data:      data,
		}
	}
	s.Debug("get logs", zap.String("shardNo", shardNo), zap.Uint64("startLogIndex", startLogIndex), zap.Uint64("endLogIndex", endLogIndex), zap.Int("logCount", len(logs)), zap.Uint64("firstLogIndex", logs[0].Index), zap.Uint64("lastLogIndex", logs[len(logs)-1].Index))
	return logs, nil
}

func (s *MessageShardLogStorage) TruncateLogTo(shardNo string, index uint64) error {
	channelId, channelType := cluster.ChannelFromChannelKey(shardNo)
	return s.db.TruncateLogTo(channelId, channelType, index)
}

// 最后一条日志的索引
func (s *MessageShardLogStorage) LastIndex(shardNo string) (uint64, error) {
	channelId, channelType := cluster.ChannelFromChannelKey(shardNo)
	lastMsgSeq, err := s.db.GetChannelLastMessageSeq(channelId, channelType)
	if err != nil {
		return 0, err
	}
	return uint64(lastMsgSeq), nil

}

func (s *MessageShardLogStorage) SetLastIndex(shardNo string, index uint64) error {
	channelId, channelType := cluster.ChannelFromChannelKey(shardNo)
	return s.db.SetChannelLastMessageSeq(channelId, channelType, index)
}

// 获取第一条日志的索引
func (s *MessageShardLogStorage) FirstIndex(shardNo string) (uint64, error) {
	return 0, nil
}

// 设置成功被状态机应用的日志索引
func (s *MessageShardLogStorage) SetAppliedIndex(shardNo string, index uint64) error {
	return nil
}

func (s *MessageShardLogStorage) AppliedIndex(shardNo string) (uint64, error) {
	return 0, nil
}

func (s *MessageShardLogStorage) LastIndexAndAppendTime(shardNo string) (uint64, uint64, error) {
	channelId, channelType := cluster.ChannelFromChannelKey(shardNo)
	lastMsgSeq, err := s.db.GetChannelLastMessageSeq(channelId, channelType)
	if err != nil {
		return 0, 0, err
	}
	return uint64(lastMsgSeq), 0, nil
}

// 是用户自己的频道
func IsUserOwnChannel(channelId string, channelType uint8) bool {
	if channelType == wkproto.ChannelTypePerson {
		return !strings.Contains(channelId, "@")
	}
	return false
}
