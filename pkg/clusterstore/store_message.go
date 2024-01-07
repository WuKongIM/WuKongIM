package clusterstore

import (
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
	"go.uber.org/zap"
)

func (s *Store) GetMessageShardLogStorage() *MessageShardLogStorage {
	return s.messageShardLogStorage
}

func (s *Store) AppendMessage(channelID string, channelType uint8, msg wkstore.Message) error {
	err := s.opts.Cluster.ProposeMessageToChannel(channelID, channelType, msg.Encode())
	return err
}

func (s *Store) LoadNextRangeMsgs(channelID string, channelType uint8, startMessageSeq, endMessageSeq uint32, limit int) ([]wkstore.Message, error) {
	return s.db.LoadNextRangeMsgs(channelID, channelType, startMessageSeq, endMessageSeq, limit)
}

type MessageShardLogStorage struct {
	db *wkstore.FileStore
	wklog.Log
}

func NewMessageShardLogStorage(db *wkstore.FileStore) *MessageShardLogStorage {
	return &MessageShardLogStorage{
		db:  db,
		Log: wklog.NewWKLog("MessageShardLogStorage"),
	}
}

func (s *MessageShardLogStorage) AppendLog(shardNo string, log replica.Log) error {
	channelID, channelType := cluster.GetChannelFromChannelKey(shardNo)
	s.Debug("AppendLog", zap.String("channelID", channelID), zap.Uint8("channelType", channelType), zap.Uint64("index", log.Index), zap.ByteString("data", log.Data))
	lastIndex, err := s.LastIndex(shardNo)
	if err != nil {
		s.Error("get last index err", zap.Error(err))
		return err
	}
	if log.Index <= lastIndex {
		s.Warn("log index is less than last index", zap.Uint64("logIndex", log.Index), zap.Uint64("lastIndex", lastIndex))
		return nil
	}
	msg, err := s.db.StoreConfig().DecodeMessageFnc(log.Data)
	if err != nil {
		s.Error("decode message err", zap.Error(err))
		return err
	}
	msg.SetSeq(uint32(log.Index))
	_, err = s.db.AppendMessages(channelID, channelType, []wkstore.Message{msg})
	if err != nil {
		s.Error("AppendMessages err", zap.Error(err))
	}
	return err
}

// 获取日志
func (s *MessageShardLogStorage) GetLogs(shardNo string, startLogIndex uint64, limit uint32) ([]replica.Log, error) {

	channelID, channelType := cluster.GetChannelFromChannelKey(shardNo)
	if startLogIndex > 0 {
		startLogIndex = startLogIndex - 1
	}
	messages, err := s.db.LoadNextRangeMsgs(channelID, channelType, uint32(startLogIndex), 0, int(limit))
	if err != nil {
		return nil, err
	}
	if len(messages) == 0 {
		return nil, nil
	}
	logs := make([]replica.Log, len(messages))
	for i, msg := range messages {
		logs[i] = replica.Log{
			Index: uint64(msg.GetSeq()),
			Data:  msg.Encode(),
		}
	}
	return logs, nil
}

// 最后一条日志的索引
func (s *MessageShardLogStorage) LastIndex(shardNo string) (uint64, error) {
	channelID, channelType := cluster.GetChannelFromChannelKey(shardNo)
	msgSeq, err := s.db.GetLastMsgSeq(channelID, channelType)
	return uint64(msgSeq), err
}

// 获取第一条日志的索引
func (s *MessageShardLogStorage) FirstIndex(shardNo string) (uint64, error) {
	return 0, nil
}

// 设置成功被状态机应用的日志索引
func (s *MessageShardLogStorage) SetAppliedIndex(shardNo string, index uint64) error {
	return nil
}
