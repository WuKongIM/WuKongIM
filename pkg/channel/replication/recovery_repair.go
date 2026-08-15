package replication

import (
	"context"
	"errors"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// recoveryRepairRequest binds bounded local suffix repair to one completed
// quorum-prefix proof. Repair publishes only complete proposal boundaries and
// never makes the Channel writable.
type recoveryRepairRequest struct {
	ChannelKey   ch.ChannelKey
	ChannelID    ch.ChannelID
	Leader       ch.NodeID
	Local        ch.NodeID
	Voters       []ch.NodeID
	Quorum       int
	Selection    recoverySelection
	Timeout      time.Duration
	MaxPageBytes int
}

type recoveryFetchDispatcher interface {
	submitRecoveryFetch(context.Context, recoveryFetchQuery, func(FetchResult, error)) error
}

type recoveryFetchCompletion struct {
	result FetchResult
	err    error
}

// repairQuorumPrefix truncates a minority suffix and installs the selected
// chain in bounded, proposal-aligned atomic pages. A crash between pages leaves
// a non-writable exact prefix that a later Install can prove and resume.
func repairQuorumPrefix(ctx context.Context, request recoveryRepairRequest, dispatcher recoveryFetchDispatcher, store ReplicaStore) (ReplicaState, error) {
	if ctx == nil || dispatcher == nil || store == nil || request.ChannelKey == "" || request.ChannelID.ID == "" ||
		request.Leader == 0 || request.Local == 0 || request.Leader != request.Local || request.Timeout <= 0 || request.MaxPageBytes <= 0 {
		return ReplicaState{}, ch.ErrInvalidConfig
	}
	configured, err := validateRecoveryTopology(request.Voters, request.Quorum)
	if err != nil {
		return ReplicaState{}, err
	}
	if _, ok := configured[request.Local]; !ok || !validRecoveryRepairSelection(request.Selection, configured, request.Quorum) {
		return ReplicaState{}, ch.ErrInvalidConfig
	}
	operationContext, cancel := context.WithTimeout(ctx, request.Timeout)
	defer cancel()

	localResult, err := loadRecoveryReplicaState(operationContext, store, request.ChannelKey, request.ChannelID, nil)
	if err != nil {
		return ReplicaState{}, err
	}
	local := localResult.State
	selection := request.Selection
	if local.Committed > selection.Index {
		return ReplicaState{}, ch.ErrLogConflict
	}
	keepThrough := local.Committed
	previous := ch.EntryIdentity{}
	if keepThrough > 0 {
		probed, probeErr := loadRecoveryReplicaState(operationContext, store, request.ChannelKey, request.ChannelID, []uint64{keepThrough})
		if probeErr != nil {
			return ReplicaState{}, probeErr
		}
		if probed.State != local || len(probed.Entries) != 1 || !probed.Entries[0].Present {
			return ReplicaState{}, ch.ErrStaleMeta
		}
		previous = probed.Entries[0].Identity
		if keepThrough == selection.CertifiedCommitted && previous != selection.CertifiedIdentity {
			return ReplicaState{}, ch.ErrLogConflict
		}
		if keepThrough == selection.Index && previous != selection.Identity {
			return ReplicaState{}, ch.ErrLogConflict
		}
	}
	if local.LEO == selection.Index && local.TailIdentity == selection.Identity && local.Committed == selection.Index {
		return local, nil
	}

	current := local
	from := keepThrough + 1
	firstPage := true
	for from <= selection.Index {
		through := selection.Index
		if distance := through - from; distance >= maxRecoveryProbeIndexes {
			through = from + maxRecoveryProbeIndexes - 1
		}
		page, fetchErr := fetchRecoveryPage(operationContext, request, from, through, previous, dispatcher)
		if fetchErr != nil {
			return ReplicaState{}, fetchErr
		}
		last, tail, pageErr := recoveryPageTail(from, through, previous, page.Proposals, request.MaxPageBytes)
		if pageErr != nil {
			return ReplicaState{}, pageErr
		}
		if previous.Index < selection.CertifiedCommitted && last >= selection.CertifiedCommitted {
			certified, present := recoveryProposalIdentityAt(page.Proposals, selection.CertifiedCommitted)
			if !present || certified != selection.CertifiedIdentity {
				return ReplicaState{}, ch.ErrLogConflict
			}
		}
		pageKeep := current.LEO
		if firstPage {
			pageKeep = keepThrough
		}
		committed := last
		replaced := store.Replace(operationContext, []RecoveryReplacement{{
			ChannelKey: request.ChannelKey, ChannelID: request.ChannelID, Expected: current,
			KeepThrough: pageKeep, Proposals: page.Proposals, Committed: committed,
		}})
		if len(replaced) != 1 || !replaced[0].Outcome.Durable() || replaced[0].Err != nil || replaced[0].LastOffset != last {
			if len(replaced) == 1 && replaced[0].Err != nil {
				return ReplicaState{}, replaced[0].Err
			}
			return ReplicaState{}, ch.ErrLogConflict
		}
		loaded, loadErr := loadRecoveryReplicaState(operationContext, store, request.ChannelKey, request.ChannelID, nil)
		if loadErr != nil {
			return ReplicaState{}, loadErr
		}
		want := ReplicaState{LEO: last, Committed: committed, Manifest: page.Proposals[len(page.Proposals)-1].Manifest, TailIdentity: tail}
		if loaded.State != want {
			return ReplicaState{}, ch.ErrLogConflict
		}
		current = loaded.State
		previous = tail
		from = last + 1
		firstPage = false
	}
	if from == 1 && selection.Index == 0 {
		replaced := store.Replace(operationContext, []RecoveryReplacement{{
			ChannelKey: request.ChannelKey, ChannelID: request.ChannelID, Expected: current,
			KeepThrough: 0, Committed: 0,
		}})
		if len(replaced) != 1 || !replaced[0].Outcome.Durable() || replaced[0].Err != nil || replaced[0].LastOffset != 0 {
			if len(replaced) == 1 && replaced[0].Err != nil {
				return ReplicaState{}, replaced[0].Err
			}
			return ReplicaState{}, ch.ErrLogConflict
		}
		current = ReplicaState{}
	}
	if current.LEO != selection.Index || current.Committed != selection.Index || current.TailIdentity != selection.Identity {
		return ReplicaState{}, ch.ErrLogConflict
	}
	return current, nil
}

func recoveryProposalIdentityAt(proposals []RecoveryProposal, index uint64) (ch.EntryIdentity, bool) {
	for _, proposal := range proposals {
		if index <= proposal.Manifest.BaseOffset || index > proposal.Manifest.LastOffset {
			continue
		}
		_, entries, ok := ch.SealProposalManifest(proposal.Manifest, proposal.Records)
		position := index - proposal.Manifest.BaseOffset - 1
		if !ok || position >= uint64(len(entries)) {
			return ch.EntryIdentity{}, false
		}
		return entries[position], true
	}
	return ch.EntryIdentity{}, false
}

func validRecoveryRepairSelection(selection recoverySelection, configured map[ch.NodeID]struct{}, quorum int) bool {
	if selection.Continuation != nil || selection.CertifiedCommitted > selection.Index {
		return false
	}
	if selection.Index == 0 {
		return selection.Identity == (ch.EntryIdentity{}) && selection.CertifiedCommitted == 0 &&
			selection.CertifiedIdentity == (ch.EntryIdentity{}) && len(selection.Supporters) == 0
	}
	if !validEntryIdentity(selection.Identity) || selection.Identity.Index != selection.Index || len(selection.Supporters) < quorum {
		return false
	}
	if selection.CertifiedCommitted == 0 {
		if selection.CertifiedIdentity != (ch.EntryIdentity{}) {
			return false
		}
	} else if !validEntryIdentity(selection.CertifiedIdentity) || selection.CertifiedIdentity.Index != selection.CertifiedCommitted {
		return false
	}
	seen := make(map[ch.NodeID]struct{}, len(selection.Supporters))
	for _, supporter := range selection.Supporters {
		if _, ok := configured[supporter.Voter]; !ok || !validReplicaState(supporter.State) || supporter.State.LEO < selection.Index {
			return false
		}
		if _, duplicate := seen[supporter.Voter]; duplicate {
			return false
		}
		seen[supporter.Voter] = struct{}{}
	}
	return true
}

func loadRecoveryReplicaState(ctx context.Context, store ReplicaStore, key ch.ChannelKey, id ch.ChannelID, indexes []uint64) (LoadResult, error) {
	loaded, err := store.Load(ctx, LoadBatch{Items: []LoadRequest{{
		ChannelKey: key, ChannelID: id, ProbeIndexes: append([]uint64(nil), indexes...),
	}}})
	if err != nil {
		return LoadResult{}, err
	}
	if len(loaded.Items) != 1 {
		return LoadResult{}, ch.ErrLogConflict
	}
	if loaded.Items[0].Err != nil {
		return LoadResult{}, loaded.Items[0].Err
	}
	if !validReplicaState(loaded.Items[0].State) || !sameRecoveryProbeIndexes(indexes, loaded.Items[0].Entries) {
		return LoadResult{}, ch.ErrLogConflict
	}
	return loaded.Items[0], nil
}

func fetchRecoveryPage(ctx context.Context, request recoveryRepairRequest, from, through uint64, previous ch.EntryIdentity, dispatcher recoveryFetchDispatcher) (FetchResult, error) {
	var lastErr error
	for _, supporter := range request.Selection.Supporters {
		query := recoveryFetchQuery{
			ChannelKey: request.ChannelKey, ChannelID: request.ChannelID,
			Leader: request.Leader, Donor: supporter.Voter, Expected: supporter.State,
			From: from, Through: through, Previous: previous, MaxBytes: request.MaxPageBytes,
		}
		completion := make(chan recoveryFetchCompletion, 1)
		if err := dispatcher.submitRecoveryFetch(ctx, query, func(result FetchResult, err error) {
			completion <- recoveryFetchCompletion{result: result, err: err}
		}); err != nil {
			lastErr = err
			continue
		}
		select {
		case <-ctx.Done():
			return FetchResult{}, ctx.Err()
		case completed := <-completion:
			if completed.err != nil {
				lastErr = completed.err
				continue
			}
			fetchRequest := FetchRequest{
				ChannelKey: query.ChannelKey, ChannelID: query.ChannelID,
				Leader: query.Leader, Follower: query.Donor, Expected: query.Expected,
				From: query.From, Through: query.Through, Previous: query.Previous, MaxBytes: query.MaxBytes,
			}
			if !validPeerFetchResult(fetchRequest, completed.result) {
				lastErr = ch.ErrLogConflict
				continue
			}
			return completed.result, nil
		}
	}
	if lastErr != nil {
		if errors.Is(lastErr, context.Canceled) || errors.Is(lastErr, context.DeadlineExceeded) {
			return FetchResult{}, lastErr
		}
		return FetchResult{}, lastErr
	}
	return FetchResult{}, errRecoveryQuorumUnavailable
}

func recoveryPageTail(from, through uint64, previous ch.EntryIdentity, proposals []RecoveryProposal, maxBytes int) (uint64, ch.EntryIdentity, error) {
	request := FetchRequest{From: from, Through: through, Previous: previous, MaxBytes: maxBytes}
	if !validRecoveryProposals(request, proposals) {
		return 0, ch.EntryIdentity{}, ch.ErrLogConflict
	}
	lastProposal := proposals[len(proposals)-1]
	_, entries, ok := ch.SealProposalManifest(lastProposal.Manifest, lastProposal.Records)
	if !ok || len(entries) == 0 {
		return 0, ch.EntryIdentity{}, ch.ErrLogConflict
	}
	return lastProposal.Manifest.LastOffset, entries[len(entries)-1], nil
}
