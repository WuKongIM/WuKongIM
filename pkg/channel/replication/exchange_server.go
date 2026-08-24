package replication

import (
	"context"
	"errors"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// ExchangeServerConfig configures one bounded follower exchange endpoint.
type ExchangeServerConfig struct {
	LocalNode     ch.NodeID
	Store         ReplicaStore
	Observer      StageObserver
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
	if s == nil || ctx == nil || from == 0 || batch.Version != ExchangeVersion || !batch.Priority.Valid() || len(batch.Items) == 0 || len(batch.Items) > s.cfg.MaxBatchItems {
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
	fetches := make([]FetchRange, 0, len(batch.Items))
	fetchPositions := make([]int, 0, len(batch.Items))
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
			if item.Replicate == nil || item.Probe != nil || item.Fetch != nil {
				return ExchangeBatchResult{}, ch.ErrInvalidConfig
			}
			request := *item.Replicate
			if request.Leader != from || request.Follower != s.cfg.LocalNode || !request.Valid() {
				return ExchangeBatchResult{}, ch.ErrInvalidConfig
			}
			itemBytes = estimateReplicateRequestBytes(request)
			operationKey = channelOperationKey{key: request.ChannelKey, id: request.ChannelID}
			class := MutationClassFollowerQuorum
			if batch.Priority == ExchangePriorityBackground {
				class = MutationClassTrailing
			}
			mutations = append(mutations, Mutation{
				ChannelKey: request.ChannelKey, ChannelID: request.ChannelID,
				Manifest: request.Manifest, Records: request.Records, Committed: request.Committed,
				Class:                     class,
				ServerAllocatedMessageIDs: request.ServerAllocatedMessageIDs,
			})
			mutationPositions = append(mutationPositions, index)
		case ExchangeProbe:
			if batch.Priority != ExchangePriorityForeground || item.Probe == nil || item.Replicate != nil || item.Fetch != nil {
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
		case ExchangeFetch:
			if batch.Priority != ExchangePriorityForeground || item.Fetch == nil || item.Replicate != nil || item.Probe != nil {
				return ExchangeBatchResult{}, ch.ErrInvalidConfig
			}
			request := *item.Fetch
			if request.Leader != from || request.Follower != s.cfg.LocalNode || !request.Valid() {
				return ExchangeBatchResult{}, ch.ErrInvalidConfig
			}
			itemBytes = estimateFetchRequestBytes(request)
			operationKey = channelOperationKey{key: request.ChannelKey, id: request.ChannelID}
			fetches = append(fetches, FetchRange{
				ChannelKey: request.ChannelKey, ChannelID: request.ChannelID, Expected: request.Expected,
				From: request.From, Through: request.Through, Previous: request.Previous, MaxBytes: request.MaxBytes,
			})
			fetchPositions = append(fetchPositions, index)
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
		startedAt := time.Now()
		results := s.cfg.Store.Sync(ctx, mutations)
		var storeErr error
		if len(results) != len(mutations) {
			storeErr = errInvalidExchangeResult
		} else {
			for index := range results {
				if results[index].Err != nil {
					storeErr = results[index].Err
					break
				}
			}
		}
		storeStage := stageFollowerForegroundStore
		if batch.Priority == ExchangePriorityBackground {
			storeStage = stageFollowerBackgroundStore
		}
		observeReplicationStage(s.cfg.Observer, storeStage, storeErr, time.Since(startedAt))
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
	if len(fetches) > 0 {
		results := s.cfg.Store.Fetch(ctx, fetches)
		if len(results) != len(fetches) {
			return ExchangeBatchResult{}, errInvalidExchangeResult
		}
		for index, result := range results {
			position := fetchPositions[index]
			mapped, ok := mapFetchResult(*batch.Items[position].Fetch, result)
			if !ok {
				return ExchangeBatchResult{}, errInvalidExchangeResult
			}
			response.Items[position].Fetch = mapped
		}
	}
	return response, nil
}

func mapFetchResult(request FetchRequest, result FetchRangeResult) (FetchResult, bool) {
	if result.Err != nil || result.State != request.Expected || !validRecoveryProposals(request, result.Proposals) {
		return FetchResult{}, false
	}
	return FetchResult{
		Proof: fetchProofFor(request), State: result.State, Proposals: cloneRecoveryProposals(result.Proposals),
	}, true
}

func validRecoveryProposals(request FetchRequest, proposals []RecoveryProposal) bool {
	if len(proposals) == 0 {
		return false
	}
	base := request.From - 1
	records := 0
	bytes := 0
	previous := request.Previous
	for _, proposal := range proposals {
		manifest := proposal.Manifest
		if manifest.BaseOffset != base || !manifest.ValidFor(base, len(proposal.Records)) {
			return false
		}
		sealed, entries, ok := ch.SealProposalManifest(manifest, proposal.Records)
		if !ok || sealed != manifest || len(entries) != len(proposal.Records) || entries[len(entries)-1].Digest != manifest.Digest {
			return false
		}
		if manifest.PreviousIndex != previous.Index || manifest.PreviousTerm != previous.LeaderTerm ||
			manifest.PreviousDigest != previous.Digest {
			return false
		}
		for _, record := range proposal.Records {
			recordBytes := 96 + len(record.FromUID) + len(record.ClientMsgNo) + len(record.Payload)
			if bytes > request.MaxBytes-recordBytes {
				return false
			}
			bytes += recordBytes
			records++
		}
		previous = entries[len(entries)-1]
		base = manifest.LastOffset
	}
	return base >= request.From && base <= request.Through && records == int(base-request.From+1) && records <= maxRecoveryProbeIndexes
}

func cloneRecoveryProposals(source []RecoveryProposal) []RecoveryProposal {
	cloned := make([]RecoveryProposal, len(source))
	for index, proposal := range source {
		cloned[index].Manifest = proposal.Manifest
		cloned[index].Records = make([]ch.Record, len(proposal.Records))
		for recordIndex, record := range proposal.Records {
			record.Payload = append([]byte(nil), record.Payload...)
			cloned[index].Records[recordIndex] = record
		}
	}
	return cloned
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
	return ProbeResult{Proof: probeProofFor(request), State: result.State, Entries: entries}, true
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
