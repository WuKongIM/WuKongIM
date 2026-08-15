package replication

import (
	"errors"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

func TestSelectRecoveryPrefixIgnoresMinorityOnlyHigherTail(t *testing.T) {
	prefix := recoveryIdentity(5, 5)
	minorityTail := recoveryIdentityAfter(prefix, 6, 6)
	reports := []recoveryProbeReport{
		recoveryReport(1, 5, 5, []EntryProbe{{Index: 6}, {Index: 5, Present: true, Identity: prefix}}),
		recoveryReport(2, 5, 5, []EntryProbe{{Index: 6}, {Index: 5, Present: true, Identity: prefix}}),
		recoveryReport(3, 6, 0, []EntryProbe{{Index: 6, Present: true, Identity: minorityTail}, {Index: 5, Present: true, Identity: prefix}}),
	}

	selected, err := selectRecoveryPrefix([]ch.NodeID{1, 2, 3}, 2, reports)
	if err != nil {
		t.Fatalf("selectRecoveryPrefix() error = %v", err)
	}
	if selected.Index != 5 || selected.Identity != prefix || selected.CertifiedCommitted != 5 {
		t.Fatalf("selectRecoveryPrefix() = %+v, want quorum prefix at 5", selected)
	}
}

func TestSelectRecoveryPrefixRejectsConflictAtCertifiedCommittedCut(t *testing.T) {
	first := recoveryIdentity(5, 5)
	second := recoveryIdentity(5, 9)
	reports := []recoveryProbeReport{
		recoveryReport(1, 5, 5, []EntryProbe{{Index: 5, Present: true, Identity: first}}),
		recoveryReport(2, 5, 5, []EntryProbe{{Index: 5, Present: true, Identity: second}}),
		recoveryReport(3, 5, 0, []EntryProbe{{Index: 5, Present: true, Identity: first}}),
	}

	_, err := selectRecoveryPrefix([]ch.NodeID{1, 2, 3}, 2, reports)
	if !errors.Is(err, ch.ErrLogConflict) {
		t.Fatalf("selectRecoveryPrefix() error = %v, want log conflict", err)
	}
}

func TestSelectRecoveryPrefixRejectsQuorumTailThatDoesNotExtendCertifiedCut(t *testing.T) {
	committed := recoveryIdentity(5, 5)
	otherPrefix := recoveryIdentity(5, 8)
	bridgedTail := recoveryIdentityAfter(otherPrefix, 6, 9)
	reports := []recoveryProbeReport{
		recoveryReport(1, 5, 5, []EntryProbe{{Index: 6}, {Index: 5, Present: true, Identity: committed}}),
		recoveryReport(2, 6, 5, []EntryProbe{{Index: 6, Present: true, Identity: bridgedTail}, {Index: 5, Present: true, Identity: committed}}),
		recoveryReport(3, 6, 0, []EntryProbe{{Index: 6, Present: true, Identity: bridgedTail}, {Index: 5, Present: true, Identity: otherPrefix}}),
	}

	_, err := selectRecoveryPrefix([]ch.NodeID{1, 2, 3}, 2, reports)
	if !errors.Is(err, ch.ErrLogConflict) {
		t.Fatalf("selectRecoveryPrefix() error = %v, want log conflict for tail detached from certified cut", err)
	}
}

func TestSelectRecoveryPrefixRequiresCompleteChainAboveCertifiedCut(t *testing.T) {
	committed := recoveryIdentity(5, 5)
	next := recoveryIdentityAfter(committed, 6, 6)
	tail := recoveryIdentityAfter(next, 7, 7)
	complete := []recoveryProbeReport{
		recoveryReport(1, 7, 5, []EntryProbe{{Index: 7, Present: true, Identity: tail}, {Index: 6, Present: true, Identity: next}, {Index: 5, Present: true, Identity: committed}}),
		recoveryReport(2, 7, 5, []EntryProbe{{Index: 7, Present: true, Identity: tail}, {Index: 6, Present: true, Identity: next}, {Index: 5, Present: true, Identity: committed}}),
		recoveryReport(3, 5, 0, []EntryProbe{{Index: 7}, {Index: 6}, {Index: 5, Present: true, Identity: committed}}),
	}

	selected, err := selectRecoveryPrefix([]ch.NodeID{1, 2, 3}, 2, complete)
	if err != nil || selected.Index != 7 || selected.Identity != tail {
		t.Fatalf("selectRecoveryPrefix(complete chain) = %+v, %v; want tail 7", selected, err)
	}
	for index := range complete {
		complete[index].Result.Entries = []EntryProbe{complete[index].Result.Entries[0], complete[index].Result.Entries[2]}
	}
	_, err = selectRecoveryPrefix([]ch.NodeID{1, 2, 3}, 2, complete)
	if !errors.Is(err, errRecoveryProbeIncomplete) {
		t.Fatalf("selectRecoveryPrefix(missing index 6) error = %v, want probe incomplete", err)
	}
}

func TestSelectRecoveryPrefixRequiresIntersectingQuorumAndCompleteBoundary(t *testing.T) {
	prefix := recoveryIdentity(4, 4)
	reports := []recoveryProbeReport{
		recoveryReport(1, 5, 0, []EntryProbe{{Index: 5, Present: true, Identity: recoveryIdentityAfter(prefix, 5, 5)}, {Index: 4, Present: true, Identity: prefix}}),
		recoveryReport(2, 5, 0, []EntryProbe{{Index: 5, Present: true, Identity: recoveryIdentityAfter(prefix, 5, 8)}, {Index: 4, Present: true, Identity: prefix}}),
		recoveryReport(3, 4, 0, []EntryProbe{{Index: 5}, {Index: 4, Present: true, Identity: prefix}}),
	}

	_, err := selectRecoveryPrefix([]ch.NodeID{1, 2, 3}, 1, reports)
	if !errors.Is(err, ch.ErrInvalidConfig) {
		t.Fatalf("selectRecoveryPrefix(non-intersecting) error = %v, want invalid config", err)
	}
	_, err = selectRecoveryPrefix([]ch.NodeID{1, 2, 3}, 2, []recoveryProbeReport{reports[0]})
	if !errors.Is(err, errRecoveryQuorumUnavailable) {
		t.Fatalf("selectRecoveryPrefix(no quorum) error = %v, want quorum unavailable", err)
	}
	for index := range reports {
		reports[index].Result.Entries = reports[index].Result.Entries[:1]
	}
	_, err = selectRecoveryPrefix([]ch.NodeID{1, 2, 3}, 2, reports)
	if !errors.Is(err, errRecoveryProbeIncomplete) {
		t.Fatalf("selectRecoveryPrefix(gapped proof) error = %v, want probe incomplete", err)
	}
}

func recoveryReport(voter ch.NodeID, leo, committed uint64, entries []EntryProbe) recoveryProbeReport {
	tail := entries[0].Identity
	if tail.Index != leo {
		for _, entry := range entries {
			if entry.Present && entry.Identity.Index == leo {
				tail = entry.Identity
				break
			}
		}
	}
	manifest := ch.ProposalManifest{
		Version: ch.ProposalManifestVersion, ChannelEpoch: tail.ChannelEpoch, LeaderTerm: tail.LeaderTerm,
		FenceVersion: tail.FenceVersion, CommandID: tail.CommandID, BaseOffset: leo - 1, LastOffset: leo,
		PreviousTerm: tail.PreviousTerm, PreviousIndex: tail.PreviousIndex,
		PreviousDigest: tail.PreviousDigest, Digest: tail.Digest,
	}
	return recoveryProbeReport{
		Voter: voter,
		Result: ProbeResult{State: ReplicaState{
			LEO: leo, Committed: committed, Manifest: manifest, TailIdentity: tail,
		}, Entries: entries},
	}
}

func recoveryIdentity(index uint64, marker byte) ch.EntryIdentity {
	identity := ch.EntryIdentity{
		Version: ch.ProposalManifestVersion, ChannelEpoch: 3, LeaderTerm: 5, FenceVersion: 7,
		Index: index, CommandID: ch.CommandID{31: marker}, Digest: ch.EntryDigest{31: marker},
	}
	if index > 1 {
		identity.PreviousTerm = 5
		identity.PreviousIndex = index - 1
		identity.PreviousDigest = ch.EntryDigest{31: marker - 1}
	}
	return identity
}

func recoveryIdentityAfter(previous ch.EntryIdentity, index uint64, marker byte) ch.EntryIdentity {
	identity := recoveryIdentity(index, marker)
	identity.PreviousTerm = previous.LeaderTerm
	identity.PreviousIndex = previous.Index
	identity.PreviousDigest = previous.Digest
	return identity
}
