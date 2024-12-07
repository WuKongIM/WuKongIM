package clusterstore

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/keylock"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/zap"
)

type Store struct {
	opts *Options
	wdb  wkdb.DB
	wklog.Log
	lock *keylock.KeyLock
	ctx  context.Context

	messageShardLogStorage *MessageShardLogStorage

	stopper *syncutil.Stopper

	applyC chan applyReq
}

func NewStore(opts *Options) *Store {

	s := &Store{
		ctx:     context.Background(),
		opts:    opts,
		Log:     wklog.NewWKLog(fmt.Sprintf("clusterStore[%d]", opts.NodeID)),
		lock:    keylock.NewKeyLock(),
		stopper: syncutil.NewStopper(),
		applyC:  make(chan applyReq, 1000),
	}

	err := os.MkdirAll(opts.DataDir, os.ModePerm)
	if err != nil {
		s.Panic("create data dir err", zap.Error(err))
	}

	s.wdb = wkdb.NewWukongDB(
		wkdb.NewOptions(
			wkdb.WithIsCmdChannel(opts.IsCmdChannel),
			wkdb.WithShardNum(opts.Db.ShardNum),
			wkdb.WithDir(opts.DataDir),
			wkdb.WithNodeId(opts.NodeID),
			wkdb.WithMemTableSize(opts.Db.MemTableSize),
			wkdb.WithSlotCount(int(opts.SlotCount)),
		),
	)

	s.messageShardLogStorage = NewMessageShardLogStorage(s.wdb)
	return s
}

func (s *Store) Open() error {

	for i := 0; i < 1; i++ { // 这里不能开启多个applyLoop，否则会导致数据不一致
		s.stopper.RunWorker(s.applyLoop)
	}

	s.lock.StartCleanLoop()
	err := s.wdb.Open()
	if err != nil {
		return err
	}

	err = s.messageShardLogStorage.Open()
	return err
}

func (s *Store) Close() {

	s.stopper.Stop()

	s.Debug("close...")
	s.messageShardLogStorage.Close()
	s.Debug("close1...")
	err := s.wdb.Close()
	if err != nil {
		s.Warn("close message storage err", zap.Error(err))
	}
	s.Debug("close2...")
	s.lock.StopCleanLoop()
	s.Debug("close3...")
}

// func (s *Store) GetPeerInFlightData() ([]*wkstore.PeerInFlightDataModel, error) {
// 	// return s.db.GetPeerInFlightData()
// 	return nil, nil
// }

// func (s *Store) ClearPeerInFlightData() error {
// 	return nil
// }

// func (s *Store) AddPeerInFlightData(data []*wkstore.PeerInFlightDataModel) error {
// 	return nil
// }

func (s *Store) GetSystemUids() ([]string, error) {
	return s.wdb.GetSystemUids()
}

func (s *Store) AddSystemUids(uids []string) error {

	data := EncodeCMDSystemUIDs(uids)
	cmd := NewCMD(CMDSystemUIDsAdd, data)
	cmdData, err := cmd.Marshal()
	if err != nil {
		return err
	}
	var slotId uint32 = 0 // 系统uid默认存储在slot 0上
	_, err = s.opts.Cluster.ProposeDataToSlot(slotId, cmdData)
	return err
}

func (s *Store) RemoveSystemUids(uids []string) error {
	data := EncodeCMDSystemUIDs(uids)
	cmd := NewCMD(CMDSystemUIDsRemove, data)
	cmdData, err := cmd.Marshal()
	if err != nil {
		return err
	}
	var slotId uint32 = 0 // 系统uid默认存储在slot 0上
	_, err = s.opts.Cluster.ProposeDataToSlot(slotId, cmdData)
	return err
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

func (s *Store) DB() wkdb.DB {
	return s.wdb
}

func (s *Store) WithTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(s.ctx, time.Second*10)
}
