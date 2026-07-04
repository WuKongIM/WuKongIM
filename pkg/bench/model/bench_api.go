package model

// BenchCapabilities describes the target bench/v1 API feature set and limits.
type BenchCapabilities struct {
	// Enabled confirms the target exposes the benchmark-only API surface.
	Enabled bool `json:"enabled"`
	// Version is the target bench API version, currently bench/v1.
	Version string `json:"version"`
	// Supports lists the preparation dimensions supported by the target.
	Supports BenchCapabilitiesSupports `json:"supports"`
	// Limits lists target request limits visible to wkbench.
	Limits BenchCapabilitiesLimits `json:"limits"`
}

// BenchCapabilitiesSupports lists the target bench/v1 preparation features.
type BenchCapabilitiesSupports struct {
	// UsersTokensBatch indicates support for batch user token upserts.
	UsersTokensBatch bool `json:"users_tokens_batch"`
	// ChannelsBatch indicates support for batch channel upserts.
	ChannelsBatch bool `json:"channels_batch"`
	// ChannelSubscribersBatch indicates support for batch subscriber appends.
	ChannelSubscribersBatch bool `json:"channel_subscribers_batch"`
	// Snapshot indicates support for lightweight bench setup snapshots.
	Snapshot bool `json:"snapshot"`
	// PresenceSnapshot indicates support for connection-route presence snapshots.
	PresenceSnapshot bool `json:"presence_snapshot"`
	// ChannelRuntimeSnapshot indicates support for local Channel runtime snapshots.
	ChannelRuntimeSnapshot bool `json:"channel_runtime_snapshot"`
	// ChannelRuntimeProbe indicates support for bounded Channel runtime probes.
	ChannelRuntimeProbe bool `json:"channel_runtime_probe"`
	// ChannelRuntimeEvict indicates support for bounded Channel runtime eviction.
	ChannelRuntimeEvict bool `json:"channel_runtime_evict"`
	// ChannelRuntimeFaults indicates support for runtime fault injection controls.
	ChannelRuntimeFaults bool `json:"channel_runtime_faults"`
	// ChannelRuntimeActivate indicates support for server-side diagnostic activation.
	ChannelRuntimeActivate bool `json:"channel_runtime_activate"`
	// ChannelTypes lists logical channel types accepted by the bench API.
	ChannelTypes []string `json:"channel_types"`
}

// BenchCapabilitiesLimits lists target bench/v1 request limits.
type BenchCapabilitiesLimits struct {
	// MaxBatchSize limits top-level items accepted by mutation requests.
	MaxBatchSize int `json:"max_batch_size"`
	// MaxPayloadBytes limits JSON request body size when configured by the target.
	MaxPayloadBytes int64 `json:"max_payload_bytes"`
}

// BenchSnapshot is a lightweight target setup snapshot used by preflight and reports.
type BenchSnapshot struct {
	// Version is the target bench API version that produced the snapshot.
	Version string `json:"version"`
	// Counts contains target-defined benchmark object counts.
	Counts map[string]int `json:"counts,omitempty"`
}

// PresenceSnapshot describes owner-local and authority-side route state for bench diagnostics.
type PresenceSnapshot struct {
	// Version is the target bench API version that produced the snapshot.
	Version string `json:"version"`
	// NodeID identifies the target node that produced the snapshot.
	NodeID uint64 `json:"node_id"`
	// OwnerRoutesActive counts active owner-local route projections.
	OwnerRoutesActive int `json:"owner_routes_active"`
	// OwnerRoutesPending counts owner-local routes accepted but not yet authority-active.
	OwnerRoutesPending int `json:"owner_routes_pending"`
	// OwnerTouchedDirty counts owner-local routes pending touch flush.
	OwnerTouchedDirty int `json:"owner_touched_dirty"`
	// AuthorityRoutesActive counts active authority-side routes on this node.
	AuthorityRoutesActive int `json:"authority_routes_active"`
	// AuthorityRoutesByHashSlot groups active authority routes by UID hash slot.
	AuthorityRoutesByHashSlot map[uint16]int `json:"authority_routes_by_hash_slot,omitempty"`
	// TouchRoutesTotal counts authority touch route entries accepted by this node.
	TouchRoutesTotal uint64 `json:"touch_routes_total"`
	// ExpiredRoutesTotal counts authority routes expired by TTL cleanup on this node.
	ExpiredRoutesTotal uint64 `json:"expired_routes_total"`
}

// ChannelRuntimeRange selects generated channel indexes in [start,end).
type ChannelRuntimeRange struct {
	// Start is the first generated channel index included by the selector.
	Start int `json:"start"`
	// End is one past the last generated channel index included by the selector.
	End int `json:"end"`
}

