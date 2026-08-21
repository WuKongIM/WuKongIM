package cluster

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internal/runtime/persondirectory"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// PersonDirectoryMembershipNode exposes aligned create-if-absent projection
// results from current UID Slot leaders.
type PersonDirectoryMembershipNode interface {
	EnsureUserChannelMembershipBatch(context.Context, []metadb.UserChannelMembership) []error
}

// PersonDirectoryMembershipWriter adapts cluster errors to the runtime's
// aligned result contract without interpreting projection policy.
type PersonDirectoryMembershipWriter struct {
	node PersonDirectoryMembershipNode
}

// NewPersonDirectoryMembershipWriter constructs a cluster-backed writer.
func NewPersonDirectoryMembershipWriter(node PersonDirectoryMembershipNode) *PersonDirectoryMembershipWriter {
	return &PersonDirectoryMembershipWriter{node: node}
}

// EnsureUserChannelMembershipBatch preserves exact input/result alignment.
func (w *PersonDirectoryMembershipWriter) EnsureUserChannelMembershipBatch(ctx context.Context, memberships []metadb.UserChannelMembership) []persondirectory.MembershipResult {
	results := make([]persondirectory.MembershipResult, len(memberships))
	if w == nil || w.node == nil {
		for i := range results {
			results[i].Err = metadb.ErrInvalidArgument
		}
		return results
	}
	errs := w.node.EnsureUserChannelMembershipBatch(ctx, memberships)
	if len(errs) != len(memberships) {
		for i := range results {
			results[i].Err = metadb.ErrInvalidArgument
		}
		return results
	}
	for i, err := range errs {
		results[i].Err = err
	}
	return results
}
