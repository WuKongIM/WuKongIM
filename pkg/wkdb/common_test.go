package wkdb_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
)

func newTestDB(t testing.TB) wkdb.DB {
	dr := t.TempDir()

	return wkdb.NewWukongDB(wkdb.NewOptions(wkdb.WithDir(dr), wkdb.WithShardNum(1)))
}
