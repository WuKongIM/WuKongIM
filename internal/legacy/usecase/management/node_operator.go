package management

import "context"

// MarkNodeDraining marks a node as draining and returns the latest node detail.
func (a *App) MarkNodeDraining(ctx context.Context, nodeID uint64) (NodeDetail, error) {
	return a.applyNodeOperator(ctx, nodeID, "draining", func(ctx context.Context) error {
		return a.cluster.MarkNodeDraining(ctx, nodeID)
	})
}

// ResumeNode marks a node as alive and returns the latest node detail.
func (a *App) ResumeNode(ctx context.Context, nodeID uint64) (NodeDetail, error) {
	return a.applyNodeOperator(ctx, nodeID, "alive", func(ctx context.Context) error {
		return a.cluster.ResumeNode(ctx, nodeID)
	})
}

func (a *App) applyNodeOperator(ctx context.Context, nodeID uint64, targetStatus string, apply func(context.Context) error) (NodeDetail, error) {
	current, err := a.GetNode(ctx, nodeID)
	if err != nil {
		return NodeDetail{}, err
	}
	if current.Status == targetStatus {
		return current, nil
	}
	if err := apply(ctx); err != nil {
		return NodeDetail{}, err
	}
	return a.GetNode(ctx, nodeID)
}
