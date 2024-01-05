package cluster

import (
	"errors"
	"fmt"
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/cockroachdb/pebble"
	"go.uber.org/zap"
)

type slotStateMachine struct {
	db   *pebble.DB
	path string
	wklog.Log
	wo *pebble.WriteOptions
}

func newSlotStateMachine(path string) *slotStateMachine {
	return &slotStateMachine{
		path: path,
		Log:  wklog.NewWKLog("slotStateMachine"),
		wo:   &pebble.WriteOptions{Sync: true},
	}
}

func (s *slotStateMachine) open() error {
	db, err := pebble.Open(s.path, &pebble.Options{})
	if err != nil {
		return err
	}
	s.db = db
	return nil
}

func (s *slotStateMachine) close() error {
	return s.db.Close()
}

func (s *slotStateMachine) applyLogs(slotID uint32, logs []replica.Log) (uint64, error) {

	for _, lg := range logs {
		cmd := &CMD{}
		err := cmd.Unmarshal(lg.Data)
		if err != nil {
			return 0, err
		}
		switch cmd.CmdType {
		case CmdTypeSetChannelInfo: // 设置频道信息
			s.handleSetChannelInfo(slotID, cmd)

		}
	}
	return logs[len(logs)-1].Index, nil
}

func (s *slotStateMachine) handleSetChannelInfo(slotID uint32, cmd *CMD) {
	channelInfo := &ChannelInfo{}
	err := channelInfo.Unmarshal(cmd.Data)
	if err != nil {
		s.Error("handleSetChannelInfo failed", zap.Error(err), zap.Uint32("slotID", slotID))
		return
	}
	err = s.db.Set(s.getSetChannelInfoKey(channelInfo.ChannelID, channelInfo.ChannelType), cmd.Data, s.wo)
	if err != nil {
		s.Error("handleSetChannelInfo failed", zap.Error(err), zap.Uint32("slotID", slotID))
		return
	}
}

func (s *slotStateMachine) getSetChannelInfoKey(channelID string, channelType uint8) []byte {
	return []byte(fmt.Sprintf("channelinfo_%d_%s", channelType, channelID))
}

func (s *slotStateMachine) getChannelInfo(slotID uint32, channelID string, channelType uint8) (*ChannelInfo, error) {
	data, closer, err := s.db.Get(s.getSetChannelInfoKey(channelID, channelType))
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	defer closer.Close()
	channelInfo := &ChannelInfo{}
	err = channelInfo.Unmarshal(data)
	if err != nil {
		return nil, err
	}
	return channelInfo, nil
}

func (s *slotStateMachine) saveChannelSyncInfos(channelID string, channelType uint8, replicas []*replica.SyncInfo) error {

	return nil

}

func (s *slotStateMachine) saveSlotSyncInfos(slotID uint32, syncInfos []*replica.SyncInfo) error {
	if len(syncInfos) == 0 {
		return nil
	}
	for _, syncInfo := range syncInfos {
		data, err := syncInfo.Marshal()
		if err != nil {
			return err
		}
		slotSyncKey := s.getSlotSyncInfoKey(slotID, syncInfo.NodeID)
		err = s.db.Set(slotSyncKey, data, s.wo)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *slotStateMachine) saveSlotSyncInfo(slotID uint32, syncInfo *replica.SyncInfo) error {
	data, err := syncInfo.Marshal()
	if err != nil {
		return err
	}
	slotSyncKey := s.getSlotSyncInfoKey(slotID, syncInfo.NodeID)
	err = s.db.Set(slotSyncKey, data, s.wo)
	if err != nil {
		return err
	}
	return nil
}

func (s *slotStateMachine) getSlotSyncInfo(slotID uint32, nodeID uint64) (*replica.SyncInfo, error) {
	data, closer, err := s.db.Get(s.getSlotSyncInfoKey(slotID, nodeID))
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	defer closer.Close()
	sc := &replica.SyncInfo{}
	err = sc.Unmarshal(data)
	if err != nil {
		return nil, err
	}

	return sc, nil
}

func (s *slotStateMachine) getSlotSyncInfos(slotID uint32) ([]*replica.SyncInfo, error) {
	lowKey := s.getSlotSyncInfoKey(slotID, 0)
	highKey := s.getSlotSyncInfoKey(slotID, math.MaxUint64)
	iter := s.db.NewIter(&pebble.IterOptions{
		LowerBound: lowKey,
		UpperBound: highKey,
	})
	defer iter.Close()

	syncInfos := make([]*replica.SyncInfo, 0)
	for iter.First(); iter.Valid(); iter.Next() {
		sc := &replica.SyncInfo{}
		err := sc.Unmarshal(iter.Value())
		if err != nil {
			return nil, err
		}
		syncInfos = append(syncInfos, sc)
	}
	return syncInfos, nil
}

func (s *slotStateMachine) getSlotSyncInfoKey(slotID uint32, nodeID uint64) []byte {
	keyStr := fmt.Sprintf("slotsyncs_%d_%d", slotID, nodeID)
	return []byte(keyStr)
}
