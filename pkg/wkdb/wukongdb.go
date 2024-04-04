package wkdb

import (
	"encoding/binary"
	"path/filepath"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/bwmarrin/snowflake"
	"github.com/cockroachdb/pebble"
)

var _ DB = (*wukongDB)(nil)

type wukongDB struct {
	db     *pebble.DB
	opts   *Options
	wo     *pebble.WriteOptions
	endian binary.ByteOrder
	wklog.Log
	prmaryKeyGen *snowflake.Node // 消息ID生成器
}

func NewWukongDB(opts *Options) *wukongDB {
	prmaryKeyGen, err := snowflake.NewNode(int64(opts.NodeId))
	if err != nil {
		panic(err)
	}
	return &wukongDB{
		opts:         opts,
		prmaryKeyGen: prmaryKeyGen,
		endian:       binary.BigEndian,
		wo:           &pebble.WriteOptions{},
		Log:          wklog.NewWKLog("wukongDB"),
	}
}

func (wk *wukongDB) Open() error {
	var err error
	wk.db, err = pebble.Open(filepath.Join(wk.opts.DataDir, "wukongimdb"), &pebble.Options{
		FormatMajorVersion: pebble.FormatNewest,
	})
	if err != nil {
		return err
	}
	return nil
}

func (wk *wukongDB) Close() error {
	return wk.db.Close()
}
