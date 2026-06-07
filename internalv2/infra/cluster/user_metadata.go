package cluster

import (
	"context"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// UserMetadataNode exposes clusterv2 Slot metadata operations used by the user usecase.
type UserMetadataNode interface {
	CreateUserMetadata(context.Context, metadb.User) error
	GetUserMetadata(context.Context, string) (metadb.User, error)
	UpsertDeviceMetadata(context.Context, metadb.Device) error
	GetDeviceMetadata(context.Context, string, int64) (metadb.Device, error)
}

// UserMetadataStore adapts clusterv2 Slot metadata to the entry-agnostic user usecase.
type UserMetadataStore struct {
	node UserMetadataNode
}

// NewUserMetadataStore creates a clusterv2-backed user metadata store.
func NewUserMetadataStore(node UserMetadataNode) *UserMetadataStore {
	return &UserMetadataStore{node: node}
}

// CreateUser persists UID metadata through Slot ownership.
func (s *UserMetadataStore) CreateUser(ctx context.Context, user metadb.User) error {
	if s == nil || s.node == nil {
		return metadb.ErrNotFound
	}
	return s.node.CreateUserMetadata(ctx, user)
}

// GetUser reads UID metadata from the current Slot route.
func (s *UserMetadataStore) GetUser(ctx context.Context, uid string) (metadb.User, error) {
	if s == nil || s.node == nil {
		return metadb.User{}, metadb.ErrNotFound
	}
	return s.node.GetUserMetadata(ctx, uid)
}

// UpsertDevice persists per-device token metadata through Slot ownership.
func (s *UserMetadataStore) UpsertDevice(ctx context.Context, device metadb.Device) error {
	if s == nil || s.node == nil {
		return metadb.ErrNotFound
	}
	return s.node.UpsertDeviceMetadata(ctx, device)
}

// GetDevice reads per-device token metadata from the current Slot route.
func (s *UserMetadataStore) GetDevice(ctx context.Context, uid string, deviceFlag int64) (metadb.Device, error) {
	if s == nil || s.node == nil {
		return metadb.Device{}, metadb.ErrNotFound
	}
	return s.node.GetDeviceMetadata(ctx, uid, deviceFlag)
}
