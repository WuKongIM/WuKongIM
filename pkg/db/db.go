package db

import (
	"errors"
	"sync/atomic"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/message"
	metastore "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// NodeStore is the root handle for node-local storage domains.
type NodeStore struct {
	closed  atomic.Bool
	opts    NodeStoreOptions
	message *engine.DB
	meta    *engine.DB
	metaDB  *metastore.MetaDB
}

// OpenNodeStore opens the physical message and metadata stores.
func OpenNodeStore(opts NodeStoreOptions) (*NodeStore, error) {
	opts = normalizeNodeStoreOptions(opts)
	if opts.MessagePath == "" || opts.MetaPath == "" {
		return nil, ErrInvalidArgument
	}
	message, err := engine.Open(opts.MessagePath, engine.Options{})
	if err != nil {
		return nil, err
	}
	meta, err := engine.Open(opts.MetaPath, engine.Options{})
	if err != nil {
		_ = message.Close()
		return nil, err
	}
	return &NodeStore{opts: opts, message: message, meta: meta, metaDB: metastore.NewDB(meta)}, nil
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
	return message.NewDB(s.message)
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
	if s == nil || s.closed.Swap(true) {
		return nil
	}
	return errors.Join(s.message.Close(), s.meta.Close())
}

// Closed reports whether Close has been called.
func (s *NodeStore) Closed() bool {
	return s == nil || s.closed.Load()
}
