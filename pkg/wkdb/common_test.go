package wkdb_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
)

func newTestDB(t testing.TB) wkdb.DB {
	return wkdb.NewWukongDB(wkdb.NewOptions(wkdb.WithDir(t.TempDir()), wkdb.WithShardNum(1)))
}
