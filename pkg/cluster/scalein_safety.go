package cluster

import "context"

// ListActiveMigrationsStrict returns active hash-slot migrations from a strict controller-leader table refresh.
func (c *Cluster) ListActiveMigrationsStrict(ctx context.Context) ([]HashSlotMigration, error) {
	if c == nil {
		return nil, ErrNotStarted
	}
	if _, err := c.ListSlotAssignmentsStrict(ctx); err != nil {
		return nil, err
	}
	table := c.GetHashSlotTable()
	if table == nil {
		return nil, ErrNotStarted
	}
	return table.ActiveMigrations(), nil
}
