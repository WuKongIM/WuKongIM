package model

// Range is a half-open integer interval [Start, End) used for deterministic shard assignments.
type Range struct {
	// Start is the inclusive first index assigned to a shard.
	Start int `json:"start" yaml:"start"`
	// End is the exclusive index after the last index assigned to a shard.
	End int `json:"end" yaml:"end"`
}

// Len returns the number of indexes in the range.
func (r Range) Len() int {
	return r.End - r.Start
}

// Plan is the deterministic worker assignment produced from a wkbench scenario.
type Plan struct {
	// RunID is copied from the scenario run identifier for downstream reporting.
	RunID string `json:"run_id" yaml:"run_id"`
	// Workers contains per-worker shards keyed by stable worker ID.
	Workers map[string]WorkerPlan `json:"workers" yaml:"workers"`
	// WorkerOrder preserves the input worker order used for deterministic range splitting.
	WorkerOrder []string `json:"worker_order" yaml:"worker_order"`
	// ProfileOrder preserves scenario channel profile order for deterministic execution.
	ProfileOrder []string `json:"profile_order" yaml:"profile_order"`
	// IdentityPool is the shared generated user index pool available to all profile shards.
	IdentityPool Range `json:"identity_pool" yaml:"identity_pool"`
	// OnlineIdentityPool is the subset of IdentityPool kept connected for this run.
	OnlineIdentityPool Range `json:"online_identity_pool" yaml:"online_identity_pool"`
	// ChannelOwners records deterministic group channel owners by profile and channel index.
	ChannelOwners map[string]map[int]string `json:"channel_owners" yaml:"channel_owners"`
}

// WorkerPlan describes all profile shards assigned to one worker.
type WorkerPlan struct {
	// WorkerID is the stable worker identifier from the configured worker set.
	WorkerID string `json:"worker_id" yaml:"worker_id"`
	// IdentityRange is the generated user index range this worker should keep online.
	IdentityRange Range `json:"identity_range" yaml:"identity_range"`
	// OnlineIdentityIndexes maps logical online indexes to their currently connected identity indexes.
	// An empty slice preserves the initial identity mapping.
	OnlineIdentityIndexes []int `json:"online_identity_indexes,omitempty" yaml:"online_identity_indexes,omitempty"`
	// Profiles contains one shard per scenario channel profile keyed by profile name.
	Profiles map[string]ProfileShard `json:"profiles" yaml:"profiles"`
}

// ProfileShard describes one worker's deterministic assignment for a channel profile.
type ProfileShard struct {
	// Name is the channel profile name.
	Name string `json:"name" yaml:"name"`
	// ChannelType is the validated v1 channel type, either person or group.
	ChannelType string `json:"channel_type" yaml:"channel_type"`
	// ChannelRange is the half-open channel index range assigned to this worker.
	ChannelRange Range `json:"channel_range" yaml:"channel_range"`
	// ParticipantRange is the half-open generated user index range consumed by person channels.
	ParticipantRange Range `json:"participant_range" yaml:"participant_range"`
	// MemberRange is the half-open member index range assigned to this worker for group member generation.
	MemberRange Range `json:"member_range" yaml:"member_range"`
	// MemberReusePolicy records whether group member indexes are shared or disjoint.
	MemberReusePolicy string `json:"member_reuse_policy,omitempty" yaml:"member_reuse_policy,omitempty"`
	// GlobalRate is the logical per-channel rate configured for this profile.
	GlobalRate Rate `json:"global_rate" yaml:"global_rate"`
	// LocalRate is the worker-local share of GlobalRate for split traffic profiles.
	LocalRate Rate `json:"local_rate" yaml:"local_rate"`
	// TrafficPartitionCount is the global traffic partition modulus for this profile.
	TrafficPartitionCount int `json:"traffic_partition_count" yaml:"traffic_partition_count"`
	// OwnedTrafficPartitions are deterministic partition indexes assigned to this worker.
	OwnedTrafficPartitions []int `json:"owned_traffic_partitions" yaml:"owned_traffic_partitions"`
}
