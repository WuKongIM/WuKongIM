package server

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig/pb"
	"github.com/WuKongIM/WuKongIM/pkg/network"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

// SystemUIDManager System uid management
type SystemUIDManager struct {
	datasource IDatasource
	s          *Server
	systemUIDs sync.Map
	loaded     atomic.Bool
	wklog.Log
}

// NewSystemUIDManager NewSystemUIDManager
func NewSystemUIDManager(s *Server) *SystemUIDManager {

	return &SystemUIDManager{
		s:          s,
		datasource: NewDatasource(s),
		systemUIDs: sync.Map{},
		Log:        wklog.NewWKLog("SystemUIDManager"),
	}
}

// LoadIfNeed LoadIfNeed
func (s *SystemUIDManager) LoadIfNeed() error {
	if s.loaded.Load() {
		return nil
	}

	var systemUIDs []string
	var err error
	if s.s.opts.HasDatasource() {
		systemUIDs, err = s.datasource.GetSystemUIDs()
		if err != nil {
			return err
		}
	} else {
		systemUIDs, err = s.getOrRequestSystemUids()
		if err != nil {
			return err
		}
	}
	s.loaded.Store(true)
	if len(systemUIDs) > 0 {
		for _, systemUID := range systemUIDs {
			s.systemUIDs.Store(systemUID, true)
		}
	}
	return nil
}

// SystemUID Is it a system account?
func (s *SystemUIDManager) SystemUID(uid string) bool {
	err := s.LoadIfNeed()
	if err != nil {
		s.Error("LoadIfNeed error", zap.Error(err))
		return false
	}

	if uid == s.s.opts.SystemUID { // 内置系统账号
		return true
	}

	_, ok := s.systemUIDs.Load(uid)
	return ok
}

// AddSystemUids AddSystemUID
func (s *SystemUIDManager) AddSystemUids(uids []string) error {
	if len(uids) == 0 {
		return nil
	}
	err := s.s.store.AddSystemUids(uids)
	if err != nil {
		return err
	}
	s.AddSystemUidsToCache(uids)
	return nil
}

// AddSystemUidsToCache 添加系统账号到缓存中
func (s *SystemUIDManager) AddSystemUidsToCache(uids []string) {
	for _, uid := range uids {
		s.systemUIDs.Store(uid, true)
	}
}

// RemoveSystemUID RemoveSystemUID
func (s *SystemUIDManager) RemoveSystemUids(uids []string) error {
	if len(uids) == 0 {
		return nil
	}
	err := s.s.store.RemoveSystemUids(uids)
	if err != nil {
		return err
	}
	for _, uid := range uids {
		s.systemUIDs.Delete(uid)
	}
	return nil
}

func (s *SystemUIDManager) RemoveSystemUidsFromCache(uids []string) {
	for _, uid := range uids {
		s.systemUIDs.Delete(uid)
	}
}

func (s *SystemUIDManager) getOrRequestSystemUids() ([]string, error) {

	var slotId uint32 = 0
	nodeInfo, err := s.s.cluster.SlotLeaderNodeInfo(slotId)
	if err != nil {
		return nil, err
	}
	if nodeInfo.Id == s.s.opts.Cluster.NodeId {
		return s.s.store.GetSystemUids()
	}

	return s.requestSystemUids(nodeInfo)
}

func (s *SystemUIDManager) requestSystemUids(nodeInfo *pb.Node) ([]string, error) {

	resp, err := network.Get(fmt.Sprintf("%s%s", nodeInfo.ApiServerAddr, "/user/systemuids"), nil, nil)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("requestSystemUids error: %s", resp.Body)
	}

	var systemUIDs []string
	err = wkutil.ReadJSONByByte([]byte(resp.Body), &systemUIDs)
	if err != nil {
		return nil, err
	}
	return systemUIDs, nil
}
