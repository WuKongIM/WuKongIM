package manager

import (
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/node/types"
	"github.com/WuKongIM/WuKongIM/pkg/network"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
)

// SystemUIDManager System uid management
type SystemAccountManager struct {
	systemUIDs sync.Map
	loaded     atomic.Bool
	wklog.Log
}

// SystemAccountManager SystemAccountManager
func NewSystemAccountManager() *SystemAccountManager {

	return &SystemAccountManager{
		systemUIDs: sync.Map{},
		Log:        wklog.NewWKLog("SystemUIDManager"),
	}
}

// LoadIfNeed LoadIfNeed
func (s *SystemAccountManager) LoadIfNeed() error {
	if s.loaded.Load() {
		return nil
	}

	var systemUIDs []string
	var err error
	if options.G.HasDatasource() {
		systemUIDs, err = s.getSystemUIDsFromDatasource()
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

// IsSystemAccount Is it a system account?
func (s *SystemAccountManager) IsSystemAccount(uid string) bool {
	err := s.LoadIfNeed()
	if err != nil {
		s.Error("LoadIfNeed error", zap.Error(err))
		return false
	}

	if uid == options.G.SystemUID { // 内置系统账号
		return true
	}

	_, ok := s.systemUIDs.Load(uid)
	return ok
}

// AddSystemUids AddSystemUID
func (s *SystemAccountManager) AddSystemUids(uids []string) error {
	if len(uids) == 0 {
		return nil
	}
	err := service.Store.AddSystemUids(uids)
	if err != nil {
		return err
	}
	s.AddSystemUidsToCache(uids)
	return nil
}

// AddSystemUidsToCache 添加系统账号到缓存中
func (s *SystemAccountManager) AddSystemUidsToCache(uids []string) {
	for _, uid := range uids {
		s.systemUIDs.Store(uid, true)
	}
}

// RemoveSystemUID RemoveSystemUID
func (s *SystemAccountManager) RemoveSystemUids(uids []string) error {
	if len(uids) == 0 {
		return nil
	}
	err := service.Store.RemoveSystemUids(uids)
	if err != nil {
		return err
	}
	for _, uid := range uids {
		s.systemUIDs.Delete(uid)
	}
	return nil
}

func (s *SystemAccountManager) RemoveSystemUidsFromCache(uids []string) {
	for _, uid := range uids {
		s.systemUIDs.Delete(uid)
	}
}

func (s *SystemAccountManager) getOrRequestSystemUids() ([]string, error) {

	var slotId uint32 = 0
	nodeInfo := service.Cluster.SlotLeaderNodeInfo(slotId)
	if nodeInfo == nil {
		return nil, errors.New("getOrRequestSystemUids: slot leader node not found")
	}
	if nodeInfo.Id == options.G.Cluster.NodeId {
		return service.Store.GetSystemUids()
	}

	return s.requestSystemUids(nodeInfo)
}

func (s *SystemAccountManager) requestSystemUids(nodeInfo *types.Node) ([]string, error) {

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

// getSystemUIDsFromDatasource 获取系统账号从数据源
func (s *SystemAccountManager) getSystemUIDsFromDatasource() ([]string, error) {
	result, err := s.requestCMD("getSystemUIDs", map[string]interface{}{})
	if err != nil {
		return nil, err
	}
	var uids []string
	err = wkutil.ReadJSONByByte([]byte(result), &uids)
	if err != nil {
		return nil, err
	}
	return uids, nil
}

func (s *SystemAccountManager) requestCMD(cmd string, param map[string]interface{}) (string, error) {
	dataMap := map[string]interface{}{
		"cmd": cmd,
	}
	if param != nil {
		dataMap["data"] = param
	}
	resp, err := network.Post(options.G.Datasource.Addr, []byte(wkutil.ToJSON(dataMap)), nil)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("http状态码错误！[%d]", resp.StatusCode)
	}

	return resp.Body, nil
}
