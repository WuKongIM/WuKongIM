package inspect

import (
	"errors"

	db "github.com/WuKongIM/WuKongIM/pkg/db"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/message"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	defaultLimit = 100
	maxLimit     = 10000
)

// Store owns read-only database handles for inspection.
type Store struct {
	opts Options

	metaEngine    *engine.DB
	messageEngine *engine.DB
	metaDB        *meta.MetaDB
	messageDB     *message.MessageDB
}

// OpenStore opens metadata and message stores in read-only mode.
func OpenStore(opts Options) (*Store, error) {
	if opts.MetaPath == "" && opts.MessagePath == "" {
		return nil, db.ErrInvalidArgument
	}
	if opts.DefaultLimit <= 0 {
		opts.DefaultLimit = defaultLimit
	}
	if opts.MaxLimit <= 0 {
		opts.MaxLimit = maxLimit
	}

	store := &Store{opts: opts}
	if opts.MetaPath != "" {
		eng, err := engine.Open(opts.MetaPath, engine.Options{ReadOnly: true})
		if err != nil {
			return nil, err
		}
		store.metaEngine = eng
		store.metaDB = meta.NewDB(eng)
	}
	if opts.MessagePath != "" {
		eng, err := engine.Open(opts.MessagePath, engine.Options{ReadOnly: true})
		if err != nil {
			_ = store.Close()
			return nil, err
		}
		store.messageEngine = eng
		store.messageDB = message.NewDB(eng)
	}
	return store, nil
}

// Close releases all opened inspect store handles.
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	var err error
	if s.metaEngine != nil {
		err = errors.Join(err, s.metaEngine.Close())
		s.metaEngine = nil
		s.metaDB = nil
	}
	if s.messageEngine != nil {
		err = errors.Join(err, s.messageEngine.Close())
		s.messageEngine = nil
		s.messageDB = nil
	}
	return err
}
