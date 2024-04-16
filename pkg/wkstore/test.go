package wkstore

import "os"

func newTestStoreConfig() *StoreConfig {
	dir, err := os.MkdirTemp("", "commitlog-index")
	if err != nil {
		panic(err)
	}
	cfg := NewStoreConfig()
	cfg.SegmentMaxBytes = 1024 * 1024 * 10
	cfg.DataDir = dir

	return cfg
}
