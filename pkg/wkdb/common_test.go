package wkdb_test

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
)

func newTestDB(t testing.TB) wkdb.DB {
	dr := t.TempDir()

	traceObj := trace.New(
		context.Background(),
		trace.NewOptions(
			trace.WithServiceName("test"),
			trace.WithServiceHostName("host"),
		))
	trace.SetGlobalTrace(traceObj)

	return wkdb.NewWukongDB(wkdb.NewOptions(wkdb.WithDir(dr), wkdb.WithShardNum(1)))
}
