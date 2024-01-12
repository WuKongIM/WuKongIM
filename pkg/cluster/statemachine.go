package cluster

import (
	"errors"
	"fmt"
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/cockroachdb/pebble"
	"go.uber.org/zap"
)

type LogKind uint8 // 日志种类

const (
	LogKindMeta LogKind = iota
	LogKindMessage
)

type stateMachine struct {
	db   *pebble.DB
	path string
	wklog.Log
	wo *pebble.WriteOptions
	s  *Server
}

func newStateMachine(path string, s *Server) *stateMachine {
	return &stateMachine{
		path: path,
		Log:  wklog.NewWKLog("stateMachine"),
		wo:   &pebble.WriteOptions{Sync: true},
		s:    s,
	}
}

func (s *stateMachine) open() error {
	db, err := pebble.Open(s.path, &pebble.Options{})
	if err != nil {
		return err
	}
	s.db = db
	return nil
}

func (s *stateMachine) close() error {
	return s.db.Close()
}

func (s *stateMachine) applySlotLogs(slotID uint32, logs []replica.Log) (uint64, error) {

	for _, lg := range logs {
		cmd := &CMD{}
		err := cmd.Unmarshal(lg.Data)
		if err != nil {
			return 0, err
		}
		switch cmd.CmdType {
		case CmdTypeSetChannelInfo: // 设置频道集群信息
			s.handleSetChannelClusterInfo(cmd)

		}
	}
	return logs[len(logs)-1].Index, nil
}

func (s *stateMachine) handleSetChannelClusterInfo(cmd *CMD) {
	channelInfo := &ChannelClusterInfo{}
	err := channelInfo.Unmarshal(cmd.Data)
	if err != nil {
		s.Error("handleSetChannelClusterInfo failed", zap.Error(err), zap.String("channelID", channelInfo.ChannelID), zap.Uint8("channelType", channelInfo.ChannelType))
		return
	}
	err = s.saveChannelClusterInfo(channelInfo)
	if err != nil {
		s.Error("handleSetChannelClusterInfo failed", zap.Error(err), zap.String("channelID", channelInfo.ChannelID), zap.Uint8("channelType", channelInfo.ChannelType))
		return
	}
}

func (s *stateMachine) getChannelClusterInfo(channelID string, channelType uint8) (*ChannelClusterInfo, error) {
	data, closer, err := s.db.Get(s.getChannelClusterInfoKey(channelID, channelType))
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	defer closer.Close()
	channelInfo := &ChannelClusterInfo{}
	err = channelInfo.Unmarshal(data)
	if err != nil {
		return nil, err
	}
	return channelInfo, nil
}

func (s *stateMachine) getChannelClusterInfos(offset, limit int) ([]*ChannelClusterInfo, error) {
	lowKey := s.getChannelClusterInfoLowKey()
	highKey := s.getChannelClusterInfoHighKey()

	iter := s.db.NewIter(&pebble.IterOptions{
		LowerBound: lowKey,
		UpperBound: highKey,
	})
	defer iter.Close()

	channelInfos := make([]*ChannelClusterInfo, 0)
	i := 0

	for iter.First(); iter.Valid(); iter.Next() {
		channelInfo := &ChannelClusterInfo{}
		err := channelInfo.Unmarshal(iter.Value())
		if err != nil {
			return nil, err
		}
		if i < offset {
			i++
			continue
		}

		if limit != 0 && i >= offset+limit {
			break
		}
		channelInfos = append(channelInfos, channelInfo)

	}
	return channelInfos, nil

}

func (s *stateMachine) saveChannelClusterInfo(channelInfo *ChannelClusterInfo) error {
	data, err := channelInfo.Marshal()
	if err != nil {
		return err
	}
	err = s.db.Set(s.getChannelClusterInfoKey(channelInfo.ChannelID, channelInfo.ChannelType), data, s.wo)
	if err != nil {
		return err
	}
	return nil
}

func (s *stateMachine) saveChannelSyncInfo(channelID string, channelType uint8, kind LogKind, syncInfo *replica.SyncInfo) error {
	data, err := syncInfo.Marshal()
	if err != nil {
		return err
	}
	channelSyncKey := s.getChannelSyncInfoKey(channelID, channelType, kind, syncInfo.NodeID)
	err = s.db.Set(channelSyncKey, data, s.wo)
	if err != nil {
		return err
	}
	return nil

}

