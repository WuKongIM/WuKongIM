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
	type channelOperationKey struct {
		key ch.ChannelKey
		id  ch.ChannelID
	}
	channelKinds := make(map[channelOperationKey]ExchangeKind, len(batch.Items))
	mutations := make([]Mutation, 0, len(batch.Items))
	mutationPositions := make([]int, 0, len(batch.Items))
	loads := make([]LoadRequest, 0, len(batch.Items))
	loadPositions := make([]int, 0, len(batch.Items))
	totalBytes := 0
	for index, item := range batch.Items {
		if item.RequestID == 0 {
			return ExchangeBatchResult{}, ch.ErrInvalidConfig
		}
		if _, exists := seen[item.RequestID]; exists {
			return ExchangeBatchResult{}, ch.ErrInvalidConfig
		}
		seen[item.RequestID] = struct{}{}
		itemBytes := 0
		var operationKey channelOperationKey
		switch item.Kind {
		case ExchangeReplicate:
			if item.Replicate == nil || item.Probe != nil {
				return ExchangeBatchResult{}, ch.ErrInvalidConfig
			}
			request := *item.Replicate
			if request.Leader != from || request.Follower != s.cfg.LocalNode || !request.Valid() {
				return ExchangeBatchResult{}, ch.ErrInvalidConfig
			}
			itemBytes = estimateReplicateRequestBytes(request)
			operationKey = channelOperationKey{key: request.ChannelKey, id: request.ChannelID}
			mutations = append(mutations, Mutation{
				ChannelKey: request.ChannelKey, ChannelID: request.ChannelID,
				Manifest: request.Manifest, Records: request.Records, Committed: request.Committed,
			})
			mutationPositions = append(mutationPositions, index)
		case ExchangeProbe:
			if item.Probe == nil || item.Replicate != nil {
				return ExchangeBatchResult{}, ch.ErrInvalidConfig
			}
			request := *item.Probe
			if request.Leader != from || request.Follower != s.cfg.LocalNode || !request.Valid() {
				return ExchangeBatchResult{}, ch.ErrInvalidConfig
			}
			itemBytes = estimateProbeRequestBytes(request)
			operationKey = channelOperationKey{key: request.ChannelKey, id: request.ChannelID}
			loads = append(loads, LoadRequest{
				ChannelKey: request.ChannelKey, ChannelID: request.ChannelID,
				ProbeIndexes: append([]uint64(nil), request.Indexes...),
			})
			loadPositions = append(loadPositions, index)
		default:
			return ExchangeBatchResult{}, ch.ErrInvalidConfig
		}
		if previous, exists := channelKinds[operationKey]; exists && previous != item.Kind {
			return ExchangeBatchResult{}, ch.ErrInvalidConfig
		}
		channelKinds[operationKey] = item.Kind
		if itemBytes > s.cfg.MaxBatchBytes || totalBytes > s.cfg.MaxBatchBytes-itemBytes {
			return ExchangeBatchResult{}, ch.ErrBackpressured
		}
		totalBytes += itemBytes
	}
	response := ExchangeBatchResult{Version: ExchangeVersion, Items: make([]ExchangeItemResult, len(batch.Items))}
	for index, item := range batch.Items {
		response.Items[index].RequestID = item.RequestID
	}
	if len(loads) > 0 {
		loaded, err := s.cfg.Store.Load(ctx, LoadBatch{Items: loads})
		if err != nil || len(loaded.Items) != len(loads) {
			return ExchangeBatchResult{}, errInvalidExchangeResult
		}
		for index, result := range loaded.Items {
			request := *batch.Items[loadPositions[index]].Probe
			probe, ok := mapProbeResult(request, result)
			if !ok {
				return ExchangeBatchResult{}, errInvalidExchangeResult
			}
			response.Items[loadPositions[index]].Probe = probe
		}
	}
	if len(mutations) > 0 {
		results := s.cfg.Store.Sync(ctx, mutations)
		if len(results) != len(mutations) {
			return ExchangeBatchResult{}, errInvalidExchangeResult
		}
		for index, result := range results {
			position := mutationPositions[index]
			mapped, ok := mapMutationResult(*batch.Items[position].Replicate, mutations[index], result)
			if !ok {
				return ExchangeBatchResult{}, errInvalidExchangeResult
			}
			response.Items[position].Replicate = mapped
		}
	}
	return response, nil
}

func mapProbeResult(request ProbeRequest, result LoadResult) (ProbeResult, bool) {
	if result.Err != nil || !validReplicaState(result.State) || len(result.Entries) != len(request.Indexes) {
		return ProbeResult{}, false
	}
	entries := make([]EntryProbe, len(result.Entries))
	for index, entry := range result.Entries {
		if entry.Index != request.Indexes[index] || entry.Present != (entry.Identity != (ch.EntryIdentity{})) ||
			(entry.Present && entry.Identity.Index != entry.Index) ||
			(entry.Present && !validEntryIdentity(entry.Identity)) ||
			(entry.Index <= result.State.LEO && !entry.Present) || (entry.Index > result.State.LEO && entry.Present) {
			return ProbeResult{}, false
		}
		entries[index] = entry
	}
	if !validProbeEntryChain(entries) {
		return ProbeResult{}, false
	}
	return ProbeResult{State: result.State, Entries: entries}, true
}

func validReplicaState(state ReplicaState) bool {
	if state.LEO == 0 {
		return state.Committed == 0 && state.Manifest == (ch.ProposalManifest{}) && state.TailIdentity == (ch.EntryIdentity{})
	}
	manifest := state.Manifest
	tail := state.TailIdentity
	return state.Committed <= state.LEO && manifest.StructurallyValid() && manifest.LastOffset == state.LEO &&
		validEntryIdentity(tail) && tail.Index == state.LEO && tail.Version == manifest.Version && tail.ChannelEpoch == manifest.ChannelEpoch &&
		tail.LeaderTerm == manifest.LeaderTerm && tail.FenceVersion == manifest.FenceVersion &&
		tail.CommandID == manifest.CommandID && tail.Digest == manifest.Digest
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
