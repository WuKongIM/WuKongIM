package replication

import (
	"context"
	"errors"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
)

const (
	maxRecoveryProbeIndexes         = 256
	maxRecoveryReplacementProposals = 256
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
		if len(item.ProbeIndexes) > maxRecoveryProbeIndexes || !validProbeIndexes(item.ProbeIndexes) {
			return LoadBatchResult{}, ch.ErrInvalidConfig
		}
		probeBytes, ok := boundedProduct(a.cfg.MaxBatchBytes, len(item.ProbeIndexes), 192)
		if !ok {
			return LoadBatchResult{}, ch.ErrBackpressured
		}
		itemBytes, ok := boundedByteSize(a.cfg.MaxBatchBytes, 64, len(item.ChannelKey), len(item.ChannelID.ID), probeBytes)
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
		state, entries, loadErr := loadExactRecoveryState(ctx, store, item.ProbeIndexes)
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
		result.Items[index].Entries = entries
	}
	return result, nil
}

func loadExactRecoveryState(ctx context.Context, store channelstore.ChannelStore, indexes []uint64) (channelstore.ExactState, []EntryProbe, error) {
	if len(indexes) == 0 {
		loader, ok := store.(channelstore.ExactStateLoader)
		if !ok {
			return channelstore.ExactState{}, nil, ch.ErrInvalidConfig
		}
		state, err := loader.LoadExactState(ctx)
		return state, nil, err
	}
	loader, ok := store.(channelstore.ExactRecoveryStateLoader)
	if !ok {
		return channelstore.ExactState{}, nil, ch.ErrInvalidConfig
	}
	recovery, err := loader.LoadExactRecoveryState(ctx, indexes)
	if err != nil {
		return channelstore.ExactState{}, nil, err
	}
	if len(recovery.Entries) != len(indexes) {
		return channelstore.ExactState{}, nil, ch.ErrLogConflict
	}
	entries := make([]EntryProbe, len(indexes))
	for position, entry := range recovery.Entries {
		if entry.Index != indexes[position] || entry.Present != (entry.Identity != (ch.EntryIdentity{})) ||
			(entry.Present && entry.Identity.Index != entry.Index) ||
			(entry.Present && !validEntryIdentity(entry.Identity)) ||
			(entry.Index <= recovery.LEO && !entry.Present) || (entry.Index > recovery.LEO && entry.Present) {
			return channelstore.ExactState{}, nil, ch.ErrLogConflict
		}
		entries[position] = EntryProbe{Index: entry.Index, Present: entry.Present, Identity: entry.Identity}
	}
	if !validProbeEntryChain(entries) {
		return channelstore.ExactState{}, nil, ch.ErrLogConflict
	}
	return recovery.ExactState, entries, nil
}

func validProbeIndexes(indexes []uint64) bool {
	seen := make(map[uint64]struct{}, len(indexes))
	for _, index := range indexes {
		if index == 0 {
			return false
		}
		if _, exists := seen[index]; exists {
			return false
		}
		seen[index] = struct{}{}
	}
	return true
}

func validEntryIdentity(identity ch.EntryIdentity) bool {
	if identity.Version != ch.ProposalManifestVersion || identity.ChannelEpoch == 0 || identity.LeaderTerm == 0 ||
		identity.FenceVersion == 0 || identity.Index == 0 || identity.PreviousIndex+1 != identity.Index ||
		identity.CommandID == (ch.CommandID{}) || identity.Digest == (ch.EntryDigest{}) {
		return false
	}
	if identity.PreviousIndex == 0 {
		return identity.PreviousTerm == 0 && identity.PreviousDigest == (ch.EntryDigest{})
	}
	return identity.PreviousTerm != 0 && identity.PreviousDigest != (ch.EntryDigest{})
}