// ChannelRuntimeQuery selects one generated benchmark channel set.
type ChannelRuntimeQuery struct {
	// RunID identifies the benchmark run that generated the channels.
	RunID string `json:"run_id"`
	// Profile identifies the generated channel profile.
	Profile string `json:"profile"`
	// ChannelType is the WuKong channel type, group is currently 2.
	ChannelType uint8 `json:"channel_type"`
	// Range selects generated channel indexes in [start,end).
	Range ChannelRuntimeRange `json:"range"`
}

// ChannelRuntimeSnapshot describes local Channel runtime state for diagnostics.
type ChannelRuntimeSnapshot struct {
	// Version is the target bench API version that produced the snapshot.
	Version string `json:"version"`
	// NodeID identifies the target node that produced the snapshot.
	NodeID uint64 `json:"node_id"`
	// RunID identifies the benchmark run associated with the snapshot when known.
	RunID string `json:"run_id,omitempty"`
	// Profile identifies the generated channel profile associated with the snapshot.
	Profile string `json:"profile,omitempty"`
	// ActiveTotal is the total number of active channel runtimes.
	ActiveTotal int `json:"active_total"`
	// ActiveLeader is the number of active leader channel runtimes.
	ActiveLeader int `json:"active_leader"`
	// ActiveFollower is the number of active follower channel runtimes.
	ActiveFollower int `json:"active_follower"`
	// FollowerParked is the number of parked follower channel runtimes.
	FollowerParked int `json:"follower_parked"`
	// ActivationRejectedTotal counts rejected runtime activation requests.
	ActivationRejectedTotal uint64 `json:"activation_rejected_total"`
	// Reactors contains per-reactor runtime counts.
	Reactors []ChannelRuntimeReactorSnapshot `json:"reactors,omitempty"`
	// WorkerQueues contains bounded worker queue depths.
	WorkerQueues []ChannelRuntimeWorkerQueue `json:"worker_queues,omitempty"`
}

// ChannelRuntimeReactorSnapshot describes one Channel reactor's runtime state.
type ChannelRuntimeReactorSnapshot struct {
	// ReactorID identifies the reactor within the target node.
	ReactorID int `json:"reactor_id"`
	// Leader is the number of leader channel runtimes on this reactor.
	Leader int `json:"leader"`
	// Follower is the number of follower channel runtimes on this reactor.
	Follower int `json:"follower"`
	// Parked is the number of parked follower channel runtimes on this reactor.
	Parked int `json:"parked"`
	// MailboxDepth is the current reactor mailbox depth.
	MailboxDepth int `json:"mailbox_depth"`
}

// ChannelRuntimeWorkerQueue describes one Channel runtime worker queue depth.
type ChannelRuntimeWorkerQueue struct {
	// Pool identifies the worker queue pool.
	Pool string `json:"pool"`
	// Depth is the current queued work depth.
	Depth int `json:"depth"`
}

// ChannelRuntimeProbeRequest selects generated channels for a bounded runtime probe.
type ChannelRuntimeProbeRequest struct {
	// RunID identifies the benchmark run that generated the channels.
	RunID string `json:"run_id"`
	// Profile identifies the generated channel profile.
	Profile string `json:"profile"`
	// ChannelType is the WuKong channel type, group is currently 2.
	ChannelType uint8 `json:"channel_type"`
	// Range selects generated channel indexes in [start,end).
	Range ChannelRuntimeRange `json:"range"`
}

// ChannelRuntimeProbeResult reports the result of a bounded runtime probe.
type ChannelRuntimeProbeResult struct {
	// Version is the target bench API version that produced the result.
	Version string `json:"version"`
	// NodeID identifies the target node that produced the result.
	NodeID uint64 `json:"node_id"`
	// RunID identifies the benchmark run that generated the channels.
	RunID string `json:"run_id"`
	// Profile identifies the generated channel profile.
	Profile string `json:"profile"`
	// Checked is the number of generated channels checked by the probe.
	Checked int `json:"checked"`
	// LoadedLeader is the number of probed channels loaded as leaders.
	LoadedLeader int `json:"loaded_leader"`
	// LoadedFollower is the number of probed channels loaded as followers.
	LoadedFollower int `json:"loaded_follower"`
	// Missing lists generated channel IDs that were not loaded.
	Missing []string `json:"missing,omitempty"`
}

// ChannelRuntimeEvictRequest selects generated channels for bounded runtime eviction.
type ChannelRuntimeEvictRequest struct {
	// RunID identifies the benchmark run that generated the channels.
	RunID string `json:"run_id"`
	// Profile identifies the generated channel profile.
	Profile string `json:"profile"`
	// ChannelType is the WuKong channel type, group is currently 2.
	ChannelType uint8 `json:"channel_type"`
	// Range selects generated channel indexes in [start,end).
	Range ChannelRuntimeRange `json:"range"`
}

