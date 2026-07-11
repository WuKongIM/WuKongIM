package db

import (
	"errors"
	"sync"
	"sync/atomic"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/message"
	metastore "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// NodeStore is the root handle for node-local storage domains.
type NodeStore struct {
	// lifecycleMu prevents physical metrics reads from overlapping shutdown.
	lifecycleMu sync.RWMutex
	// closed makes root shutdown idempotent.
	closed atomic.Bool
	// opts contains the normalized physical store configuration.
	opts NodeStoreOptions
	// message is the physical message engine used by metrics and fallback close.
	message *engine.DB
	// messageDB is the single canonical message domain returned by Messages.
	messageDB *message.MessageDB
	// meta is the physical metadata engine.
	meta *engine.DB
	// metaDB is the typed metadata domain returned by Meta.
	metaDB *metastore.MetaDB
}

// OpenNodeStore opens the physical message and metadata stores.
func OpenNodeStore(opts NodeStoreOptions) (*NodeStore, error) {
	opts = normalizeNodeStoreOptions(opts)
	if opts.MessagePath == "" || opts.MetaPath == "" {
		return nil, ErrInvalidArgument
	}
	messageEngine, err := engine.Open(opts.MessagePath, engine.Options{})
	if err != nil {
		return nil, err
	}
	meta, err := engine.Open(opts.MetaPath, engine.Options{})
	if err != nil {
		_ = messageEngine.Close()
		return nil, err
	}
	return &NodeStore{
		opts:      opts,
		message:   messageEngine,
		messageDB: message.NewDB(messageEngine),
		meta:      meta,
		metaDB:    metastore.NewDB(meta),
	}, nil
}

// Options returns the normalized store options.
func (s *NodeStore) Options() NodeStoreOptions {
	if s == nil {
		return NodeStoreOptions{}
	}
	return s.opts
}

// Messages returns the channel message storage domain.
func (s *NodeStore) Messages() *message.MessageDB {
	if s == nil {
		return nil
	}
	return s.messageDB
}

// Meta returns the hash-slot metadata storage domain.
func (s *NodeStore) Meta() *metastore.MetaDB {
	if s == nil {
		return nil
	}
	return s.metaDB
}

// Close closes the physical stores.
func (s *NodeStore) Close() error {
	if s == nil {
		return nil
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.closed.Swap(true) {
		return nil
	}
	var messageErr error
	if s.messageDB != nil {
		messageErr = s.messageDB.Close()
	} else if s.message != nil {
		messageErr = s.message.Close()
	}
	return errors.Join(messageErr, s.meta.Close())
}

// Closed reports whether Close has been called.
func (s *NodeStore) Closed() bool {
	return s == nil || s.closed.Load()
}
