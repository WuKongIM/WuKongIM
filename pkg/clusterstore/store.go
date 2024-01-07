package clusterstore

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
	"go.uber.org/zap"
)

type Store struct {
	opts *Options
	db   *wkstore.FileStore
	wklog.Log

	messageShardLogStorage *MessageShardLogStorage
}

func NewStore(opts *Options) *Store {

	s := &Store{
		opts: opts,
		Log:  wklog.NewWKLog(fmt.Sprintf("clusterStore[%d]", opts.NodeID)),
	}
	storeCfg := wkstore.NewStoreConfig()
	storeCfg.DataDir = opts.DataDir
	storeCfg.SlotNum = int(opts.SlotCount)
	storeCfg.DecodeMessageFnc = opts.DecodeMessageFnc
	s.db = wkstore.NewFileStore(storeCfg)
	s.messageShardLogStorage = NewMessageShardLogStorage(s.db)
	return s
}

func (s *Store) Open() error {
	err := s.db.Open()
	return err
}

func (s *Store) Close() {
	err := s.db.Close()
	if err != nil {
		s.Warn("close message storage err", zap.Error(err))
	}
}

// 频道元数据应用
func (s *Store) OnMetaApply(channelID string, channelType uint8, logs []replica.Log) error {
	for _, lg := range logs {
		err := s.onMetaApply(channelID, channelType, lg)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) onMetaApply(channelID string, channelType uint8, log replica.Log) error {
	s.Debug("OnMetaApply", zap.String("channelID", channelID), zap.Uint8("channelType", channelType), zap.Uint64("index", log.Index), zap.ByteString("data", log.Data))
	cmd := &CMD{}
	err := cmd.Unmarshal(log.Data)
	if err != nil {
		s.Error("unmarshal cmd err", zap.Error(err), zap.String("channelID", channelID), zap.Uint8("channelType", channelType), zap.Uint64("index", log.Index), zap.ByteString("data", log.Data))
		return err
	}
	switch cmd.CmdType {
	case CMDAddSubscribers:
		return s.handleAddSubscribers(channelID, channelType, cmd)
	case CMDRemoveSubscribers:
		return s.handleRemoveSubscribers(channelID, channelType, cmd)

	}
	return nil
}

func (s *Store) handleAddSubscribers(channelID string, channelType uint8, cmd *CMD) error {
	s.Debug("handleAddSubscribers", zap.String("channelID", channelID), zap.Uint8("channelType", channelType), zap.ByteString("data", cmd.Data))
	subscribers, err := cmd.DecodeSubscribers()
	if err != nil {
		s.Error("decode subscribers err", zap.Error(err), zap.String("channelID", channelID), zap.Uint8("channelType", channelType), zap.ByteString("data", cmd.Data))
		return err
	}
	err = s.db.AddSubscribers(channelID, channelType, subscribers)
	return err
}

func (s *Store) handleRemoveSubscribers(channelID string, channelType uint8, cmd *CMD) error {
	subscribers, err := cmd.DecodeSubscribers()
	if err != nil {
		return err
	}
	err = s.db.RemoveSubscribers(channelID, channelType, subscribers)
	return err
}
