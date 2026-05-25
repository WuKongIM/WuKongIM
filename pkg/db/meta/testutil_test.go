package meta

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

type testMetaStore struct {
	engine *engine.DB
	db     *MetaDB
}

func openTestMetaStore(t *testing.T) *testMetaStore {
	t.Helper()
	eng, err := engine.Open(t.TempDir(), engine.Options{})
	if err != nil {
		t.Fatalf("engine.Open(): %v", err)
	}
	return &testMetaStore{engine: eng, db: NewDB(eng)}
}

func (s *testMetaStore) close(t *testing.T) {
	t.Helper()
	if s == nil || s.engine == nil {
		return
	}
	if err := s.engine.Close(); err != nil {
		t.Fatalf("engine.Close(): %v", err)
	}
}
