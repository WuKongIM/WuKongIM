package benchdata

import (
	"context"
	"errors"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const versionV1 = "bench/v1"

// ErrValidation marks deterministic request validation failures.
var ErrValidation = errors.New("benchdata: validation error")

// ErrDependency marks missing benchdata dependencies required to serve a request.
var ErrDependency = errors.New("benchdata: dependency unavailable")

// App coordinates benchmark data setup without depending on HTTP DTOs or stores.
type App struct {
	users           UserWriter
	channels        ChannelWriter
	snapshot        SnapshotReader
	maxBatchSize    int
	maxPayloadBytes int64
}

// New creates a benchmark data setup usecase.
func New(cfg Config) *App {
	return &App{
		users:           cfg.Users,
		channels:        cfg.Channels,
		snapshot:        cfg.Snapshot,
		maxBatchSize:    cfg.MaxBatchSize,
		maxPayloadBytes: cfg.MaxPayloadBytes,
	}
}

// Capabilities returns the enabled bench/v1 feature set and configured limits.
func (a *App) Capabilities(context.Context) CapabilitiesResponse {
	return CapabilitiesResponse{
		Enabled: true,
		Version: versionV1,
		Supports: CapabilitiesSupports{
			UsersTokensBatch:        true,
			ChannelsBatch:           true,
			ChannelSubscribersBatch: true,
			Snapshot:                true,
			ChannelTypes:            []string{"group"},
		},
		Limits: CapabilitiesLimits{
			MaxBatchSize:    a.maxBatchSize,
			MaxPayloadBytes: a.maxPayloadBytes,
		},
	}
}

// UpsertTokens writes benchmark user tokens through the user boundary.
func (a *App) UpsertTokens(ctx context.Context, req TokensRequest) (MutationResponse, error) {
	resp := MutationResponse{RunID: req.RunID, BatchID: req.BatchID}
	items := req.tokenItems()
	if err := validateMutationHeader(req.RunID, req.BatchID); err != nil {
		return resp, err
	}
	if len(items) == 0 {
		return resp, fmt.Errorf("%w: users are required", ErrValidation)
	}
	if err := a.validateBatchSize(len(items)); err != nil {
		return resp, err
	}
	for _, item := range items {
		if err := validateUserToken(item); err != nil {
			return resp, err
		}
	}
	if a.users == nil {
		return resp, fmt.Errorf("%w: user writer required", ErrDependency)
	}
	for _, item := range items {
		if err := a.users.UpdateToken(ctx, item); err != nil {
			return resp, err
		}
		resp.Accepted++
	}
	return resp, nil
}

// UpsertChannels writes benchmark group channels through the channel boundary.
func (a *App) UpsertChannels(ctx context.Context, req ChannelsRequest) (MutationResponse, error) {
	resp := MutationResponse{RunID: req.RunID, BatchID: req.BatchID}
	items := req.channelItems()
	if err := validateMutationHeader(req.RunID, req.BatchID); err != nil {
		return resp, err
	}
	if len(items) == 0 {
		return resp, fmt.Errorf("%w: channels are required", ErrValidation)
	}
	if err := a.validateBatchSize(len(items)); err != nil {
		return resp, err
	}
	for _, item := range items {
		if err := validateChannelRecord(item); err != nil {
			return resp, err
		}
	}
	if a.channels == nil {
		return resp, fmt.Errorf("%w: channel writer required", ErrDependency)
	}
	for _, item := range items {
		if err := a.channels.UpsertChannel(ctx, item); err != nil {
			return resp, err
		}
		resp.Accepted++
	}
	return resp, nil
}

// AddSubscribers appends subscribers to benchmark group channels.
func (a *App) AddSubscribers(ctx context.Context, req SubscribersRequest) (SubscribersResponse, error) {
	resp := SubscribersResponse{RunID: req.RunID, BatchID: req.BatchID}
	if err := validateMutationHeader(req.RunID, req.BatchID); err != nil {
		return resp, err
	}
	if err := a.validateBatchSize(len(req.Items)); err != nil {
		return resp, err
	}
	if len(req.Items) == 0 {
		return resp, fmt.Errorf("%w: items are required", ErrValidation)
	}
	for _, item := range req.Items {
		if err := validateSubscriberItem(item); err != nil {
			return resp, err
		}
	}
	if a.channels == nil {
		return resp, fmt.Errorf("%w: channel writer required", ErrDependency)
	}
	for _, item := range req.Items {
		if err := a.channels.AddSubscribers(ctx, item.ChannelID, item.ChannelType, item.Subscribers); err != nil {
			return resp, err
		}
		resp.Accepted++
		resp.AcceptedSubscribers += len(item.Subscribers)
	}
	return resp, nil
}

func (r TokensRequest) tokenItems() []UserTokenCommand {
	if len(r.Users) > 0 {
		return r.Users
	}
	return r.Items
}

func (r ChannelsRequest) channelItems() []ChannelRecord {
	if len(r.Channels) > 0 {
		return r.Channels
	}
	return r.Items
}

// Snapshot returns benchmark setup state when a reader is configured.
func (a *App) Snapshot(ctx context.Context) (SnapshotResponse, error) {
	if a.snapshot == nil {
		return SnapshotResponse{Version: versionV1}, nil
	}
	resp, err := a.snapshot.Snapshot(ctx)
	if resp.Version == "" {
		resp.Version = versionV1
	}
	return resp, err
}

func validateMutationHeader(runID, batchID string) error {
	switch {
	case runID == "":
		return fmt.Errorf("%w: run_id is required", ErrValidation)
	case batchID == "":
		return fmt.Errorf("%w: batch_id is required", ErrValidation)
	default:
		return nil
	}
}

func (a *App) validateBatchSize(n int) error {
	if a.maxBatchSize > 0 && n > a.maxBatchSize {
		return fmt.Errorf("%w: batch size %d exceeds max %d", ErrValidation, n, a.maxBatchSize)
	}
	return nil
}

func validateUserToken(cmd UserTokenCommand) error {
	switch {
	case cmd.UID == "":
		return fmt.Errorf("%w: uid is required", ErrValidation)
	case hasForbiddenUIDChar(cmd.UID):
		return fmt.Errorf("%w: uid contains forbidden character", ErrValidation)
	case cmd.Token == "":
		return fmt.Errorf("%w: token is required", ErrValidation)
	default:
		return nil
	}
}

func validateChannelRecord(ch ChannelRecord) error {
	if ch.ChannelID == "" {
		return fmt.Errorf("%w: channel_id is required", ErrValidation)
	}
	return validateGroupChannelType(ch.ChannelType)
}

func validateSubscriberItem(item SubscriberItem) error {
	switch {
	case item.ChannelID == "":
		return fmt.Errorf("%w: channel_id is required", ErrValidation)
	case item.Reset:
		return fmt.Errorf("%w: reset=true is not supported for bench/v1 subscribers", ErrValidation)
	case len(item.Subscribers) == 0:
		return fmt.Errorf("%w: subscribers are required", ErrValidation)
	}
	if err := validateGroupChannelType(item.ChannelType); err != nil {
		return err
	}
	for _, uid := range item.Subscribers {
		if uid == "" {
			return fmt.Errorf("%w: subscriber uid is required", ErrValidation)
		}
		if hasForbiddenUIDChar(uid) {
			return fmt.Errorf("%w: subscriber uid contains forbidden character", ErrValidation)
		}
	}
	return nil
}

func validateGroupChannelType(channelType uint8) error {
	if channelType != frame.ChannelTypeGroup {
		return fmt.Errorf("%w: bench/v1 supports only group channel_type=%d", ErrValidation, frame.ChannelTypeGroup)
	}
	return nil
}

func hasForbiddenUIDChar(uid string) bool {
	for _, r := range uid {
		switch r {
		case '@', '#', '&':
			return true
		}
	}
	return false
}
