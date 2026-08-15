package replication

import (
	"context"
	"errors"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
)

// StoreAdapterConfig bounds one local ReplicaStore adapter.
type StoreAdapterConfig struct {
	// Factory opens exact per-Channel durable state handles.
	Factory channelstore.Factory
	// MaxBatchItems caps positional Load and Sync admission.
	MaxBatchItems int
	// MaxBatchBytes caps identity bytes for Load and immutable mutation bytes for Sync.
	MaxBatchBytes int
}

type storeAdapter struct {
	cfg StoreAdapterConfig
}

// NewStoreAdapter adapts the Channel store factory to the exact ReplicaStore
// seam used by the durable quorum log.
func NewStoreAdapter(cfg StoreAdapterConfig) (ReplicaStore, error) {
	if cfg.Factory == nil || cfg.MaxBatchItems <= 0 || cfg.MaxBatchBytes <= 0 {
		return nil, ch.ErrInvalidConfig
	}
	return &storeAdapter{cfg: cfg}, nil
}

func (a *storeAdapter) Load(ctx context.Context, batch LoadBatch) (LoadBatchResult, error) {
	if a == nil || ctx == nil || len(batch.Items) == 0 || len(batch.Items) > a.cfg.MaxBatchItems {
		return LoadBatchResult{}, ch.ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return LoadBatchResult{}, err
	}
	bytes := 0
	for _, item := range batch.Items {
		itemBytes, ok := boundedByteSize(a.cfg.MaxBatchBytes, 64, len(item.ChannelKey), len(item.ChannelID.ID))
		if !ok || bytes > a.cfg.MaxBatchBytes-itemBytes {
			return LoadBatchResult{}, ch.ErrBackpressured
		}
		bytes += itemBytes
	}
	result := LoadBatchResult{Items: make([]LoadResult, len(batch.Items))}
	for index, item := range batch.Items {
		if item.ChannelKey == "" || item.ChannelID.ID == "" {
			result.Items[index].Err = ch.ErrInvalidConfig
			continue
		}
		store, err := a.cfg.Factory.ChannelStore(item.ChannelKey, item.ChannelID)
		if err != nil {
			result.Items[index].Err = err
			continue
		}
		loader, ok := store.(channelstore.ExactStateLoader)
		if !ok {
			result.Items[index].Err = ch.ErrInvalidConfig
			_ = store.Close()
			continue
		}
		state, loadErr := loader.LoadExactState(ctx)
		closeErr := store.Close()
		if loadErr == nil && closeErr != nil {
			loadErr = closeErr
		}
		if loadErr == nil {
			loadErr = validateExactState(state)
		}
		if loadErr != nil {
			result.Items[index].Err = loadErr
			continue
		}
		result.Items[index].State = ReplicaState{
			LEO: state.LEO, Committed: state.HW,
			Manifest: state.Manifest, TailIdentity: state.TailIdentity,
		}
	}
	return result, nil
}

func (a *storeAdapter) Sync(ctx context.Context, mutations []Mutation) []MutationResult {
	results := make([]MutationResult, len(mutations))
	if a == nil || ctx == nil || len(mutations) == 0 || len(mutations) > a.cfg.MaxBatchItems {
		return rejectMutations(results, ch.ErrInvalidConfig)
	}
	bytes := 0
	for _, mutation := range mutations {
		if !validMutation(mutation) {
			return rejectMutations(results, ch.ErrInvalidConfig)
		}
		itemBytes, ok := estimateMutationBytes(mutation, a.cfg.MaxBatchBytes)
		if !ok || bytes > a.cfg.MaxBatchBytes-itemBytes {
			return rejectMutations(results, ch.ErrBackpressured)
		}
		bytes += itemBytes
	}
	if err := ctx.Err(); err != nil {
		return rejectMutations(results, err)
	}
	if batcher, ok := a.cfg.Factory.(channelstore.LeaderAppendBatcher); ok {
		items := make([]channelstore.AppendLeaderBatchItem, len(mutations))
		for index, mutation := range mutations {
			items[index] = channelstore.AppendLeaderBatchItem{
				ChannelKey: mutation.ChannelKey,
				ChannelID:  mutation.ChannelID,
				Request: channelstore.AppendLeaderRequest{
					Records: mutation.Records, Committed: mutation.Committed, ExactBaseOffset: true,
					ExpectedBaseOffset: mutation.Manifest.BaseOffset, Proposal: mutation.Manifest,
				},
			}
		}
		batchResults := batcher.AppendLeaderBatch(ctx, items)
		if len(batchResults) != len(mutations) {
			return rejectMutationsUnknown(results, ch.ErrInvalidConfig)
		}
		for index, result := range batchResults {
			results[index] = normalizeMutationResult(mutations[index], MutationResult{
				Outcome: result.Outcome, LastOffset: result.LastOffset, NeedFrom: result.NeedFrom, Err: result.Err,
			})
		}
		return results
	}
	for index, mutation := range mutations {
		store, err := a.cfg.Factory.ChannelStore(mutation.ChannelKey, mutation.ChannelID)
		if err != nil {
			results[index] = MutationResult{Outcome: ch.AppendOutcomeDefinitelyNotWritten, Err: err}
			continue
		}
		appendResult, appendErr := store.AppendLeader(ctx, channelstore.AppendLeaderRequest{
			Records: mutation.Records, Committed: mutation.Committed, ExactBaseOffset: true,
			ExpectedBaseOffset: mutation.Manifest.BaseOffset, Proposal: mutation.Manifest,
		})
		closeErr := store.Close()
		if appendErr == nil && closeErr != nil && !appendResult.Outcome.Durable() {
			appendErr = closeErr
		}
		results[index] = normalizeMutationResult(mutation, MutationResult{
			Outcome: appendResult.Outcome, LastOffset: appendResult.LastOffset,
			NeedFrom: appendResult.NeedFrom, Err: appendErr,
		})
	}
	return results
}