func validProbeEntryChain(entries []EntryProbe) bool {
	byIndex := make(map[uint64]ch.EntryIdentity, len(entries))
	for _, entry := range entries {
		if entry.Present {
			byIndex[entry.Index] = entry.Identity
		}
	}
	for index, identity := range byIndex {
		if index <= 1 {
			continue
		}
		previous, probed := byIndex[index-1]
		if probed && (identity.PreviousIndex != previous.Index || identity.PreviousTerm != previous.LeaderTerm ||
			identity.PreviousDigest != previous.Digest) {
			return false
		}
	}
	return true
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

func (a *storeAdapter) Replace(ctx context.Context, replacements []RecoveryReplacement) []RecoveryReplacementResult {
	results := make([]RecoveryReplacementResult, len(replacements))
	if a == nil || ctx == nil || len(replacements) == 0 || len(replacements) > a.cfg.MaxBatchItems {
		return rejectRecoveryReplacements(results, ch.ErrInvalidConfig)
	}
	totalBytes := 0
	for _, replacement := range replacements {
		itemBytes, validationErr := validateRecoveryReplacement(replacement, a.cfg.MaxBatchBytes)
		if validationErr != nil {
			return rejectRecoveryReplacements(results, validationErr)
		}
		if totalBytes > a.cfg.MaxBatchBytes-itemBytes {
			return rejectRecoveryReplacements(results, ch.ErrBackpressured)
		}
		totalBytes += itemBytes
	}
	if err := ctx.Err(); err != nil {
		return rejectRecoveryReplacements(results, err)
	}
	for index, replacement := range replacements {
		store, err := a.cfg.Factory.ChannelStore(replacement.ChannelKey, replacement.ChannelID)
		if err != nil {
			results[index] = RecoveryReplacementResult{Outcome: ch.AppendOutcomeDefinitelyNotWritten, Err: err}
			continue
		}
		replacer, ok := store.(channelstore.RecoverySuffixReplacer)
		if !ok {
			_ = store.Close()
			results[index] = RecoveryReplacementResult{Outcome: ch.AppendOutcomeDefinitelyNotWritten, Err: ch.ErrInvalidConfig}
			continue
		}
		proposals := make([]channelstore.RecoveryProposal, len(replacement.Proposals))
		for proposalIndex, proposal := range replacement.Proposals {
			proposals[proposalIndex] = channelstore.RecoveryProposal{Manifest: proposal.Manifest, Records: proposal.Records}
		}
		replaceResult, replaceErr := replacer.ReplaceRecoverySuffix(ctx, channelstore.ReplaceRecoverySuffixRequest{
			Expected: channelstore.ExactState{
				InitialState: channelstore.InitialState{
					LEO: replacement.Expected.LEO, HW: replacement.Expected.Committed,
					CheckpointHW: replacement.Expected.Committed,
				},
				Manifest: replacement.Expected.Manifest, TailIdentity: replacement.Expected.TailIdentity,
			},
			KeepThrough: replacement.KeepThrough,
			Proposals:   proposals,
			Committed:   replacement.Committed,
		})
		closeErr := store.Close()
		if replaceErr == nil && closeErr != nil && !replaceResult.Outcome.Durable() {
			replaceErr = closeErr
		}
		results[index] = normalizeRecoveryReplacementResult(replacement, RecoveryReplacementResult{
			Outcome: replaceResult.Outcome, LastOffset: replaceResult.LastOffset, Err: replaceErr,
		})
	}
	return results
}

func validateRecoveryReplacement(replacement RecoveryReplacement, maxBytes int) (int, error) {
	if replacement.ChannelKey == "" || replacement.ChannelID.ID == "" ||
		len(replacement.Proposals) > maxRecoveryReplacementProposals || replacement.KeepThrough > replacement.Expected.LEO ||
		replacement.KeepThrough < replacement.Expected.Committed || replacement.Committed < replacement.Expected.Committed {
		return 0, ch.ErrInvalidConfig
	}
	expected := channelstore.ExactState{
		InitialState: channelstore.InitialState{
			LEO: replacement.Expected.LEO, HW: replacement.Expected.Committed,
			CheckpointHW: replacement.Expected.Committed,
		},
		Manifest: replacement.Expected.Manifest, TailIdentity: replacement.Expected.TailIdentity,
	}
	if validateExactState(expected) != nil {
		return 0, ch.ErrInvalidConfig
	}
	total, ok := boundedByteSize(maxBytes, 256, len(replacement.ChannelKey), len(replacement.ChannelID.ID))
	if !ok {
		return 0, ch.ErrBackpressured
	}
	base := replacement.KeepThrough
	for _, proposal := range replacement.Proposals {
		mutation := Mutation{
			ChannelKey: replacement.ChannelKey, ChannelID: replacement.ChannelID,
			Manifest: proposal.Manifest, Records: proposal.Records,
		}
		if proposal.Manifest.BaseOffset != base || !validMutation(mutation) {
			return 0, ch.ErrInvalidConfig
		}
		itemBytes, ok := estimateMutationBytes(mutation, maxBytes-total)
		if !ok {
			return 0, ch.ErrBackpressured
		}
		total += itemBytes
		base = proposal.Manifest.LastOffset
	}
	if replacement.Committed > base {
		return 0, ch.ErrInvalidConfig
	}
	return total, nil
}

func normalizeRecoveryReplacementResult(replacement RecoveryReplacement, result RecoveryReplacementResult) RecoveryReplacementResult {
	lastOffset := replacement.KeepThrough
	if len(replacement.Proposals) > 0 {
		lastOffset = replacement.Proposals[len(replacement.Proposals)-1].Manifest.LastOffset
	}
	valid := result.Outcome.Valid()
	if result.Outcome.Durable() {
		valid = valid && result.Err == nil && result.LastOffset == lastOffset
	} else {
		valid = valid && result.Err != nil && result.LastOffset == 0
	}
	if valid {
		return result
	}
	return RecoveryReplacementResult{Outcome: ch.AppendOutcomeUnknown, Err: ch.ErrInvalidConfig}
}

func rejectRecoveryReplacements(results []RecoveryReplacementResult, err error) []RecoveryReplacementResult {
	outcome := ch.AppendOutcomeDefinitelyNotWritten
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) &&
		!errors.Is(err, ch.ErrInvalidConfig) && !errors.Is(err, ch.ErrBackpressured) {
		outcome = ch.AppendOutcomeUnknown
	}
	for index := range results {
		results[index] = RecoveryReplacementResult{Outcome: outcome, Err: err}
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

func boundedProduct(limit, left, right int) (int, bool) {
	if limit < 0 || left < 0 || right < 0 || (left != 0 && right > limit/left) {
		return 0, false
	}
	return left * right, true
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
		manifest.LastOffset != state.LEO || !validEntryIdentity(tail) || tail.Version != manifest.Version || tail.Index != state.LEO ||
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
