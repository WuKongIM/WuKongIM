package channel

// MQTTReplayReadiness binds local shared-content coverage to one committed log
// frontier. It is not a release decision, consumer proof or cluster-wide receipt.
type MQTTReplayReadiness struct {
	CommittedThrough uint64
	// AnchorPosition and RequiredThrough identify the selected committed anchor.
	// Both are zero when this captured frontier has no anchored obligation.
	AnchorPosition, RequiredThrough uint64
	Covered                         bool
}

// ValidFor validates result association; only the replica's store proves content.
func (r MQTTReplayReadiness) ValidFor(through uint64) bool {
	if r.CommittedThrough != through {
		return false
	}
	if r.AnchorPosition == 0 {
		return r.RequiredThrough == 0 && r.Covered
	}
	return r.RequiredThrough > 0 && r.RequiredThrough < r.AnchorPosition && r.AnchorPosition <= through
}
