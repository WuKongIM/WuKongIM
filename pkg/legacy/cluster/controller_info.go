package cluster

// ControllerLeaderID returns the current controller leader node ID when it is
// known locally.
func (c *Cluster) ControllerLeaderID() uint64 {
	if c == nil || c.controller == nil {
		return 0
	}
	return c.controller.LeaderID()
}
