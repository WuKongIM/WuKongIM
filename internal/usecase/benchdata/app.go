package benchdata

import (
	"context"
	"errors"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const versionV1 = "bench/v1"

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
			ChannelTypes: []string{"group"},
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
	if err := validateMutationHeader(req.RunID, req.BatchID); err != nil {
		return resp, err
	}
	if err := a.validateBatchSize(len(req.Items)); err != nil {
		return resp, err
	}
	if a.users == nil {
		return resp, errors.New("benchdata: user writer required")
	}
	for _, item := range req.Items {
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
	if err := validateMutationHeader(req.RunID, req.BatchID); err != nil {
		return resp, err
	}
	if err := a.validateBatchSize(len(req.Items)); err != nil {
		return resp, err
	}
	for _, item := range req.Items {
		if err := validateGroupChannelType(item.ChannelType); err != nil {
			return resp, err
		}
	}
	if a.channels == nil {
		return resp, errors.New("benchdata: channel writer required")
	}
	for _, item := range req.Items {
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
	for _, item := range req.Items {
		if item.Reset {
			return resp, errors.New("benchdata: reset=true is not supported for bench/v1 subscribers")
		}
		if err := validateGroupChannelType(item.ChannelType); err != nil {
			return resp, err
		}
	}
	if a.channels == nil {
		return resp, errors.New("benchdata: channel writer required")
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
		return errors.New("benchdata: run_id is required")
	case batchID == "":
		return errors.New("benchdata: batch_id is required")
	default:
		return nil
	}
}

func (a *App) validateBatchSize(n int) error {
	if a.maxBatchSize > 0 && n > a.maxBatchSize {
		return fmt.Errorf("benchdata: batch size %d exceeds max %d", n, a.maxBatchSize)
	}
	return nil
}

func validateGroupChannelType(channelType uint8) error {
	if channelType != frame.ChannelTypeGroup {
		return fmt.Errorf("benchdata: bench/v1 supports only group channel_type=%d", frame.ChannelTypeGroup)
	}
	return nil
}
