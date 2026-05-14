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
