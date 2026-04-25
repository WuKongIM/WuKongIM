package channel

// LeaderRepairReason explains why the authoritative channel leader needs repair.
type LeaderRepairReason string

const (
	LeaderRepairReasonLeaderMissing      LeaderRepairReason = "leader_missing"
	LeaderRepairReasonLeaderNotReplica   LeaderRepairReason = "leader_not_replica"
	LeaderRepairReasonLeaderLeaseExpired LeaderRepairReason = "leader_lease_expired"
	LeaderRepairReasonLeaderDead         LeaderRepairReason = "leader_dead"
	LeaderRepairReasonLeaderDraining     LeaderRepairReason = "leader_draining"
	LeaderRepairReasonLeaderDrift        LeaderRepairReason = "leader_drift"
)

func (r LeaderRepairReason) String() string {
	return string(r)
}
