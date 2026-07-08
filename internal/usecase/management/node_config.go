package management

import (
	"context"
	"errors"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const NodeConfigSnapshotSourceEffectiveStartup = "effective_startup_config"

// ErrNodeConfigUnavailable reports that selected-node config inspection is not wired.
var ErrNodeConfigUnavailable = errors.New("internal/usecase/management: node config reader unavailable")

// NodeConfigReader reads one selected node's redacted effective startup config.
type NodeConfigReader interface {
	// NodeConfigSnapshot returns one selected node's allowlisted, redacted config snapshot.
	NodeConfigSnapshot(ctx context.Context, nodeID uint64) (NodeConfigSnapshot, error)
}

// NodeConfigSnapshot is the manager-facing redacted startup config view for one node.
type NodeConfigSnapshot struct {
	// GeneratedAt records when this snapshot was built.
	GeneratedAt time.Time `json:"generated_at"`
	// NodeID is the node whose effective config was read.
	NodeID uint64 `json:"node_id"`
	// Source names the config source class.
	Source string `json:"source"`
	// RequiresRestart reports whether changing these startup settings requires restart.
	RequiresRestart bool `json:"requires_restart"`
	// Groups contains stable, bounded config sections.
	Groups []NodeConfigGroup `json:"groups"`
}

// NodeConfigGroup contains a stable set of related config items.
type NodeConfigGroup struct {
	// ID is the stable group identifier used by the web UI.
	ID string `json:"id"`
	// Title is the operator-facing group title.
	Title string `json:"title"`
	// Items contains allowlisted config values.
	Items []NodeConfigItem `json:"items"`
}

// NodeConfigItem contains one allowlisted config value.
type NodeConfigItem struct {
	// Key is the canonical WK_* config key.
	Key string `json:"key"`
	// Label is a concise operator-facing label.
	Label string `json:"label"`
	// Value is the already-formatted effective value.
	Value string `json:"value"`
	// Sensitive reports whether the underlying config is sensitive.
	Sensitive bool `json:"sensitive"`
	// Redacted reports whether Value is a fixed redaction token.
	Redacted bool `json:"redacted"`
}

// NodeConfigSnapshot returns one selected node's redacted effective startup configuration.
func (a *App) NodeConfigSnapshot(ctx context.Context, nodeID uint64) (NodeConfigSnapshot, error) {
	if err := ctxErr(ctx); err != nil {
		return NodeConfigSnapshot{}, err
	}
	if nodeID == 0 {
		return NodeConfigSnapshot{}, metadb.ErrInvalidArgument
	}
	if a == nil || a.nodeConfig == nil {
		return NodeConfigSnapshot{}, ErrNodeConfigUnavailable
	}
	if err := a.ensureManagedNodeExists(ctx, nodeID); err != nil {
		return NodeConfigSnapshot{}, err
	}
	snapshot, err := a.nodeConfig.NodeConfigSnapshot(ctx, nodeID)
	if err != nil {
		return NodeConfigSnapshot{}, err
	}
	if snapshot.NodeID == 0 {
		snapshot.NodeID = nodeID
	}
	return snapshot, nil
}

func (a *App) ensureManagedNodeExists(ctx context.Context, nodeID uint64) error {
	if a == nil || a.cluster == nil {
		return metadb.ErrInvalidArgument
	}
	snapshot, err := a.cluster.LocalControlSnapshot(ctx)
	if err != nil {
		return err
	}
	for _, node := range snapshot.Nodes {
		if node.NodeID == nodeID {
			return nil
		}
	}
	return metadb.ErrNotFound
}