// ChannelRuntimeEvictResult reports the result of bounded runtime eviction.
type ChannelRuntimeEvictResult struct {
	// Version is the target bench API version that produced the result.
	Version string `json:"version"`
	// NodeID identifies the target node that produced the result.
	NodeID uint64 `json:"node_id"`
	// RunID identifies the benchmark run that generated the channels.
	RunID string `json:"run_id"`
	// Profile identifies the generated channel profile.
	Profile string `json:"profile"`
	// Requested is the number of generated channels selected for eviction.
	Requested int `json:"requested"`
	// Evicted is the number of channel runtimes evicted.
	Evicted int `json:"evicted"`
	// SkippedBusy is the number of busy channel runtimes skipped during eviction.
	SkippedBusy int `json:"skipped_busy"`
	// Missing is the number of selected channel runtimes that were not loaded.
	Missing int `json:"missing"`
}

// CapacityTarget describes the target node addresses needed by capacity tests.
type CapacityTarget struct {
	// Version is the benchmark API version that produced this target document.
	Version string `json:"version"`
	// Gateway contains gateway addresses published by this target node.
	Gateway CapacityTargetGateway `json:"gateway"`
}

// CapacityTargetGateway contains externally reachable gateway addresses.
type CapacityTargetGateway struct {
	// TCPAddr is the WKProto TCP gateway address used by wkbench workers.
	TCPAddr string `json:"tcp_addr"`
	// WSAddr is the WebSocket gateway address, reserved for future workers.
	WSAddr string `json:"ws_addr"`
	// WSSAddr is the secure WebSocket gateway address, reserved for future workers.
	WSSAddr string `json:"wss_addr"`
}

// BatchTokensRequest updates benchmark user tokens in one batch.
type BatchTokensRequest struct {
	// RunID identifies the benchmark run that owns this batch.
	RunID string `json:"run_id"`
	// BatchID identifies this idempotent preparation batch.
	BatchID string `json:"batch_id"`
	// Upsert asks the target to create or update records where supported.
	Upsert bool `json:"upsert,omitempty"`
	// Users is the spec-shaped user token list accepted by bench/v1.
	Users []UserTokenItem `json:"users,omitempty"`
	// Items keeps compatibility with older bench/v1 item-shaped requests.
	Items []UserTokenItem `json:"items,omitempty"`
}

// UserTokenItem carries one benchmark user token update.
type UserTokenItem struct {
	// UID is the benchmark user identifier.
	UID string `json:"uid"`
	// Token is the authentication token assigned to the benchmark user.
	Token string `json:"token"`
	// DeviceFlag is the legacy WuKong device flag.
	DeviceFlag uint8 `json:"device_flag,omitempty"`
	// DeviceLevel is the legacy WuKong device level.
	DeviceLevel uint8 `json:"device_level,omitempty"`
}

// BatchChannelsRequest upserts benchmark channels in one batch.
type BatchChannelsRequest struct {
	// RunID identifies the benchmark run that owns this batch.
	RunID string `json:"run_id"`
	// BatchID identifies this idempotent preparation batch.
	BatchID string `json:"batch_id"`
	// Upsert asks the target to create or update records where supported.
	Upsert bool `json:"upsert,omitempty"`
	// Channels is the spec-shaped channel list accepted by bench/v1.
	Channels []ChannelItem `json:"channels,omitempty"`
	// Items keeps compatibility with older bench/v1 item-shaped requests.
	Items []ChannelItem `json:"items,omitempty"`
}

// ChannelItem carries one benchmark channel upsert.
type ChannelItem struct {
	// ChannelID is the benchmark channel identifier.
	ChannelID string `json:"channel_id"`
	// ChannelType is the legacy WuKong channel type; group is currently 2.
	ChannelType uint8 `json:"channel_type"`
	// Large marks a large-group channel.
	Large bool `json:"large,omitempty"`
	// Ban blocks channel messaging when true.
	Ban bool `json:"ban,omitempty"`
	// Disband marks a channel as disbanded.
	Disband bool `json:"disband,omitempty"`
	// SendBan blocks sending while allowing receives.
	SendBan bool `json:"send_ban,omitempty"`
	// AllowStranger permits stranger sends for compatible legacy semantics.
	AllowStranger bool `json:"allow_stranger,omitempty"`
}

// BatchSubscribersRequest appends benchmark subscribers in one batch.
type BatchSubscribersRequest struct {
	// RunID identifies the benchmark run that owns this batch.
	RunID string `json:"run_id"`
	// BatchID identifies this idempotent preparation batch.
	BatchID string `json:"batch_id"`
	// Items carries subscriber mutations grouped by channel.
	Items []SubscriberItem `json:"items"`
}

// SubscriberItem carries subscribers to append for one group channel.
type SubscriberItem struct {
	// ChannelID is the benchmark channel identifier.
	ChannelID string `json:"channel_id"`
	// ChannelType is the legacy WuKong channel type; group is currently 2.
	ChannelType uint8 `json:"channel_type"`
	// Reset requests subscriber replacement; bench/v1 currently rejects true.
	Reset bool `json:"reset,omitempty"`
	// Subscribers are user IDs appended to the channel subscriber list.
	Subscribers []string `json:"subscribers"`
}
