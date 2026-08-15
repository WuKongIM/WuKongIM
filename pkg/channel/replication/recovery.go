package replication

import (
	"errors"
	"sort"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

var (
	errRecoveryQuorumUnavailable = errors.New("channel replication: recovery quorum unavailable")
	errRecoveryProbeIncomplete   = errors.New("channel replication: recovery probe incomplete")
)

// recoveryProbeReport binds one exact read-only probe to a configured voter.
type recoveryProbeReport struct {
	Voter  ch.NodeID
	Result ProbeResult
}

// recoverySelection is the greatest identical prefix proven by a quorum in
// one complete probe round.
type recoverySelection struct {
	Index              uint64
	Identity           ch.EntryIdentity
	CertifiedCommitted uint64
	CertifiedIdentity  ch.EntryIdentity
	Supporters         []recoverySupporter
	Continuation       *recoveryContinuation
}

// recoverySupporter binds one donor candidate to the exact immutable frontier
// that participated in the selected quorum proof.
type recoverySupporter struct {
	Voter ch.NodeID
	State ReplicaState
}

func selectRecoveryPrefix(voters []ch.NodeID, quorum int, reports []recoveryProbeReport) (recoverySelection, error) {
	configured, err := validateRecoveryTopology(voters, quorum)
	if err != nil {
		return recoverySelection{}, err
	}
	if len(reports) < quorum {
		return recoverySelection{}, errRecoveryQuorumUnavailable
	}
	seen := make(map[ch.NodeID]struct{}, len(reports))
	committed := make([]uint64, 0, len(reports))
	leos := make([]uint64, 0, len(reports))
	var indexes []uint64
	for reportIndex, report := range reports {
		if _, member := configured[report.Voter]; !member || report.Voter == 0 {
			return recoverySelection{}, ch.ErrInvalidConfig
		}
		if _, duplicate := seen[report.Voter]; duplicate {
			return recoverySelection{}, ch.ErrInvalidConfig
		}
		seen[report.Voter] = struct{}{}
		if !validRecoveryProbeResult(report.Result) {
			return recoverySelection{}, ch.ErrLogConflict
		}
		if reportIndex == 0 {
			indexes = make([]uint64, len(report.Result.Entries))
			for index, entry := range report.Result.Entries {
				indexes[index] = entry.Index
			}
		} else if !sameRecoveryProbeIndexes(indexes, report.Result.Entries) {
			return recoverySelection{}, ch.ErrInvalidConfig
		}
		committed = append(committed, report.Result.State.Committed)
		leos = append(leos, report.Result.State.LEO)
	}
	certifiedCommitted := quorumFrontier(committed, quorum)
	quorumLEO := quorumFrontier(leos, quorum)
	byIndex := make(map[uint64]int, len(indexes))
	for position, index := range indexes {
		byIndex[index] = position
	}
	var certifiedIdentity ch.EntryIdentity
	if certifiedCommitted > 0 {
		position, present := byIndex[certifiedCommitted]
		if !present {
			return recoverySelection{}, errRecoveryProbeIncomplete
		}
		var ok bool
		certifiedIdentity, ok = quorumCommittedIdentityAt(reports, position, certifiedCommitted, quorum)
		if !ok {
			return recoverySelection{}, ch.ErrLogConflict
		}
	}
	sortedIndexes := append([]uint64(nil), indexes...)
	sort.Slice(sortedIndexes, func(i, j int) bool { return sortedIndexes[i] > sortedIndexes[j] })
	for _, index := range sortedIndexes {
		identity, ok := quorumIdentityAt(reports, byIndex[index], index, quorum)
		if !ok {
			continue
		}
		if certifiedCommitted > 0 {
			if index < certifiedCommitted {
				return recoverySelection{}, ch.ErrLogConflict
			}
			if err := validateQuorumChain(reports, byIndex, certifiedCommitted, certifiedIdentity, index, quorum); err != nil {
				return recoverySelection{}, err
			}
		}
		if index != quorumLEO {
			if index == ^uint64(0) {
				return recoverySelection{}, ch.ErrLogConflict
			}
			if _, provedNext := byIndex[index+1]; !provedNext {
				return recoverySelection{}, errRecoveryProbeIncomplete
			}
		}
		return recoverySelection{
			Index: index, Identity: identity, CertifiedCommitted: certifiedCommitted, CertifiedIdentity: certifiedIdentity,
			Supporters: recoverySupportersFor(voters, reports, byIndex[index], identity),
		}, nil
	}
	if quorumLEO == 0 {
		return recoverySelection{CertifiedCommitted: certifiedCommitted}, nil
	}
	return recoverySelection{}, errRecoveryProbeIncomplete
}

func recoverySupportersFor(voters []ch.NodeID, reports []recoveryProbeReport, position int, identity ch.EntryIdentity) []recoverySupporter {
	byVoter := make(map[ch.NodeID]recoverySupporter, len(reports))
	for _, report := range reports {
		if position < 0 || position >= len(report.Result.Entries) {
			continue
		}
		entry := report.Result.Entries[position]
		if entry.Present && entry.Identity == identity {
			byVoter[report.Voter] = recoverySupporter{Voter: report.Voter, State: report.Result.State}
		}
	}
	supporters := make([]recoverySupporter, 0, len(byVoter))
	for _, voter := range voters {
		if supporter, ok := byVoter[voter]; ok {
			supporters = append(supporters, supporter)
		}
	}
	return supporters
}

func validateRecoveryTopology(voters []ch.NodeID, quorum int) (map[ch.NodeID]struct{}, error) {
	if len(voters) == 0 || len(voters) > maxRecoveryProbeVoters || quorum <= 0 || quorum > len(voters) || quorum*2 <= len(voters) {
		return nil, ch.ErrInvalidConfig
	}
	configured := make(map[ch.NodeID]struct{}, len(voters))
	for _, voter := range voters {
		if voter == 0 {
			return nil, ch.ErrInvalidConfig
		}
		if _, duplicate := configured[voter]; duplicate {
			return nil, ch.ErrInvalidConfig
		}
		configured[voter] = struct{}{}
	}
	return configured, nil
}

func validRecoveryProbeResult(result ProbeResult) bool {
	return validRecoveryProofResult(result, maxRecoveryProbeIndexes)
}

func validRecoveryProofResult(result ProbeResult, maxEntries int) bool {
	if maxEntries < 0 || !validReplicaState(result.State) || len(result.Entries) > maxEntries {
		return false
	}
	seen := make(map[uint64]struct{}, len(result.Entries))
	for _, entry := range result.Entries {
		if entry.Index == 0 {
			return false
		}
		if _, duplicate := seen[entry.Index]; duplicate {
			return false
		}
		seen[entry.Index] = struct{}{}
		if entry.Present != (entry.Identity != (ch.EntryIdentity{})) ||
			(entry.Present && entry.Identity.Index != entry.Index) ||
			(entry.Present && !validEntryIdentity(entry.Identity)) ||
			(entry.Index <= result.State.LEO && !entry.Present) ||
			(entry.Index > result.State.LEO && entry.Present) {
			return false
		}
		if entry.Index == result.State.LEO && entry.Present && entry.Identity != result.State.TailIdentity {
			return false
		}
	}
	return validProbeEntryChain(result.Entries)
}

func sameRecoveryProbeIndexes(indexes []uint64, entries []EntryProbe) bool {
	if len(indexes) != len(entries) {
		return false
	}
	for index, entry := range entries {
		if entry.Index != indexes[index] {
			return false
		}
	}
	return true
}

func quorumFrontier(values []uint64, quorum int) uint64 {
	ordered := append([]uint64(nil), values...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] > ordered[j] })
	return ordered[quorum-1]
}

