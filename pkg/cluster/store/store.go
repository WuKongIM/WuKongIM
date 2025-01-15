package store

import (
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type Store struct {
	opts *Options
	wklog.Log

	wdb wkdb.DB
}

func New(opts *Options) *Store {
	s := &Store{
		opts: opts,
		Log:  wklog.NewWKLog("store"),
		wdb:  opts.DB,
	}

	return s
}

func (s *Store) NextPrimaryKey() uint64 {
	return s.wdb.NextPrimaryKey()
}

func (s *Store) DB() wkdb.DB {
	return s.wdb
}
