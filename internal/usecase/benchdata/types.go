package benchdata

import "context"

// Config defines bench data preparation dependencies and request limits.
type Config struct {
	// Users updates benchmark user tokens through the normal user usecase boundary.
	Users UserWriter
	// Channels mutates benchmark channel metadata and subscribers through the channel usecase boundary.
	Channels ChannelWriter
	// Snapshot reads a lightweight benchmark setup snapshot.
	Snapshot SnapshotReader
	// MaxBatchSize limits the number of top-level items accepted by mutation requests.
	MaxBatchSize int
	// MaxPayloadBytes reports the configured HTTP payload limit to bench clients.
	MaxPayloadBytes int64
}

// UserWriter updates benchmark user tokens through the normal user usecase boundary.
type UserWriter interface {
	UpdateToken(ctx context.Context, cmd UserTokenCommand) error
}

// ChannelWriter mutates benchmark channel metadata and subscribers.
type ChannelWriter interface {
	UpsertChannel(ctx context.Context, ch ChannelRecord) error
	AddSubscribers(ctx context.Context, channelID string, channelType uint8, uids []string) error
}

// SnapshotReader returns lightweight benchmark setup state for clients.
type SnapshotReader interface {
	Snapshot(ctx context.Context) (SnapshotResponse, error)
}

// UserTokenCommand carries one benchmark user token update.
type UserTokenCommand struct {
	// UID is the benchmark user identifier.
	UID string `json:"uid"`
	// Token is the authentication token assigned to the benchmark user.
	Token string `json:"token"`
	// DeviceFlag is the legacy WuKong device flag.
	DeviceFlag uint8 `json:"device_flag,omitempty"`
	// DeviceLevel is the legacy WuKong device level.
	DeviceLevel uint8 `json:"device_level,omitempty"`
}

// ChannelRecord carries one benchmark channel upsert.
type ChannelRecord struct {
	// ChannelID is the benchmark channel identifier.
	ChannelID string `json:"channel_id"`
	// ChannelType is the legacy WuKong channel type; bench/v1 accepts group channels only.
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

// CapabilitiesResponse describes the bench target API version and limits.
type CapabilitiesResponse struct {
	Enabled  bool                 `json:"enabled"`
	Version  string               `json:"version"`
	Supports CapabilitiesSupports `json:"supports"`
	Limits   CapabilitiesLimits   `json:"limits"`
}

// CapabilitiesSupports lists bench/v1 supported setup dimensions.
type CapabilitiesSupports struct {
	UsersTokensBatch        bool     `json:"users_tokens_batch"`
	ChannelsBatch           bool     `json:"channels_batch"`
	ChannelSubscribersBatch bool     `json:"channel_subscribers_batch"`
	Snapshot                bool     `json:"snapshot"`
	ChannelTypes            []string `json:"channel_types"`
}

// CapabilitiesLimits lists configured request limits exposed to wkbench.
type CapabilitiesLimits struct {
	MaxBatchSize    int   `json:"max_batch_size"`
	MaxPayloadBytes int64 `json:"max_payload_bytes"`
}

// TokensRequest updates benchmark user tokens in one batch.
type TokensRequest struct {
	RunID   string             `json:"run_id"`
	BatchID string             `json:"batch_id"`
	Upsert  bool               `json:"upsert,omitempty"`
	Users   []UserTokenCommand `json:"users,omitempty"`
	Items   []UserTokenCommand `json:"items,omitempty"`
}

// ChannelsRequest upserts benchmark channels in one batch.
type ChannelsRequest struct {
	RunID    string          `json:"run_id"`
	BatchID  string          `json:"batch_id"`
	Upsert   bool            `json:"upsert,omitempty"`
	Channels []ChannelRecord `json:"channels,omitempty"`
	Items    []ChannelRecord `json:"items,omitempty"`
}

// SubscribersRequest appends benchmark subscribers in one batch.
type SubscribersRequest struct {
	RunID   string           `json:"run_id"`
	BatchID string           `json:"batch_id"`
	Items   []SubscriberItem `json:"items"`
}

// SubscriberItem carries subscribers to append for one group channel.
type SubscriberItem struct {
	ChannelID   string   `json:"channel_id"`
	ChannelType uint8    `json:"channel_type"`
	Reset       bool     `json:"reset,omitempty"`
	Subscribers []string `json:"subscribers"`
}

// MutationResponse reports accepted item counts for idempotent bench mutations.
type MutationResponse struct {
	RunID    string `json:"run_id"`
	BatchID  string `json:"batch_id"`
	Accepted int    `json:"accepted"`
}

// SubscribersResponse reports accepted subscriber items and UID counts.
type SubscribersResponse struct {
	RunID               string `json:"run_id"`
	BatchID             string `json:"batch_id"`
	Accepted            int    `json:"accepted"`
	AcceptedSubscribers int    `json:"accepted_subscribers"`
}

// SnapshotResponse returns a lightweight setup snapshot for benchmark clients.
type SnapshotResponse struct {
	Version string         `json:"version"`
	Counts  map[string]int `json:"counts,omitempty"`
}
