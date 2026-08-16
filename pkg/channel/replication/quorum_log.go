package replication

import (
	"context"
	"reflect"
	"sync"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

type recoveryDispatcher interface {
	recoveryProbeDispatcher
	recoveryFetchDispatcher
}

// quorumLogConfig bounds every owner-side collection before a Channel can be
// installed. The implementation starts no goroutine or timer per Channel.
type quorumLogConfig struct {
	// Local is the only node allowed to install leader authority through this owner.
	Local ch.NodeID
	// Store, Recovery, and Durability are bounded local/peer adapters.
	Store      ReplicaStore
	Recovery   recoveryDispatcher
	Durability durabilityDispatcher
	// RecoveryTimeout and RecoveryPageBytes bound one proof and repair attempt.
	RecoveryTimeout   time.Duration
	RecoveryPageBytes int
	// MaxChannels bounds resident sequencers without per-Channel goroutines.
	MaxChannels int
	// Proposal and retained-command limits bound hot-path owned memory.
	MaxProposalRecords  int
	MaxProposalBytes    int
	MaxRetainedCommands int
}

// quorumLog owns authority fencing, one sequencer per resident Channel, and
// the complete local-plus-quorum durability protocol.
type quorumLog struct {
	cfg      quorumLogConfig
	commands commandStore

	mu       sync.Mutex
	channels map[ch.ChannelKey]*quorumChannel
}

// quorumChannel serializes one authority generation. pending is immutable
// until a definite conflict or exact durability proof resolves it.
type quorumChannel struct {
	mu sync.Mutex

	id        ch.ChannelID
	authority Authority
	frontier  ReplicaState
	hw        uint64
	ready     bool
	pending   *retainedProposal
	retained  map[ch.CommandID]retainedProposal
	order     []ch.CommandID
}

type retainedProposal struct {
	proposal durableProposal
	receipt  Receipt
	durable  bool
}

func newQuorumLog(cfg quorumLogConfig) (*quorumLog, error) {
	if cfg.Local == 0 || cfg.Store == nil || cfg.Recovery == nil || cfg.Durability == nil ||
		cfg.RecoveryTimeout <= 0 || cfg.RecoveryPageBytes <= 0 || cfg.MaxChannels <= 0 ||
		cfg.MaxProposalRecords <= 0 || cfg.MaxProposalBytes <= 0 || cfg.MaxRetainedCommands <= 0 {
		return nil, ch.ErrInvalidConfig
	}
	commands, ok := cfg.Store.(commandStore)
	if !ok {
		return nil, ch.ErrInvalidConfig
	}
	return &quorumLog{cfg: cfg, commands: commands, channels: make(map[ch.ChannelKey]*quorumChannel, cfg.MaxChannels)}, nil
}

func (l *quorumLog) Install(ctx context.Context, authority Authority) (Installed, error) {
	if l == nil || ctx == nil || !validAuthority(authority) || authority.Leader != l.cfg.Local {
		return Installed{}, ch.ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return Installed{}, err
	}
	state, err := l.channel(authority.Key, authority.ChannelID)
	if err != nil {
		return Installed{}, err
	}
	state.mu.Lock()
	defer state.mu.Unlock()

	if state.authority.ID != (AuthorityID{}) {
		switch compareAuthorityID(authority.ID, state.authority.ID) {
		case -1:
			return Installed{}, ch.ErrStaleMeta
		case 0:
			if !sameAuthority(authority, state.authority) {
				return Installed{}, ch.ErrLogConflict
			}
			if authority.WriteFence.Set() {
				return Installed{}, ch.ErrWriteFenced
			}
			if state.ready {
				return Installed{Authority: state.authority.ID, LEO: state.frontier.LEO, HW: state.hw}, nil
			}
		case 1:
			fenceQuorumChannel(state, authority, l.cfg.MaxRetainedCommands)
		}
	} else {
		fenceQuorumChannel(state, authority, l.cfg.MaxRetainedCommands)
	}
	if authority.WriteFence.Set() {
		return Installed{}, ch.ErrWriteFenced
	}

	selection, err := recoverQuorumPrefix(ctx, recoveryProbeRequest{
		ChannelKey: authority.Key, ChannelID: authority.ChannelID, Leader: authority.Leader,
		Voters: authority.Voters, Quorum: authority.WriteQuorum, Timeout: l.cfg.RecoveryTimeout,
	}, l.cfg.Recovery)
	if err != nil {
		return Installed{}, err
	}
	recovered, err := repairQuorumPrefix(ctx, recoveryRepairRequest{
		ChannelKey: authority.Key, ChannelID: authority.ChannelID, Leader: authority.Leader, Local: l.cfg.Local,
		Voters: authority.Voters, Quorum: authority.WriteQuorum, Selection: selection,
		Timeout: l.cfg.RecoveryTimeout, MaxPageBytes: l.cfg.RecoveryPageBytes,
	}, l.cfg.Recovery, l.cfg.Store)
	if err != nil {
		return Installed{}, err
	}
	installedFrontier := recovered
	// A quorum-proved empty log has no durable effect that needs a standalone
	// authority fence. Its first business proposal carries the complete current
	// authority and makes that proof durable in the same quorum round. A
	// non-empty frontier still needs the barrier before it can accept a proposal
	// under a different authority.
	if recovered != (ReplicaState{}) && !frontierUsesAuthority(recovered, authority.ID) {
		barrier, barrierErr := writeCurrentTermBarrier(ctx, authority, recovered, l.cfg.Durability)
		if barrierErr != nil {
			return Installed{}, barrierErr
		}
		installedFrontier = barrier.State
	}

	state.frontier = installedFrontier
	state.hw = installedFrontier.LEO
	state.ready = true
	state.pending = nil
	state.retained = make(map[ch.CommandID]retainedProposal, l.cfg.MaxRetainedCommands)
	state.order = state.order[:0]
	return Installed{Authority: authority.ID, LEO: state.frontier.LEO, HW: state.hw}, nil
}

func fenceQuorumChannel(state *quorumChannel, authority Authority, retainedCapacity int) {
	state.authority = cloneAuthority(authority)
	state.frontier = ReplicaState{}
	state.hw = 0
	state.ready = false
	state.pending = nil
	state.retained = make(map[ch.CommandID]retainedProposal, retainedCapacity)
	state.order = state.order[:0]
}

func (l *quorumLog) Commit(ctx context.Context, proposal Proposal) (Receipt, error) {
	if l == nil || ctx == nil || proposal.Key == "" || proposal.Expected == (AuthorityID{}) ||
		proposal.CommandID == (ch.CommandID{}) || len(proposal.Records) == 0 ||
		len(proposal.Records) > l.cfg.MaxProposalRecords || !validProposalRecords(proposal.Records, l.cfg.MaxProposalBytes) {
		return Receipt{}, ch.ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return Receipt{}, err
	}
	state := l.existingChannel(proposal.Key)
	if state == nil {
		return Receipt{}, ch.ErrNotReady
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.ready {
		return Receipt{}, ch.ErrNotReady
	}
	if proposal.Expected != state.authority.ID {
		return Receipt{}, ch.ErrStaleMeta
	}
	if state.authority.WriteFence.Set() {
		return Receipt{}, ch.ErrWriteFenced
	}

	if retained, ok := state.retained[proposal.CommandID]; ok {
		if !sameProposalContent(retained.proposal, proposal.Records) {
			return Receipt{}, ch.ErrLogConflict
		}
		if retained.durable {
			return retained.receipt, nil
		}
		return l.retryPending(ctx, state, retained)
	}
	if state.pending != nil && state.pending.proposal.manifest.CommandID == proposal.CommandID {
		if !sameProposalContent(state.pending.proposal, proposal.Records) {
			return Receipt{}, ch.ErrLogConflict
		}
		return l.retryPending(ctx, state, *state.pending)
	}
	if state.pending != nil {
		return Receipt{}, ch.ErrBackpressured
	}

	durable, err := sealBusinessProposal(state.authority, state.frontier, state.hw, proposal.CommandID, proposal.Records)
	if err != nil {
		return Receipt{}, err
	}
	pending := retainedProposal{proposal: durable}
	state.pending = &pending
	result, err := runDurableRound(ctx, l.cfg.Local, state.authority.Voters, state.authority.WriteQuorum, durable, l.cfg.Durability)
	if err != nil {
		if result.outcome == ch.AppendOutcomeConflict {
			state.pending = nil
			return l.reconcileCommandConflict(ctx, state, proposal)
		}
		return Receipt{}, err
	}
	return l.finishCommit(state, pending, result)
}

func (l *quorumLog) reconcileCommandConflict(ctx context.Context, state *quorumChannel, proposal Proposal) (Receipt, error) {
	loaded, found, err := l.loadRetainedProposal(ctx, state, proposal.CommandID)
	if err != nil {
		return Receipt{}, err
	}
	if !found || !sameProposalContent(loaded.proposal, proposal.Records) {
		return Receipt{}, ch.ErrLogConflict
	}
	l.remember(state, loaded)
	return loaded.receipt, nil
}

func (l *quorumLog) loadRetainedProposal(ctx context.Context, state *quorumChannel, command ch.CommandID) (retainedProposal, bool, error) {
	results := l.commands.LookupCommands(ctx, []CommandLookup{{
		ChannelKey: state.authority.Key, ChannelID: state.authority.ChannelID, CommandID: command,
		MaxRecords: l.cfg.MaxProposalRecords, MaxBytes: l.cfg.MaxProposalBytes,
	}})
	if len(results) != 1 {
		return retainedProposal{}, false, ch.ErrLogConflict
	}
	result := results[0]
	if result.Err != nil {
		return retainedProposal{}, false, result.Err
	}
	if !result.Found {
		return retainedProposal{}, false, nil
	}
	manifest := result.Manifest
	if !manifest.StructurallyValid() || manifest.CommandID != command || manifest.LastOffset > state.hw ||
		manifest.ChannelEpoch != state.authority.ID.ChannelEpoch || manifest.LeaderTerm != state.authority.ID.LeaderTerm ||
		manifest.FenceVersion != state.authority.ID.FenceVersion {
		return retainedProposal{}, false, ch.ErrLogConflict
	}
	sealed, entries, ok := ch.SealProposalManifest(manifest, result.Records)
	if !ok || sealed != manifest || len(entries) == 0 {
		return retainedProposal{}, false, ch.ErrLogConflict
	}
	receipt := Receipt{
		Authority: state.authority.ID, CommandID: command,
		First: manifest.BaseOffset + 1, Last: manifest.LastOffset, HW: manifest.LastOffset,
	}
	return retainedProposal{proposal: durableProposal{
		first: receipt.First, last: receipt.Last,
		channelKey: state.authority.Key, channelID: state.authority.ChannelID, leader: state.authority.Leader,
		manifest: manifest, records: cloneRecords(result.Records), committed: manifest.BaseOffset,
	}, receipt: receipt, durable: true}, true, nil
}

func (l *quorumLog) retryPending(ctx context.Context, state *quorumChannel, retained retainedProposal) (Receipt, error) {
	result, err := runDurableRound(ctx, l.cfg.Local, state.authority.Voters, state.authority.WriteQuorum, retained.proposal, l.cfg.Durability)
	if err != nil {
		return Receipt{}, err
	}
	return l.finishCommit(state, retained, result)
}

func (l *quorumLog) finishCommit(state *quorumChannel, retained retainedProposal, result durableRoundResult) (Receipt, error) {
	if !result.localDurable || result.durableVotes < state.authority.WriteQuorum || !result.outcome.Durable() {
		return Receipt{}, errDurableQuorumUnavailable
	}
	proposal := retained.proposal
	_, entries, ok := ch.SealProposalManifest(proposal.manifest, proposal.records)
	if !ok || len(entries) == 0 {
		return Receipt{}, ch.ErrLogConflict
	}
	receipt := Receipt{
		Authority: state.authority.ID, CommandID: proposal.manifest.CommandID,
		First: proposal.first, Last: proposal.last, HW: proposal.last,
	}
	state.frontier = ReplicaState{
		LEO: proposal.last, Committed: proposal.committed,
		Manifest: proposal.manifest, TailIdentity: entries[len(entries)-1],
	}
	state.hw = proposal.last
	state.pending = nil
	retained.receipt = receipt
	retained.durable = true
	l.remember(state, retained)
	return receipt, nil
}

func (l *quorumLog) remember(state *quorumChannel, retained retainedProposal) {
	command := retained.proposal.manifest.CommandID
	if _, exists := state.retained[command]; exists {
		state.retained[command] = retained
		return
	}
	if len(state.order) == l.cfg.MaxRetainedCommands {
		delete(state.retained, state.order[0])
		copy(state.order, state.order[1:])
		state.order = state.order[:len(state.order)-1]
	}
	state.retained[command] = retained
	state.order = append(state.order, command)
}

func (l *quorumLog) channel(key ch.ChannelKey, id ch.ChannelID) (*quorumChannel, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if state := l.channels[key]; state != nil {
		if state.id != id {
			return nil, ch.ErrLogConflict
		}
		return state, nil
	}
	if len(l.channels) >= l.cfg.MaxChannels {
		return nil, ch.ErrTooManyChannels
	}
	state := &quorumChannel{id: id}
	l.channels[key] = state
	return state, nil
}

func (l *quorumLog) existingChannel(key ch.ChannelKey) *quorumChannel {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.channels[key]
}

func sealBusinessProposal(authority Authority, frontier ReplicaState, hw uint64, command ch.CommandID, records []ch.Record) (durableProposal, error) {
	if frontier.LEO == ^uint64(0) || uint64(len(records)) > ^uint64(0)-frontier.LEO {
		return durableProposal{}, ch.ErrInvalidConfig
	}
	frozen := cloneRecords(records)
	manifest, entries, ok := ch.SealProposalManifest(ch.ProposalManifest{
		Version:      ch.ProposalManifestVersion,
		ChannelEpoch: authority.ID.ChannelEpoch, LeaderTerm: authority.ID.LeaderTerm, FenceVersion: authority.ID.FenceVersion,
		CommandID: command, BaseOffset: frontier.LEO, LastOffset: frontier.LEO + uint64(len(frozen)),
		PreviousTerm: frontier.TailIdentity.LeaderTerm, PreviousIndex: frontier.LEO, PreviousDigest: frontier.TailIdentity.Digest,
	}, frozen)
	if !ok || len(entries) != len(frozen) {
		return durableProposal{}, ch.ErrInvalidConfig
	}
	return durableProposal{
		first: frontier.LEO + 1, last: manifest.LastOffset,
		channelKey: authority.Key, channelID: authority.ChannelID, leader: authority.Leader,
		manifest: manifest, records: frozen, committed: hw,
	}, nil
}

func validProposalRecords(records []ch.Record, maxBytes int) bool {
	total := 0
	for _, record := range records {
		if record.ID == 0 || record.Epoch == 0 || record.ServerTimestampMS <= 0 || record.SizeBytes != len(record.Payload) {
			return false
		}
		item := 96 + len(record.FromUID) + len(record.ClientMsgNo) + len(record.Payload)
		if item > maxBytes-total {
			return false
		}
		total += item
	}
	return total <= maxBytes
}

func sameProposalContent(retained durableProposal, records []ch.Record) bool {
	if len(records) != len(retained.records) {
		return false
	}
	manifest, _, ok := ch.SealProposalManifest(retained.manifest, records)
	return ok && manifest == retained.manifest
}

func cloneRecords(records []ch.Record) []ch.Record {
	cloned := append([]ch.Record(nil), records...)
	for index := range cloned {
		cloned[index].Payload = append([]byte(nil), cloned[index].Payload...)
	}
	return cloned
}

func cloneAuthority(authority Authority) Authority {
	authority.Voters = append([]ch.NodeID(nil), authority.Voters...)
	return authority
}

func sameAuthority(left, right Authority) bool {
	return left.Key == right.Key && left.ChannelID == right.ChannelID && left.ID == right.ID && left.Leader == right.Leader &&
		left.WriteQuorum == right.WriteQuorum && left.WriteFence == right.WriteFence && reflect.DeepEqual(left.Voters, right.Voters)
}

func compareAuthorityID(left, right AuthorityID) int {
	for _, pair := range [][2]uint64{
		{left.ChannelEpoch, right.ChannelEpoch},
		{left.LeaderTerm, right.LeaderTerm},
		{left.FenceVersion, right.FenceVersion},
	} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	return 0
}

func frontierUsesAuthority(frontier ReplicaState, authority AuthorityID) bool {
	return frontier.LEO > 0 && frontier.Manifest.ChannelEpoch == authority.ChannelEpoch &&
		frontier.Manifest.LeaderTerm == authority.LeaderTerm && frontier.Manifest.FenceVersion == authority.FenceVersion
}
