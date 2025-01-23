package store

import (
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/lni/goutils/syncutil"
)

type Store struct {
	opts *Options
	wklog.Log

	wdb wkdb.DB

	channelCfgCh chan *channelCfgReq
	stopper      *syncutil.Stopper
}

func New(opts *Options) *Store {
	s := &Store{
		opts:         opts,
		Log:          wklog.NewWKLog("store"),
		wdb:          opts.DB,
		channelCfgCh: make(chan *channelCfgReq, 2048),
		stopper:      syncutil.NewStopper(),
	}

	return s
}

func (s *Store) NextPrimaryKey() uint64 {
	return s.wdb.NextPrimaryKey()
}

func (s *Store) DB() wkdb.DB {
	return s.wdb
}

func (s *Store) Start() error {
	for i := 0; i < 50; i++ {
		go s.loopSaveChannelClusterConfig()
	}
	// s.stopper.RunWorker(s.loopSaveChannelClusterConfig)
	return nil
}

func (s *Store) Stop() {
	s.stopper.Stop()
}

type channelCfgReq struct {
	cfg   wkdb.ChannelClusterConfig
	errCh chan error
}
