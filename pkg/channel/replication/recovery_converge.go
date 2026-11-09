package replication

import (
	"context"
	"slices"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// convergeRecoverySuffix copies one bounded page of immutable proposals only
// when a read quorum proves that every observed tail belongs to the donor's
// chain. It never truncates a replica or promotes an unproved commit frontier.
// A new quorum probe, followed by the authority barrier, still gates readiness.
func (l *quorumLog) convergeRecoverySuffix(ctx context.Context, authority Authority, selected recoverySelection, promise AuthorityID) error {
	ctx, cancel := context.WithTimeout(ctx, l.cfg.RecoveryTimeout)
	defer cancel()
	request := recoveryProbeRequest{ChannelKey: authority.Key, ChannelID: authority.ChannelID,
		Leader: authority.Leader, Voters: authority.Voters, Quorum: authority.WriteQuorum, Timeout: l.cfg.RecoveryTimeout,
		Promise: promise}
	reports, err := collectRecoveryProbeRound(ctx, request, nil, l.cfg.Recovery)
	if err != nil {
		return err
	}
	if len(reports) < authority.WriteQuorum {
		return ch.ErrNotReady
	}
	donor := reports[0]
	indexes := make([]uint64, 0, len(reports))
	first := donor.Result.State.LEO
	for _, report := range reports {
		state := report.Result.State
		if state.LEO > 0 {
			tail := state.TailIdentity
			if compareAuthorityID(AuthorityID{ChannelEpoch: tail.ChannelEpoch, LeaderTerm: tail.LeaderTerm, FenceVersion: tail.FenceVersion}, authority.ID) > 0 {
				return ch.ErrStaleMeta
			}
		}
		if state.LEO > donor.Result.State.LEO {
			donor = report
		}
		first = min(first, state.LEO)
		if state.LEO > 0 {
			indexes = append(indexes, state.LEO)
		}
	}
	if donor.Result.State.LEO <= selected.Index || first == donor.Result.State.LEO {
		return nil
	}
	slices.Sort(indexes)
	indexes = slices.Compact(indexes)
	// Recheck every responding voter, including any longer minority suffix. A
	// conflicting or changing tail is insufficient evidence to choose a branch.
	stableVoters := make([]ch.NodeID, len(reports))
	for i, report := range reports {
		stableVoters[i] = report.Voter
	}
	request.Voters = stableVoters
	// Every previously observed tail participates in this proof. A fast quorum
	// must not turn a still-pending observed voter into an incomplete recheck.
	request.Quorum = len(stableVoters)
	proved, err := collectRecoveryProbeRound(ctx, request, indexes, l.cfg.Recovery)
	if err != nil {
		return err
	}
	if len(proved) != len(reports) {
		return ch.ErrNotReady
	}
	var donorProof ProbeResult
	for _, report := range proved {
		if report.Voter == donor.Voter {
			donorProof = report.Result
		}
		i := slices.IndexFunc(reports, func(old recoveryProbeReport) bool { return old.Voter == report.Voter })
		if i < 0 || reports[i].Result.State != report.Result.State {
			return ch.ErrNotReady
		}
	}
	for _, report := range reports {
		state := report.Result.State
		if state.LEO == 0 {
			continue
		}
		i, ok := slices.BinarySearch(indexes, state.LEO)
		if !ok || i >= len(donorProof.Entries) || !donorProof.Entries[i].Present || donorProof.Entries[i].Identity != state.TailIdentity {
			return ch.ErrNotReady
		}
	}
	previous := ch.EntryIdentity{}
	if first > 0 {
		i, _ := slices.BinarySearch(indexes, first)
		previous = donorProof.Entries[i].Identity
	}
	through := donor.Result.State.LEO
	if through-first > maxRecoveryProbeIndexes {
		through = first + maxRecoveryProbeIndexes
	}
	page, err := fetchRecoveryPage(ctx, recoveryRepairRequest{
		ChannelKey: authority.Key, ChannelID: authority.ChannelID, Leader: authority.Leader,
		MaxPageBytes: l.cfg.RecoveryPageBytes,
		Selection:    recoverySelection{Supporters: []recoverySupporter{{Voter: donor.Voter, State: donor.Result.State}}},
	}, first+1, through, previous, l.cfg.Recovery)
	if err != nil {
		return err
	}
	for _, report := range reports {
		for _, item := range page.Proposals {
			if report.Result.State.LEO >= item.Manifest.LastOffset {
				continue
			}
			proposal := durableProposal{first: item.Manifest.BaseOffset + 1, last: item.Manifest.LastOffset,
				channelKey: authority.Key, channelID: authority.ChannelID, leader: authority.Leader,
				manifest: item.Manifest, records: item.Records, committed: min(selected.Index, item.Manifest.LastOffset),
				writer: promise}
			completed := make(chan durabilityCompletion, 1)
			finish := func(result durabilityCompletion) { completed <- result }
			if report.Voter == authority.Leader {
				err = l.cfg.Durability.submitLocal(ctx, proposal, finish)
			} else {
				err = l.cfg.Durability.submitReplica(ctx, report.Voter, proposal, finish)
			}
			if err != nil {
				return err
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case result := <-completed:
				if result.err != nil {
					return result.err
				}
				if !result.outcome.Durable() {
					return ch.ErrNotReady
				}
			}
		}
	}
	return nil
}
