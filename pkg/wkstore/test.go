package wkstore

import (
	"io/ioutil"
)

func newTestStoreConfig() *StoreConfig {
	dir, err := ioutil.TempDir("", "commitlog-index")
	if err != nil {
		panic(err)
	}
	cfg := NewStoreConfig()
	cfg.SegmentMaxBytes = 1024 * 1024 * 10
	cfg.DataDir = dir

	return cfg
}
