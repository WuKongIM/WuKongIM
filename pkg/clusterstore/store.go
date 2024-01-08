package clusterstore

import (
	"fmt"
	"os"

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

	err := os.MkdirAll(opts.DataDir, os.ModePerm)
	if err != nil {
		s.Panic("create data dir err", zap.Error(err))
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

func (s *Store) GetPeerInFlightData() ([]*wkstore.PeerInFlightDataModel, error) {
	return s.db.GetPeerInFlightData()
}

func (s *Store) ClearPeerInFlightData() error {
	return s.db.ClearPeerInFlightData()
}

func (s *Store) AddPeerInFlightData(data []*wkstore.PeerInFlightDataModel) error {
	return s.db.AddPeerInFlightData(data)
}

func (s *Store) AddSystemUIDs(uids []string) error {
	return s.db.AddSystemUIDs(uids)
}

func (s *Store) RemoveSystemUIDs(uids []string) error {
	return s.db.RemoveSystemUIDs(uids)
}
