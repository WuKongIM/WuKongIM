package wraft

type Monitor interface {
	// SetProposalsCommitted sets the number of proposals committed to the log.
	SetProposalsCommitted(proposalsCommitted uint64)
	// LeaderChangesInc increments the number of leader changes.
	LeaderChangesInc()
	// HeartbeatSendFailuresInc increments the number of heartbeat send failures.
	HeartbeatSendFailuresInc()

	ProposalsFailedInc()

	ProposalsPendingInc()

	ProposalsPendingDec()
}

type emptyMonitor struct{}

func (e *emptyMonitor) SetProposalsCommitted(proposalsCommitted uint64) {}
func (e *emptyMonitor) LeaderChangesInc()                               {}
func (e *emptyMonitor) HeartbeatSendFailuresInc()                       {}

func (e *emptyMonitor) ProposalsFailedInc()  {}
func (e *emptyMonitor) ProposalsPendingInc() {}
func (e *emptyMonitor) ProposalsPendingDec() {}
