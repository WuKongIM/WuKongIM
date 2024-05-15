package server

import (
	"sync"

	"go.uber.org/atomic"
)

// SystemUIDManager System uid management
type SystemUIDManager struct {
	datasource IDatasource
	s          *Server
	systemUIDs sync.Map
	loaded     atomic.Bool
}

// NewSystemUIDManager NewSystemUIDManager
func NewSystemUIDManager(s *Server) *SystemUIDManager {

	return &SystemUIDManager{
		s:          s,
		datasource: NewDatasource(s),
		systemUIDs: sync.Map{},
	}
}

// LoadIfNeed LoadIfNeed
func (s *SystemUIDManager) LoadIfNeed() error {
	if s.loaded.Load() {
		return nil
	}

	if !s.s.opts.HasDatasource() {
		return nil
	}

	var err error
	systemUIDs, err := s.datasource.GetSystemUIDs()
	if err != nil {
		return err
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
	_, ok := s.systemUIDs.Load(uid)
	return ok
}

// AddSystemUID AddSystemUID
func (s *SystemUIDManager) AddSystemUIDs(uids []string) error {
	if len(uids) == 0 {
		return nil
	}
	err := s.s.store.AddSystemUIDs(uids)
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
	err := s.s.store.RemoveSystemUIDs(uids)
	if err != nil {
		return err
	}
	for _, uid := range uids {
		s.systemUIDs.Delete(uid)
	}
	return nil
}
