package replication

import (
	"context"
	"errors"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// ExchangeServerConfig configures one bounded follower exchange endpoint.
type ExchangeServerConfig struct {
	LocalNode     ch.NodeID
	Store         ReplicaStore
	MaxBatchItems int
	MaxBatchBytes int
}

// ExchangeServer validates data-bearing peer work before synchronous storage.
type ExchangeServer struct {
	cfg ExchangeServerConfig
}

// NewExchangeServer creates one exact follower exchange endpoint.
func NewExchangeServer(cfg ExchangeServerConfig) (*ExchangeServer, error) {
	if cfg.LocalNode == 0 || cfg.Store == nil || cfg.MaxBatchItems <= 0 || cfg.MaxBatchBytes <= 0 {
		return nil, ch.ErrInvalidConfig
	}
	return &ExchangeServer{cfg: cfg}, nil
}

// Handle validates and synchronously applies one bounded data-bearing batch.
func (s *ExchangeServer) Handle(ctx context.Context, from ch.NodeID, batch ExchangeBatch) (ExchangeBatchResult, error) {
	if s == nil || ctx == nil || from == 0 || batch.Version != ExchangeVersion || len(batch.Items) == 0 || len(batch.Items) > s.cfg.MaxBatchItems {
		return ExchangeBatchResult{}, ch.ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return ExchangeBatchResult{}, err
	}
	seen := make(map[uint64]struct{}, len(batch.Items))
	mutations := make([]Mutation, len(batch.Items))
	totalBytes := 0
	for index, item := range batch.Items {
		if item.RequestID == 0 || item.Kind != ExchangeReplicate || item.Replicate == nil {
			return ExchangeBatchResult{}, ch.ErrInvalidConfig
		}
		if _, exists := seen[item.RequestID]; exists {
			return ExchangeBatchResult{}, ch.ErrInvalidConfig
		}
		seen[item.RequestID] = struct{}{}
		request := *item.Replicate
		if request.Leader != from || request.Follower != s.cfg.LocalNode || !request.Valid() {
			return ExchangeBatchResult{}, ch.ErrInvalidConfig
		}
		itemBytes := estimateReplicateRequestBytes(request)
		if itemBytes > s.cfg.MaxBatchBytes || totalBytes > s.cfg.MaxBatchBytes-itemBytes {
			return ExchangeBatchResult{}, ch.ErrBackpressured
		}
		totalBytes += itemBytes
		mutations[index] = Mutation{
			ChannelKey: request.ChannelKey, ChannelID: request.ChannelID,
			Manifest: request.Manifest, Records: request.Records, Committed: request.Committed,
		}
	}
	results := s.cfg.Store.Sync(ctx, mutations)
	if len(results) != len(mutations) {
		return ExchangeBatchResult{}, errInvalidExchangeResult
	}
	response := ExchangeBatchResult{Version: ExchangeVersion, Items: make([]ExchangeItemResult, len(results))}
	for index, result := range results {
		mapped, ok := mapMutationResult(*batch.Items[index].Replicate, mutations[index], result)
		if !ok {
			return ExchangeBatchResult{}, errInvalidExchangeResult
		}
		response.Items[index] = ExchangeItemResult{RequestID: batch.Items[index].RequestID, Replicate: mapped}
	}
	return response, nil
}

func mapMutationResult(request ReplicateRequest, mutation Mutation, result MutationResult) (ReplicateResult, bool) {
	if !result.Outcome.Valid() {
		return ReplicateResult{}, false
	}
	if result.Outcome.Durable() {
		if result.Err != nil || result.LastOffset != mutation.Manifest.LastOffset || result.NeedFrom != 0 {
			return ReplicateResult{}, false
		}
		status := ReplicateDurable
		if result.Outcome == ch.AppendOutcomeAlreadyDurable {
			status = ReplicateAlreadyDurable
		}
		return ReplicateResult{Status: status, LastOffset: result.LastOffset, Proof: replicateProofFor(request)}, true
	}
	if result.Err == nil || result.LastOffset != 0 {
		return ReplicateResult{}, false
	}
	switch result.Outcome {
	case ch.AppendOutcomeConflict:
		if result.NeedFrom > 0 && result.NeedFrom <= mutation.Manifest.LastOffset {
			return ReplicateResult{Status: ReplicateNeedFrom, NeedFrom: result.NeedFrom}, true
		}
		if result.NeedFrom != 0 {
			return ReplicateResult{}, false
		}
		return ReplicateResult{Status: ReplicateConflict}, true
	case ch.AppendOutcomeDefinitelyNotWritten:
		if result.NeedFrom != 0 {
			return ReplicateResult{}, false
		}
		if errors.Is(result.Err, ch.ErrStaleMeta) || errors.Is(result.Err, ch.ErrWriteFenced) || errors.Is(result.Err, ch.ErrNotReplica) {
			return ReplicateResult{Status: ReplicateStaleFence}, true
		}
		return ReplicateResult{Status: ReplicateBackpressured}, true
	case ch.AppendOutcomeUnknown:
		if result.NeedFrom != 0 {
			return ReplicateResult{}, false
		}
		return ReplicateResult{Status: ReplicateOutcomeUnknown}, true
	default:
		return ReplicateResult{}, false
	}
}
