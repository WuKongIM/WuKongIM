package store

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
)

type Store struct {
	wdb  wkdb.DB
	opts *Options
}

func New(opts *Options) *Store {
	s := &Store{
		opts: opts,
	}
	s.wdb = wkdb.NewWukongDB(
		wkdb.NewOptions(
			wkdb.WithShardNum(opts.ShardNum),
			wkdb.WithDir(opts.DataDir),
			wkdb.WithNodeId(opts.NodeId),
			wkdb.WithMemTableSize(opts.MemTableSize),
			wkdb.WithSlotCount(int(opts.SlotCount)),
		),
	)
	return s
}

func (s *Store) Open() error {
	return s.wdb.Open()
}

func (s *Store) Close() error {
	return s.wdb.Close()
}

func (s *Store) WKDB() wkdb.DB {
	return s.wdb
}

// ApplyLogs 应用槽日志
func (s *Store) ApplySlotLogs(logs []types.Log) error {
	fmt.Println("logs---->", logs)
	return nil
}
