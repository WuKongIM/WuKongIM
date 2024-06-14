package wkstore

import "os"

func newTestStoreConfig() *StoreConfig {
	dir, err := "./test_data", os.MkdirAll("./test_data", 0755)
	if err != nil {
		panic(err)
	}
	cfg := NewStoreConfig()
	cfg.SegmentMaxBytes = 1024 * 1024 * 10
	cfg.DataDir = dir

	return cfg
}
