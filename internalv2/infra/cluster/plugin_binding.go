package cluster

import (
	"context"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	pluginusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/plugin"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// PluginBindingNode exposes UID-owned plugin bindings for Receive hook selection.
type PluginBindingNode interface {
	// ListPluginBindingsByUID returns durable plugin bindings for one UID.
	ListPluginBindingsByUID(context.Context, string) ([]metadb.PluginUserBinding, error)
}

// ManagementPluginBindingNode exposes UID-owned plugin binding mutations.
type ManagementPluginBindingNode interface {
	PluginBindingNode
	// BindPluginUser persists one UID-owned plugin binding.
	BindPluginUser(context.Context, metadb.PluginUserBinding) error
	// UnbindPluginUser removes one UID-owned plugin binding.
	UnbindPluginUser(context.Context, string, string) error
}

type managementPluginBindingPluginNoNode interface {
	// ListPluginBindingsByPluginNo returns one plugin-centric binding page.
	ListPluginBindingsByPluginNo(context.Context, string, string, int) ([]metadb.PluginUserBinding, string, bool, error)
}

// PluginBindingReader adapts cluster metadata rows to the plugin usecase port.
type PluginBindingReader struct {
	node PluginBindingNode
}

// ManagementPluginBindingStore adapts cluster metadata to manager binding ports.
type ManagementPluginBindingStore struct {
	node ManagementPluginBindingNode
}

// NewPluginBindingReader creates a Receive binding reader from cluster metadata.
func NewPluginBindingReader(node PluginBindingNode) *PluginBindingReader {
	if node == nil {
		return nil
	}
	return &PluginBindingReader{node: node}
}

// NewManagementPluginBindingStore creates a manager binding store from cluster metadata.
func NewManagementPluginBindingStore(node ManagementPluginBindingNode) *ManagementPluginBindingStore {
	if node == nil {
		return nil
	}
	return &ManagementPluginBindingStore{node: node}
}

// ListPluginBindingsByUID lists all plugin bindings for one UID.
func (r *PluginBindingReader) ListPluginBindingsByUID(ctx context.Context, uid string) ([]pluginusecase.PluginBinding, error) {
	if r == nil || r.node == nil {
		return nil, nil
	}
	bindings, err := r.node.ListPluginBindingsByUID(ctx, uid)
	if err != nil {
		return nil, err
	}
	out := make([]pluginusecase.PluginBinding, 0, len(bindings))
	for _, binding := range bindings {
		out = append(out, pluginusecase.PluginBinding{UID: binding.UID, PluginNo: binding.PluginNo})
	}
	return out, nil
}

// ListPluginBindingsByUID lists all plugin bindings for one UID.
func (s *ManagementPluginBindingStore) ListPluginBindingsByUID(ctx context.Context, uid string) ([]managementusecase.PluginBinding, error) {
	if s == nil || s.node == nil {
		return nil, nil
	}
	bindings, err := s.node.ListPluginBindingsByUID(ctx, uid)
	if err != nil {
		return nil, err
	}
	out := make([]managementusecase.PluginBinding, 0, len(bindings))
	for _, binding := range bindings {
		out = append(out, managementPluginBindingFromMeta(binding))
	}
	return out, nil
}

// ListPluginBindingsByPluginNo lists one plugin-centric binding page.
func (s *ManagementPluginBindingStore) ListPluginBindingsByPluginNo(ctx context.Context, pluginNo, cursor string, limit int) ([]managementusecase.PluginBinding, string, bool, error) {
	if s == nil || s.node == nil {
		return nil, "", false, managementusecase.ErrPluginBindingsUnavailable
	}
	scanner, ok := s.node.(managementPluginBindingPluginNoNode)
	if !ok {
		return nil, "", false, managementusecase.ErrPluginBindingsUnavailable
	}
	bindings, next, hasMore, err := scanner.ListPluginBindingsByPluginNo(ctx, pluginNo, cursor, limit)
	if err != nil {
		return nil, "", false, err
	}
	out := make([]managementusecase.PluginBinding, 0, len(bindings))
	for _, binding := range bindings {
		out = append(out, managementPluginBindingFromMeta(binding))
	}
	return out, next, hasMore, nil
}

// BindPluginUser creates or updates one UID binding.
func (s *ManagementPluginBindingStore) BindPluginUser(ctx context.Context, binding managementusecase.PluginBinding) error {
	if s == nil || s.node == nil {
		return nil
	}
	return s.node.BindPluginUser(ctx, metadb.PluginUserBinding{
		UID:         binding.UID,
		PluginNo:    binding.PluginNo,
		CreatedAtMS: timeToUnixMilli(binding.CreatedAt),
		UpdatedAtMS: timeToUnixMilli(binding.UpdatedAt),
	})
}

// UnbindPluginUser removes one UID binding.
func (s *ManagementPluginBindingStore) UnbindPluginUser(ctx context.Context, uid, pluginNo string) error {
	if s == nil || s.node == nil {
		return nil
	}
	return s.node.UnbindPluginUser(ctx, uid, pluginNo)
}

func managementPluginBindingFromMeta(binding metadb.PluginUserBinding) managementusecase.PluginBinding {
	return managementusecase.PluginBinding{
		UID:       binding.UID,
		PluginNo:  binding.PluginNo,
		CreatedAt: time.UnixMilli(binding.CreatedAtMS).UTC(),
		UpdatedAt: time.UnixMilli(binding.UpdatedAtMS).UTC(),
	}
}

func timeToUnixMilli(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}