func rejectMutationsUnknown(results []MutationResult, err error) []MutationResult {
	for index := range results {
		results[index] = MutationResult{Outcome: ch.AppendOutcomeUnknown, Err: err}
	}
	return results
}

func validMutation(mutation Mutation) bool {
	if mutation.ChannelKey == "" || mutation.ChannelID.ID == "" ||
		!mutation.Manifest.ValidFor(mutation.Manifest.BaseOffset, len(mutation.Records)) ||
		mutation.Committed > mutation.Manifest.LastOffset {
		return false
	}
	_, entries, ok := ch.SealProposalManifest(mutation.Manifest, mutation.Records)
	return ok && len(entries) == len(mutation.Records) && entries[len(entries)-1].Digest == mutation.Manifest.Digest
}

func estimateMutationBytes(mutation Mutation, maxBytes int) (int, bool) {
	const fixedBytes = 192
	total, ok := boundedByteSize(maxBytes, fixedBytes, len(mutation.ChannelKey), len(mutation.ChannelID.ID))
	if !ok {
		return 0, false
	}
	for _, record := range mutation.Records {
		itemBytes, ok := boundedByteSize(maxBytes-total, 96, len(record.FromUID), len(record.ClientMsgNo), len(record.Payload))
		if !ok {
			return 0, false
		}
		total += itemBytes
	}
	return total, true
}

func boundedByteSize(limit int, values ...int) (int, bool) {
	if limit < 0 {
		return 0, false
	}
	total := 0
	for _, value := range values {
		if value < 0 || value > limit-total {
			return 0, false
		}
		total += value
	}
	return total, true
}

func validateExactState(state channelstore.ExactState) error {
	if state.LEO == 0 {
		if state.HW != 0 || state.CheckpointHW != 0 || state.Manifest != (ch.ProposalManifest{}) ||
			state.TailIdentity != (ch.EntryIdentity{}) {
			return ch.ErrLogConflict
		}
		return nil
	}
	manifest := state.Manifest
	tail := state.TailIdentity
	if state.HW > state.LEO || state.CheckpointHW != state.HW || !manifest.StructurallyValid() ||
		manifest.LastOffset != state.LEO || tail.Version != manifest.Version || tail.Index != state.LEO ||
		tail.ChannelEpoch != manifest.ChannelEpoch || tail.LeaderTerm != manifest.LeaderTerm ||
		tail.FenceVersion != manifest.FenceVersion || tail.CommandID != manifest.CommandID || tail.Digest != manifest.Digest {
		return ch.ErrLogConflict
	}
	return nil
}

func normalizeMutationResult(mutation Mutation, result MutationResult) MutationResult {
	valid := result.Outcome.Valid()
	if result.Outcome.Durable() {
		valid = valid && result.Err == nil && result.LastOffset == mutation.Manifest.LastOffset && result.NeedFrom == 0
	} else {
		valid = valid && result.Err != nil && result.LastOffset == 0
		if result.NeedFrom != 0 {
			valid = valid && result.Outcome == ch.AppendOutcomeConflict && result.NeedFrom <= mutation.Manifest.LastOffset
		}
	}
	if valid {
		return result
	}
	return MutationResult{Outcome: ch.AppendOutcomeUnknown, Err: ch.ErrInvalidConfig}
}

func rejectMutations(results []MutationResult, err error) []MutationResult {
	outcome := ch.AppendOutcomeDefinitelyNotWritten
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) &&
		!errors.Is(err, ch.ErrInvalidConfig) && !errors.Is(err, ch.ErrBackpressured) {
		outcome = ch.AppendOutcomeUnknown
	}
	for index := range results {
		results[index] = MutationResult{Outcome: outcome, Err: err}
	}
	return results
}
