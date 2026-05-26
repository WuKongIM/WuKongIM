package meta

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

type testMetaStore struct {
	engine *engine.DB
	db     *MetaDB
}

func openTestMetaStore(tb testing.TB) *testMetaStore {
	tb.Helper()
	eng, err := engine.Open(tb.TempDir(), engine.Options{})
	if err != nil {
		tb.Fatalf("engine.Open(): %v", err)
	}
	return &testMetaStore{engine: eng, db: NewDB(eng)}
}

func (s *testMetaStore) close(tb testing.TB) {
	tb.Helper()
	if s == nil || s.engine == nil {
		return
	}
	if err := s.engine.Close(); err != nil {
		tb.Fatalf("engine.Close(): %v", err)
	}
}
