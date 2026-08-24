package replication

import (
	"context"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// maxRecoveryProbeVoters is a defensive topology bound checked before any
// fanout allocation. Normal Channel replica sets are much smaller.
const maxRecoveryProbeVoters = 256

// recoveryProbeRequest is one bounded leader-owned proof operation. It never
// mutates replica state or makes the Channel writable.
type recoveryProbeRequest struct {
	ChannelKey ch.ChannelKey
	ChannelID  ch.ChannelID
	Leader     ch.NodeID
	Voters     []ch.NodeID
	Quorum     int
	// Timeout bounds one accepted recovery attempt independently of its caller.
	Timeout      time.Duration
	Continuation *recoveryContinuation
}

// recoveryContinuation binds one next page to the exact frontier and stable
// voter subset that proved every preceding page. It carries no log payload.
type recoveryContinuation struct {
	ChannelKey         ch.ChannelKey
	ChannelID          ch.ChannelID
	Leader             ch.NodeID
	Voters             []ch.NodeID
	Quorum             int
	CertifiedCommitted uint64
	CertifiedIdentity  ch.EntryIdentity
	QuorumLEO          uint64
	NextIndex          uint64
	SelectedIndex      uint64
	SelectedIdentity   ch.EntryIdentity
	Stable             []recoveryContinuationVoter
}

type recoveryContinuationVoter struct {
	Voter ch.NodeID
	State ReplicaState
}

// recoveryProbeQuery asks one current voter for the exact frontier and one
// optional position-stable identity page.
type recoveryProbeQuery struct {
	ChannelKey ch.ChannelKey
	ChannelID  ch.ChannelID
	Leader     ch.NodeID
	Voter      ch.NodeID
	Indexes    []uint64
}

// recoveryProbeDispatcher transfers one admitted query to a bounded local or
// peer owner. A nil return transfers exactly one completion callback.
type recoveryProbeDispatcher interface {
	submitRecoveryProbe(context.Context, recoveryProbeQuery, func(ProbeResult, error)) error
}

type recoveryProbeCompletion struct {
	voter  ch.NodeID
	result ProbeResult
	err    error
}

// recoveryFetchQuery asks one previously proved supporter for an exact,
// proposal-aligned donor range. Expected is the supporter's immutable frontier
// from the quorum proof; Previous binds the returned suffix to the proof cut.
type recoveryFetchQuery struct {
	ChannelKey ch.ChannelKey
	ChannelID  ch.ChannelID
	Leader     ch.NodeID
	Donor      ch.NodeID
	Expected   ReplicaState
	From       uint64
	Through    uint64
	Previous   ch.EntryIdentity
	MaxBytes   int
}

// batchingRecoveryProbeDispatcher routes local reads through one bounded
// executor and remote reads through the shared per-target peer owner.
type batchingRecoveryProbeDispatcher struct {
	local        ch.NodeID
	ownerContext context.Context
	localTimeout time.Duration
	store        ReplicaStore
	peers        *peerBatcher
	executor     peerExecutor
}

func (d *batchingRecoveryProbeDispatcher) submitRecoveryProbe(ctx context.Context, query recoveryProbeQuery, complete func(ProbeResult, error)) error {
	if d == nil || ctx == nil || d.local == 0 || d.ownerContext == nil || d.localTimeout <= 0 || d.store == nil || d.executor == nil ||
		complete == nil || query.ChannelKey == "" || query.ChannelID.ID == "" || query.Leader != d.local || query.Voter == 0 ||
		len(query.Indexes) > maxRecoveryProbeIndexes || !validProbeIndexes(query.Indexes) {
		return ch.ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := d.ownerContext.Err(); err != nil {
		return ch.ErrClosed
	}
	query.Indexes = append([]uint64(nil), query.Indexes...)
	if query.Voter != d.local {
		if d.peers == nil {
			return ch.ErrInvalidConfig
		}
		request := ProbeRequest{
			ChannelKey: query.ChannelKey, ChannelID: query.ChannelID,
			Leader: query.Leader, Follower: query.Voter, Indexes: query.Indexes,
		}
		return d.peers.submitProbe(ctx, query.Voter, request, complete)
	}
	return d.executor.Submit(func() {
		d.runLocalRecoveryProbe(query, complete)
	})
}

func (d *batchingRecoveryProbeDispatcher) submitRecoveryFetch(ctx context.Context, query recoveryFetchQuery, complete func(FetchResult, error)) error {
	if d == nil || ctx == nil || d.local == 0 || d.ownerContext == nil || d.localTimeout <= 0 || d.store == nil || d.executor == nil ||
		complete == nil || query.ChannelKey == "" || query.ChannelID.ID == "" || query.Leader != d.local || query.Donor == 0 ||
		!validReplicaState(query.Expected) || query.From == 0 || query.Through < query.From ||
		query.Through-query.From >= maxRecoveryProbeIndexes || query.Through > query.Expected.LEO || query.MaxBytes <= 0 ||
		(query.From == 1 && query.Previous != (ch.EntryIdentity{})) ||
		(query.From > 1 && (!validEntryIdentity(query.Previous) || query.Previous.Index != query.From-1)) {
		return ch.ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := d.ownerContext.Err(); err != nil {
		return ch.ErrClosed
	}
	request := FetchRequest{
		ChannelKey: query.ChannelKey, ChannelID: query.ChannelID,
		Leader: query.Leader, Follower: query.Donor, Expected: query.Expected,
		From: query.From, Through: query.Through, Previous: query.Previous, MaxBytes: query.MaxBytes,
	}
	if query.Donor != d.local {
		if d.peers == nil {
			return ch.ErrInvalidConfig
		}
		return d.peers.submitFetch(ctx, query.Donor, request, complete)
	}
	return d.executor.Submit(func() {
		d.runLocalRecoveryFetch(request, complete)
	})
}

func (d *batchingRecoveryProbeDispatcher) runLocalRecoveryProbe(query recoveryProbeQuery, complete func(ProbeResult, error)) {
	result, err := d.loadLocalRecoveryProbe(query)
	complete(result, err)
}

func (d *batchingRecoveryProbeDispatcher) loadLocalRecoveryProbe(query recoveryProbeQuery) (result ProbeResult, err error) {
	defer func() {
		if recover() != nil {
			result = ProbeResult{}
			err = errPeerExchangePanic
		}
	}()
	loadContext, cancel := context.WithTimeout(d.ownerContext, d.localTimeout)
	defer cancel()
	loaded, err := d.store.Load(loadContext, LoadBatch{Items: []LoadRequest{{
		ChannelKey: query.ChannelKey, ChannelID: query.ChannelID,
		ProbeIndexes: append([]uint64(nil), query.Indexes...),
	}}})
	if err != nil || len(loaded.Items) != 1 {
		if err == nil {
			err = errInvalidExchangeResult
		}
		return ProbeResult{}, err
	}
	request := ProbeRequest{
		ChannelKey: query.ChannelKey, ChannelID: query.ChannelID,
		Leader: query.Leader, Follower: query.Voter, Indexes: query.Indexes,
	}
	result, ok := mapProbeResult(request, loaded.Items[0])
	if !ok {
		return ProbeResult{}, errInvalidExchangeResult
	}
	return result, nil
}

func (d *batchingRecoveryProbeDispatcher) runLocalRecoveryFetch(request FetchRequest, complete func(FetchResult, error)) {
	result, err := d.loadLocalRecoveryFetch(request)
	complete(result, err)
}

func (d *batchingRecoveryProbeDispatcher) loadLocalRecoveryFetch(request FetchRequest) (result FetchResult, err error) {
	defer func() {
		if recover() != nil {
			result = FetchResult{}
			err = errPeerExchangePanic
		}
	}()
	fetchContext, cancel := context.WithTimeout(d.ownerContext, d.localTimeout)
	defer cancel()
	fetched := d.store.Fetch(fetchContext, []FetchRange{{
		ChannelKey: request.ChannelKey, ChannelID: request.ChannelID, Expected: request.Expected,
		From: request.From, Through: request.Through, Previous: request.Previous, MaxBytes: request.MaxBytes,
	}})
	if len(fetched) != 1 {
		return FetchResult{}, errInvalidExchangeResult
	}
	if fetched[0].Err != nil {
		return FetchResult{}, fetched[0].Err
	}
	mapped, ok := mapFetchResult(request, fetched[0])
	if !ok {
		return FetchResult{}, errInvalidExchangeResult
	}
	return mapped, nil
}

// recoverQuorumPrefix proves the greatest quorum-identical prefix using one
// frontier round followed by bounded identity pages. Every voter retained in
// the final proof must report one unchanged frontier across all pages.
func recoverQuorumPrefix(ctx context.Context, request recoveryProbeRequest, dispatcher recoveryProbeDispatcher) (recoverySelection, error) {
	if ctx == nil || dispatcher == nil || request.ChannelKey == "" || request.ChannelID.ID == "" || request.Leader == 0 || request.Timeout <= 0 {
		return recoverySelection{}, ch.ErrInvalidConfig
	}
	configured, err := validateRecoveryTopology(request.Voters, request.Quorum)
	if err != nil {
		return recoverySelection{}, err
	}
	if _, leaderIsVoter := configured[request.Leader]; !leaderIsVoter {
		return recoverySelection{}, ch.ErrInvalidConfig
	}
	if request.Continuation != nil && !validRecoveryContinuationShape(request, configured) {
		return recoverySelection{}, ch.ErrInvalidConfig
	}
	operationContext, cancel := context.WithTimeout(ctx, request.Timeout)
	defer cancel()

	frontierReports, err := collectRecoveryProbeRound(operationContext, request, nil, dispatcher)
	if err != nil {
		return recoverySelection{}, err
	}
	if len(frontierReports) < request.Quorum {
		return recoverySelection{}, errRecoveryQuorumUnavailable
	}
	committed := make([]uint64, 0, len(frontierReports))
	leos := make([]uint64, 0, len(frontierReports))
	for _, report := range frontierReports {
		committed = append(committed, report.Result.State.Committed)
		leos = append(leos, report.Result.State.LEO)
	}
	certifiedCommitted := quorumFrontier(committed, request.Quorum)
	quorumLEO := quorumFrontier(leos, request.Quorum)
	if certifiedCommitted > quorumLEO {
		return recoverySelection{}, ch.ErrLogConflict
	}
	if quorumLEO == 0 {
		return recoverySelection{CertifiedCommitted: certifiedCommitted}, nil
	}
	selected := recoverySelection{CertifiedCommitted: certifiedCommitted}
	firstIndex := certifiedCommitted
	if firstIndex == 0 {
		firstIndex = 1
	}
	stable := make(map[ch.NodeID]ReplicaState, len(frontierReports))
	if request.Continuation == nil {
		for _, report := range frontierReports {
			stable[report.Voter] = report.Result.State
		}
	} else {
		continuation := request.Continuation
		if continuation.CertifiedCommitted != certifiedCommitted || continuation.QuorumLEO != quorumLEO {
			return recoverySelection{}, errRecoveryProbeIncomplete
		}
		selected.Index = continuation.SelectedIndex
		selected.Identity = continuation.SelectedIdentity
		selected.CertifiedIdentity = continuation.CertifiedIdentity
		selected.Continuation = cloneRecoveryContinuation(continuation)
		firstIndex = continuation.NextIndex
		current := make(map[ch.NodeID]ReplicaState, len(frontierReports))
		for _, report := range frontierReports {
			current[report.Voter] = report.Result.State
		}
		for _, voter := range continuation.Stable {
			state, ok := current[voter.Voter]
			if !ok || state != voter.State {
				continue
			}
			stable[voter.Voter] = voter.State
		}
		if len(stable) < request.Quorum {
			return selected, errRecoveryProbeIncomplete
		}
		selected.Supporters = recoverySupportersFromStable(request.Voters, stable)
	}
	for pageStart := firstIndex; ; {
		pageSize := quorumLEO - pageStart + 1
		if pageSize > maxRecoveryProbeIndexes {
			pageSize = maxRecoveryProbeIndexes
		}
		indexes := ascendingRecoveryIndexes(pageStart, int(pageSize))
		pageRequest := request
		pageRequest.Voters = stableRecoveryVoters(request.Voters, stable)
		pageRequest.Continuation = nil
		pageReports, roundErr := collectRecoveryProbeRound(operationContext, pageRequest, indexes, dispatcher)
		if roundErr != nil {
			return selected, roundErr
		}
		pageByVoter := make(map[ch.NodeID]recoveryProbeReport, len(pageReports))
		for _, report := range pageReports {
			pageByVoter[report.Voter] = report
		}
		for voter, frontier := range stable {
			page, ok := pageByVoter[voter]
			if !ok || page.Result.State != frontier {
				delete(stable, voter)
			}
		}
		if len(stable) < request.Quorum {
			return selected, errRecoveryProbeIncomplete
		}
		stableReports := make([]recoveryProbeReport, 0, len(stable))
		stableCommitted := make([]uint64, 0, len(stable))
		stableLEOs := make([]uint64, 0, len(stable))
		for _, voter := range request.Voters {
			if _, ok := stable[voter]; !ok {
				continue
			}
			report := pageByVoter[voter]
			stableReports = append(stableReports, report)
			stableCommitted = append(stableCommitted, report.Result.State.Committed)
			stableLEOs = append(stableLEOs, report.Result.State.LEO)
		}
		if quorumFrontier(stableCommitted, request.Quorum) != certifiedCommitted || quorumFrontier(stableLEOs, request.Quorum) != quorumLEO {
			return selected, errRecoveryProbeIncomplete
		}
		position := 0
		if pageStart == certifiedCommitted && certifiedCommitted > 0 {
			identity, ok := quorumCommittedIdentityAt(stableReports, 0, certifiedCommitted, request.Quorum)
			if !ok {
				return recoverySelection{}, ch.ErrLogConflict
			}
			selected.Index = certifiedCommitted
			selected.Identity = identity
			selected.CertifiedIdentity = identity
			selected.Supporters = recoverySupportersFor(request.Voters, stableReports, 0, identity)
			position = 1
		}
		for ; position < len(indexes); position++ {
			index := indexes[position]
			identity, ok := quorumIdentityAt(stableReports, position, index, request.Quorum)
			if !ok {
				selected.Continuation = nil
				return selected, nil
			}
			if selected.Index == 0 {
				if index != 1 || identity.PreviousIndex != 0 || identity.PreviousTerm != 0 || identity.PreviousDigest != (ch.EntryDigest{}) {
					return recoverySelection{}, ch.ErrLogConflict
				}
			} else if identity.PreviousIndex != selected.Identity.Index || identity.PreviousTerm != selected.Identity.LeaderTerm ||
				identity.PreviousDigest != selected.Identity.Digest {
				return recoverySelection{}, ch.ErrLogConflict
			}
			selected.Index = index
			selected.Identity = identity
			selected.Supporters = recoverySupportersFor(request.Voters, stableReports, position, identity)
		}
		pageEnd := indexes[len(indexes)-1]
		if pageEnd == quorumLEO {
			selected.Continuation = nil
			return selected, nil
		}
		stable = recoveryIdentitySupporters(stableReports, len(indexes)-1, selected.Identity)
		if len(stable) < request.Quorum {
			return selected, errRecoveryProbeIncomplete
		}
		pageStart = pageEnd + 1
		selected.Continuation = makeRecoveryContinuation(request, certifiedCommitted, quorumLEO, pageStart, selected, stable)
	}
}

func validRecoveryContinuationShape(request recoveryProbeRequest, configured map[ch.NodeID]struct{}) bool {
	continuation := request.Continuation
	if continuation == nil || continuation.ChannelKey != request.ChannelKey || continuation.ChannelID != request.ChannelID ||
		continuation.Leader != request.Leader || continuation.Quorum != request.Quorum ||
		len(continuation.Voters) != len(request.Voters) || len(continuation.Stable) < request.Quorum ||
		len(continuation.Stable) > len(request.Voters) || continuation.CertifiedCommitted > continuation.QuorumLEO ||
		continuation.NextIndex == 0 || continuation.NextIndex > continuation.QuorumLEO {
		return false
	}
	if continuation.CertifiedCommitted == 0 {
		if continuation.CertifiedIdentity != (ch.EntryIdentity{}) {
			return false
		}
	} else if !validEntryIdentity(continuation.CertifiedIdentity) ||
		continuation.CertifiedIdentity.Index != continuation.CertifiedCommitted {
		return false
	}
	for index := range request.Voters {
		if continuation.Voters[index] != request.Voters[index] {
			return false
		}
	}
	firstIndex := continuation.CertifiedCommitted
	if firstIndex == 0 {
		firstIndex = 1
	}
	if continuation.SelectedIndex == 0 {
		if continuation.SelectedIdentity != (ch.EntryIdentity{}) || continuation.NextIndex != firstIndex {
			return false
		}
	} else if continuation.SelectedIdentity.Index != continuation.SelectedIndex ||
		continuation.SelectedIndex == ^uint64(0) || continuation.NextIndex != continuation.SelectedIndex+1 ||
		continuation.SelectedIndex < continuation.CertifiedCommitted || continuation.SelectedIndex > continuation.QuorumLEO ||
		!validEntryIdentity(continuation.SelectedIdentity) {
		return false
	}
	seen := make(map[ch.NodeID]struct{}, len(continuation.Stable))
	for _, voter := range continuation.Stable {
		if _, ok := configured[voter.Voter]; !ok || !validReplicaState(voter.State) {
			return false
		}
		if _, duplicate := seen[voter.Voter]; duplicate {
			return false
		}
		seen[voter.Voter] = struct{}{}
	}
	return true
}

func makeRecoveryContinuation(
	request recoveryProbeRequest,
	certifiedCommitted uint64,
	quorumLEO uint64,
	nextIndex uint64,
	selected recoverySelection,
	stable map[ch.NodeID]ReplicaState,
) *recoveryContinuation {
	continuation := &recoveryContinuation{
		ChannelKey: request.ChannelKey, ChannelID: request.ChannelID,
		Leader: request.Leader, Voters: append([]ch.NodeID(nil), request.Voters...), Quorum: request.Quorum,
		CertifiedCommitted: certifiedCommitted, QuorumLEO: quorumLEO, NextIndex: nextIndex,
		CertifiedIdentity: selected.CertifiedIdentity,
		SelectedIndex:     selected.Index, SelectedIdentity: selected.Identity,
		Stable: make([]recoveryContinuationVoter, 0, len(stable)),
	}
	for _, voter := range request.Voters {
		if state, ok := stable[voter]; ok {
			continuation.Stable = append(continuation.Stable, recoveryContinuationVoter{Voter: voter, State: state})
		}
	}
	return continuation
}

func recoverySupportersFromStable(voters []ch.NodeID, stable map[ch.NodeID]ReplicaState) []recoverySupporter {
	supporters := make([]recoverySupporter, 0, len(stable))
	for _, voter := range voters {
		if state, ok := stable[voter]; ok {
			supporters = append(supporters, recoverySupporter{Voter: voter, State: state})
		}
	}
	return supporters
}

func cloneRecoveryContinuation(source *recoveryContinuation) *recoveryContinuation {
	if source == nil {
		return nil
	}
	cloned := *source
	cloned.Voters = append([]ch.NodeID(nil), source.Voters...)
	cloned.Stable = append([]recoveryContinuationVoter(nil), source.Stable...)
	return &cloned
}

func stableRecoveryVoters(configured []ch.NodeID, stable map[ch.NodeID]ReplicaState) []ch.NodeID {
	voters := make([]ch.NodeID, 0, len(stable))
	for _, voter := range configured {
		if _, ok := stable[voter]; ok {
			voters = append(voters, voter)
		}
	}
	return voters
}

func recoveryIdentitySupporters(reports []recoveryProbeReport, position int, identity ch.EntryIdentity) map[ch.NodeID]ReplicaState {
	supporters := make(map[ch.NodeID]ReplicaState, len(reports))
	for _, report := range reports {
		if position < 0 || position >= len(report.Result.Entries) {
			continue
		}
		entry := report.Result.Entries[position]
		if entry.Present && entry.Identity == identity {
			supporters[report.Voter] = report.Result.State
		}
	}
	return supporters
}

func collectRecoveryProbeRound(ctx context.Context, request recoveryProbeRequest, indexes []uint64, dispatcher recoveryProbeDispatcher) ([]recoveryProbeReport, error) {
	completions := make(chan recoveryProbeCompletion, len(request.Voters))
	for _, voter := range request.Voters {
		voter := voter
		query := recoveryProbeQuery{
			ChannelKey: request.ChannelKey, ChannelID: request.ChannelID,
			Leader: request.Leader, Voter: voter, Indexes: append([]uint64(nil), indexes...),
		}
		complete := func(result ProbeResult, err error) {
			completions <- recoveryProbeCompletion{voter: voter, result: result, err: err}
		}
		if err := dispatcher.submitRecoveryProbe(ctx, query, complete); err != nil {
			completions <- recoveryProbeCompletion{voter: voter, err: err}
		}
	}

	reports := make([]recoveryProbeReport, 0, len(request.Voters))
	for pending := len(request.Voters); pending > 0; pending-- {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case completion := <-completions:
			if completion.err != nil {
				continue
			}
			if !validRecoveryProbeResult(completion.result) || !sameRecoveryProbeIndexes(indexes, completion.result.Entries) {
				return nil, ch.ErrLogConflict
			}
			reports = append(reports, recoveryProbeReport{Voter: completion.voter, Result: completion.result})
		}
	}
	return reports, nil
}

func ascendingRecoveryIndexes(first uint64, count int) []uint64 {
	indexes := make([]uint64, count)
	for index := range indexes {
		indexes[index] = first + uint64(index)
	}
	return indexes
}
