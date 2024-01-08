package clusterstore

import (
	"fmt"

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