func (s *stateMachine) getChannelSyncInfo(channelID string, channelType uint8, kind LogKind, nodeID uint64) (*replica.SyncInfo, error) {
	data, closer, err := s.db.Get(s.getChannelSyncInfoKey(channelID, channelType, kind, nodeID))
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

func (s *stateMachine) getChannelSyncInfos(channelID string, channelType uint8, kind LogKind) ([]*replica.SyncInfo, error) {
	lowKey := s.getChannelSyncInfoLowKey(channelID, channelType, kind)
	highKey := s.getChannelSyncInfoHighKey(channelID, channelType, kind)
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

func (s *stateMachine) saveSlotSyncInfos(slotID uint32, syncInfos []*replica.SyncInfo) error {
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

func (s *stateMachine) saveSlotSyncInfo(slotID uint32, syncInfo *replica.SyncInfo) error {
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

func (s *stateMachine) getSlotSyncInfo(slotID uint32, nodeID uint64) (*replica.SyncInfo, error) {
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

func (s *stateMachine) getSlotSyncInfos(slotID uint32) ([]*replica.SyncInfo, error) {
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

func (s *stateMachine) getSlotSyncInfoKey(slotID uint32, nodeID uint64) []byte {
	keyStr := fmt.Sprintf("/slotsyncs/slots/%s/nodes/%020d", s.getSlotFillFormat(slotID), nodeID)
	return []byte(keyStr)
}

func (s *stateMachine) getChannelSyncInfoKey(channelID string, channelType uint8, kind LogKind, nodeID uint64) []byte {
	slotID := s.s.GetSlotID(channelID)
	keyStr := fmt.Sprintf("/channelsyncs/slots/%s/kind/%02d/channels/%03d/%s/nodes/%020d", s.getSlotFillFormat(slotID), kind, channelType, channelID, nodeID)
	return []byte(keyStr)
}

func (s *stateMachine) getChannelSyncInfoHighKey(channelID string, channelType uint8, kind LogKind) []byte {
	slotID := s.s.GetSlotID(channelID)
	var max uint64 = math.MaxUint64
	return []byte(fmt.Sprintf("/channelsyncs/slots/%s/kind/%02d/channels/%03d/%s/nodes/%020d", s.getSlotFillFormat(slotID), kind, channelType, channelID, max))
}

func (s *stateMachine) getChannelSyncInfoLowKey(channelID string, channelType uint8, kind LogKind) []byte {
	slotID := s.s.GetSlotID(channelID)
	return []byte(fmt.Sprintf("/channelsyncs/slots/%s/kind/%02d/channels/%03d/%s/nodes/%020d", s.getSlotFillFormat(slotID), kind, channelType, channelID, 0))
}

func (s *stateMachine) getChannelClusterInfoKey(channelID string, channelType uint8) []byte {
	slotID := s.s.GetSlotID(channelID)
	return []byte(fmt.Sprintf("/channelclusterinfo/slots/%s/channels/%03d/%s", s.getSlotFillFormat(slotID), channelType, channelID))
}

func (s *stateMachine) getChannelClusterInfoLowKey() []byte {
	return []byte(fmt.Sprintf("/channelclusterinfo/slots/%s/channels/", s.getSlotFillFormatMin()))
}

func (s *stateMachine) getChannelClusterInfoHighKey() []byte {
	return []byte(fmt.Sprintf("/channelclusterinfo/slots/%s/channels/", s.getSlotFillFormatMax()))
}

func (s *stateMachine) getSlotFillFormat(slotID uint32) string {
	return wkutil.GetSlotFillFormat(int(slotID), int(s.s.opts.SlotCount))
}

func (s *stateMachine) getSlotFillFormatMax() string {
	return wkutil.GetSlotFillFormat(int(s.s.opts.SlotCount), int(s.s.opts.SlotCount))
}

func (s *stateMachine) getSlotFillFormatMin() string {
	return wkutil.GetSlotFillFormat(0, int(s.s.opts.SlotCount))
}