func quorumIdentityAt(reports []recoveryProbeReport, position int, index uint64, quorum int) (ch.EntryIdentity, bool) {
	counts := make(map[ch.EntryIdentity]int, len(reports))
	for _, report := range reports {
		entry := report.Result.Entries[position]
		if entry.Index != index || !entry.Present {
			continue
		}
		counts[entry.Identity]++
		if counts[entry.Identity] >= quorum {
			return entry.Identity, true
		}
	}
	return ch.EntryIdentity{}, false
}

func quorumCommittedIdentityAt(reports []recoveryProbeReport, position int, index uint64, quorum int) (ch.EntryIdentity, bool) {
	counts := make(map[ch.EntryIdentity]int, len(reports))
	for _, report := range reports {
		if report.Result.State.Committed < index {
			continue
		}
		entry := report.Result.Entries[position]
		if entry.Index != index || !entry.Present {
			continue
		}
		counts[entry.Identity]++
		if counts[entry.Identity] >= quorum {
			return entry.Identity, true
		}
	}
	return ch.EntryIdentity{}, false
}

func validateQuorumChain(
	reports []recoveryProbeReport,
	byIndex map[uint64]int,
	from uint64,
	identity ch.EntryIdentity,
	to uint64,
	quorum int,
) error {
	if to < from || identity.Index != from {
		return ch.ErrLogConflict
	}
	for index := from; index < to; {
		if index == ^uint64(0) {
			return ch.ErrLogConflict
		}
		index++
		position, present := byIndex[index]
		if !present {
			return errRecoveryProbeIncomplete
		}
		next, ok := quorumIdentityAt(reports, position, index, quorum)
		if !ok {
			return errRecoveryProbeIncomplete
		}
		if next.PreviousIndex != identity.Index || next.PreviousTerm != identity.LeaderTerm ||
			next.PreviousDigest != identity.Digest {
			return ch.ErrLogConflict
		}
		identity = next
	}
	return nil
}
