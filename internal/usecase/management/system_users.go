package management

import (
	"context"
	"sort"
	"strings"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// SystemUser is one persisted manager-facing system UID row.
type SystemUser struct {
	// UID is the system account user identifier.
	UID string
}

// ListSystemUsersResponse is the manager system UID list result.
type ListSystemUsersResponse struct {
	// Items contains sorted system UID rows.
	Items []SystemUser
	// Total is the number of returned rows.
	Total int
}

// MutateSystemUsersRequest configures add/remove system UID mutations.
type MutateSystemUsersRequest struct {
	// UIDs contains raw user identifiers; values are trimmed and deduplicated.
	UIDs []string
}

// MutateSystemUsersResponse reports accepted system UID mutations.
type MutateSystemUsersResponse struct {
	// UIDs contains normalized UID values passed to the mutation operator.
	UIDs []string
	// Changed reports whether the mutation was accepted.
	Changed bool
}

// ListSystemUsers returns all persisted system account UIDs in stable order.
func (a *App) ListSystemUsers(ctx context.Context) (ListSystemUsersResponse, error) {
	if a == nil || a.systemUsers == nil {
		return ListSystemUsersResponse{}, metadb.ErrInvalidArgument
	}
	uids, err := a.systemUsers.ListSystemUIDs(ctx)
	if err != nil {
		return ListSystemUsersResponse{}, err
	}
	normalized := normalizeSystemUIDs(uids)
	sort.Strings(normalized)
	items := make([]SystemUser, 0, len(normalized))
	for _, uid := range normalized {
		items = append(items, SystemUser{UID: uid})
	}
	return ListSystemUsersResponse{Items: items, Total: len(items)}, nil
}

// AddSystemUsers persists system account UIDs and refreshes caches.
func (a *App) AddSystemUsers(ctx context.Context, req MutateSystemUsersRequest) (MutateSystemUsersResponse, error) {
	if a == nil || a.systemUsers == nil {
		return MutateSystemUsersResponse{}, metadb.ErrInvalidArgument
	}
	uids, err := normalizeSystemUIDMutation(req.UIDs)
	if err != nil {
		return MutateSystemUsersResponse{}, err
	}
	if err := a.systemUsers.AddSystemUIDs(ctx, uids); err != nil {
		return MutateSystemUsersResponse{}, err
	}
	return MutateSystemUsersResponse{UIDs: uids, Changed: true}, nil
}

// RemoveSystemUsers removes persisted system account UIDs and refreshes caches.
func (a *App) RemoveSystemUsers(ctx context.Context, req MutateSystemUsersRequest) (MutateSystemUsersResponse, error) {
	if a == nil || a.systemUsers == nil {
		return MutateSystemUsersResponse{}, metadb.ErrInvalidArgument
	}
	uids, err := normalizeSystemUIDMutation(req.UIDs)
	if err != nil {
		return MutateSystemUsersResponse{}, err
	}
	if err := a.systemUsers.RemoveSystemUIDs(ctx, uids); err != nil {
		return MutateSystemUsersResponse{}, err
	}
	return MutateSystemUsersResponse{UIDs: uids, Changed: true}, nil
}

func normalizeSystemUIDMutation(raw []string) ([]string, error) {
	uids := normalizeSystemUIDs(raw)
	if len(uids) == 0 {
		return nil, metadb.ErrInvalidArgument
	}
	return uids, nil
}

func normalizeSystemUIDs(raw []string) []string {
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, value := range raw {
		uid := strings.TrimSpace(value)
		if uid == "" {
			continue
		}
		if _, ok := seen[uid]; ok {
			continue
		}
		seen[uid] = struct{}{}
		out = append(out, uid)
	}
	return out
}
