package server

import (
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
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
		systemUIDs, err = s.s.store.GetSystemUids()
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
	_, ok := s.systemUIDs.Load(uid)
	return ok
}

// AddSystemUID AddSystemUID
func (s *SystemUIDManager) AddSystemUIDs(uids []string) error {
	if len(uids) == 0 {
		return nil
	}
	err := s.s.store.AddSystemUids(uids)
	if err != nil {
		return err
	}
	for _, uid := range uids {
		s.systemUIDs.Store(uid, true)
	}
	return nil
}

// RemoveSystemUID RemoveSystemUID
func (s *SystemUIDManager) RemoveSystemUIDs(uids []string) error {
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
