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
	s.wdb = wkdb.NewWukongDB(wkdb.NewOptions(wkdb.WithDir(opts.DataDir), wkdb.WithNodeId(opts.NodeID)))
	s.messageShardLogStorage = NewMessageShardLogStorage(s.wdb)
	return s
}

func (s *Store) Open() error {
	s.lock.StartCleanLoop()
	err := s.wdb.Open()
	if err != nil {
		return err
	}
	return nil
}

func (s *Store) Close() {
	err := s.wdb.Close()
	if err != nil {
		s.Warn("close message storage err", zap.Error(err))
	}
	s.lock.StopCleanLoop()
}

func (s *Store) GetPeerInFlightData() ([]*wkstore.PeerInFlightDataModel, error) {
	// return s.db.GetPeerInFlightData()
	return nil, nil
}

func (s *Store) ClearPeerInFlightData() error {
	return nil
}

func (s *Store) AddPeerInFlightData(data []*wkstore.PeerInFlightDataModel) error {
	return nil
}

func (s *Store) AddSystemUIDs(uids []string) error {
	// return s.db.AddSystemUIDs(uids)
	return nil
}

func (s *Store) RemoveSystemUIDs(uids []string) error {
	return nil
	// return s.db.RemoveSystemUIDs(uids)
}

func (s *Store) GetIPBlacklist() ([]string, error) {
	// return s.db.GetIPBlacklist()
	return nil, nil
}

func (s *Store) RemoveIPBlacklist(ips []string) error {
	// return s.db.RemoveIPBlacklist(ips)
	return nil
}

func (s *Store) AddIPBlacklist(ips []string) error {
	// return s.db.AddIPBlacklist(ips)
	return nil
}
