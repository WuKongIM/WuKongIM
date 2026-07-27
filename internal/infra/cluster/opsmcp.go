package cluster

import (
	"context"

	runtimeops "github.com/WuKongIM/WuKongIM/internal/runtime/opsmcp"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	controller "github.com/WuKongIM/WuKongIM/pkg/controller"
)

// ManagementOpsMCPNode exposes the bounded Controller MCP desired-state writer.
type ManagementOpsMCPNode interface {
	ReplaceOpsMCPState(context.Context, uint64, controller.OpsMCPState) error
}

// OpsMCPStateNode exposes the latest local Controller read model.
type OpsMCPStateNode interface {
	LocalControlSnapshot(context.Context) (control.Snapshot, error)
}

// OpsMCPStateReader projects Controller state into the high-frequency runtime.
type OpsMCPStateReader struct {
	node OpsMCPStateNode
}

// NewOpsMCPStateReader creates the narrow desired-state adapter.
func NewOpsMCPStateReader(node OpsMCPStateNode) *OpsMCPStateReader {
	return &OpsMCPStateReader{node: node}
}

// OpsMCPDesiredState returns one detached runtime desired-state snapshot.
func (r *OpsMCPStateReader) OpsMCPDesiredState(ctx context.Context) (runtimeops.DesiredState, error) {
	if r == nil || r.node == nil {
		return runtimeops.DesiredState{}, controller.ErrNotStarted
	}
	snapshot, err := r.node.LocalControlSnapshot(ctx)
	if err != nil {
		return runtimeops.DesiredState{}, err
	}
	state := runtimeops.DesiredState{Revision: snapshot.Revision, Credentials: []runtimeops.Credential{}}
	if snapshot.OpsMCP == nil {
		return state, nil
	}
	state.Enabled = snapshot.OpsMCP.Enabled
	state.OwnerNodeID = snapshot.OpsMCP.OwnerNodeID
	state.ProfileFenceUntilUnixMillis = snapshot.OpsMCP.ProfileFenceUntilUnixMillis
	state.Credentials = make([]runtimeops.Credential, 0, len(snapshot.OpsMCP.Credentials))
	for _, credential := range snapshot.OpsMCP.Credentials {
		state.Credentials = append(state.Credentials, runtimeops.Credential{
			ID: credential.ID, DigestSHA256: credential.DigestSHA256,
		})
	}
	return state, nil
}

// ManagementOpsMCPWriter adapts cluster control writes to the management usecase.
type ManagementOpsMCPWriter struct {
	node ManagementOpsMCPNode
}

// NewManagementOpsMCPWriter creates the bounded MCP desired-state adapter.
func NewManagementOpsMCPWriter(node ManagementOpsMCPNode) *ManagementOpsMCPWriter {
	return &ManagementOpsMCPWriter{node: node}
}

// ReplaceOpsMCPState commits one revision-fenced complete replacement.
func (w *ManagementOpsMCPWriter) ReplaceOpsMCPState(ctx context.Context, expectedRevision uint64, replacement controller.OpsMCPState) error {
	if w == nil || w.node == nil {
		return controller.ErrNotStarted
	}
	return w.node.ReplaceOpsMCPState(ctx, expectedRevision, replacement)
}
