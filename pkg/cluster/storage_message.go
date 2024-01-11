package cluster

import (
	"fmt"
	"os"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
	"go.uber.org/zap"
)

type MessageStorage struct {
	dataDir string
	db      *wkstore.FileStore
	wklog.Log
	nodeID uint64
}

func NewMessageStorage(nodeID uint64, dataDir string, slotCount uint32, decodeMessageFnc func(msg []byte) (wkstore.Message, error)) *MessageStorage {
	storeCfg := wkstore.NewStoreConfig()
	storeCfg.DataDir = dataDir
	storeCfg.SlotNum = int(slotCount)
	storeCfg.DecodeMessageFnc = decodeMessageFnc

	m := &MessageStorage{
		nodeID:  nodeID,
		dataDir: dataDir,
		db:      wkstore.NewFileStore(storeCfg),
		Log:     wklog.NewWKLog(fmt.Sprintf("messageStorage[%d]", nodeID)),
	}
	err := os.MkdirAll(dataDir, os.ModePerm)
	if err != nil {
		m.Panic("create data dir err", zap.Error(err))
	}
	return m
}

func (m *MessageStorage) Open() error {
	return m.db.Open()
}

func (m *MessageStorage) Close() {
	err := m.db.Close()
	if err != nil {
		m.Warn("close message storage err", zap.Error(err))
	}
}

func (m *MessageStorage) AppendLog(shardNo string, log replica.Log) error {
	m.Debug("AppendLog", zap.String("shardNo", shardNo), zap.Uint64("index", log.Index), zap.ByteString("data", log.Data))
	lastIndex, err := m.LastIndex(shardNo)
	if err != nil {
		m.Error("get last index err", zap.Error(err))
		return err
	}
	if log.Index <= lastIndex {
		m.Warn("log index is less than last index", zap.Uint64("logIndex", log.Index), zap.Uint64("lastIndex", lastIndex))
		return nil
	}
	msg, err := m.db.StoreConfig().DecodeMessageFnc(log.Data)
	if err != nil {
		m.Error("decode message err", zap.Error(err))
		return err
	}
	msg.SetSeq(uint32(log.Index))
	channelID, channelType := GetChannelFromChannelKey(shardNo)
	_, err = m.db.AppendMessages(channelID, channelType, []wkstore.Message{msg})
	if err != nil {
		m.Error("AppendMessages err", zap.Error(err))
		return err
	}
	return nil
}

func (m *MessageStorage) GetLogs(shardNo string, startLogIndex uint64, limit uint32) ([]replica.Log, error) {
	channelID, channelType := GetChannelFromChannelKey(shardNo)
	if startLogIndex > 0 {
		startLogIndex = startLogIndex - 1
	}
	messages, err := m.db.LoadNextRangeMsgs(channelID, channelType, uint32(startLogIndex), 0, int(limit))
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

func (m *MessageStorage) LastIndex(shardNo string) (uint64, error) {
	channelID, channelType := GetChannelFromChannelKey(shardNo)
	msgSeq, err := m.db.GetLastMsgSeq(channelID, channelType)
	return uint64(msgSeq), err
}

func (m *MessageStorage) FirstIndex(shardNo string) (uint64, error) {
	return 0, nil
}

func (m *MessageStorage) SetAppliedIndex(shardNo string, index uint64) error {

	return nil
}
