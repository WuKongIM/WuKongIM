package reactor

import "testing"

func TestEventDomainHandlerNames(t *testing.T) {
	var r *Reactor

	_ = r.handleLeaderPull
	_ = r.handleLeaderAck
	_ = r.handleFollowerPullHint
	_ = r.handleLegacyFollowerNotify
	_ = r.tickFollowerReplication
	_ = r.tickLifecycleController
	_ = r.driveLifecycle
	_ = r.handleWorkerResult
}
