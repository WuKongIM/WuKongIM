package clusterstore

import (
	"context"
	"fmt"
	"os"

	"github.com/WuKongIM/WuKongIM/pkg/keylock"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
	"go.uber.org/zap"
)

type Store struct {
	opts *Options
	db   *wkstore.FileStore
	wdb  wkdb.DB
	wklog.Log
	lock *keylock.KeyLock
	ctx  context.Context

	messageShardLogStorage *MessageShardLogStorage
}

func NewStore(opts *Options) *Store {

	s := &Store{
		ctx:  context.Background(),
		opts: opts,
		Log:  wklog.NewWKLog(fmt.Sprintf("clusterStore[%d]", opts.NodeID)),
		lock: keylock.NewKeyLock(),
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
	s.wdb = wkdb.NewPebbleDB(wkdb.NewOptions(wkdb.WithDir(opts.DataDir)))
	s.messageShardLogStorage = NewMessageShardLogStorage(s.wdb)
	return s
}

func (s *Store) Open() error {
	s.lock.StartCleanLoop()
	err := s.wdb.Open()
	if err != nil {
		return err
	}
	err = s.db.Open()
	return err
}

func (s *Store) Close() {
	_ = s.wdb.Close()
	err := s.db.Close()
	if err != nil {
		s.Warn("close message storage err", zap.Error(err))
	}
	s.lock.StopCleanLoop()
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

func (s *Store) GetIPBlacklist() ([]string, error) {
	return s.db.GetIPBlacklist()
}

func (s *Store) RemoveIPBlacklist(ips []string) error {
	return s.db.RemoveIPBlacklist(ips)
}

func (s *Store) AddIPBlacklist(ips []string) error {
	return s.db.AddIPBlacklist(ips)
}
