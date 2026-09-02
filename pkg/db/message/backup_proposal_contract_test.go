package message

import (
	"bytes"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

func TestBackupProposalSystemEntriesAcceptCompleteCommittedChain(t *testing.T) {
	fixture := newBackupProposalContractFixture(t)

	if err := validateBackupProposalSystemEntries(fixture.channelKey, fixture.hw, fixture.entries); err != nil {
		t.Fatalf("validate complete committed proposal chain: %v", err)
	}
}

func TestBackupProposalSystemEntriesRejectIncompleteOrConflictingPairedIndexes(t *testing.T) {
	fixture := newBackupProposalContractFixture(t)
	first := fixture.proposals[0]
	second := fixture.proposals[1]
	conflictingSecond := second
	conflictingSecond.manifest.FenceVersion++

	tests := []struct {
		name    string
		entries []backupRawEntry
	}{
		{
			name: "missing command index",
			entries: withoutBackupProposalContractEntry(t, fixture.entries,
				encodeProposalByCommandKey(fixture.channelKey, first.manifest.CommandID)),
		},
		{
			name: "missing last-offset index",
			entries: withoutBackupProposalContractEntry(t, fixture.entries,
				encodeProposalByLastKey(fixture.channelKey, first.manifest.LastOffset)),
		},
		{
			name: "paired indexes disagree",
			entries: withBackupProposalContractValue(t, fixture.entries,
				encodeProposalByCommandKey(fixture.channelKey, second.manifest.CommandID),
				encodeDurableProposalRecord(conflictingSecond)),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateBackupProposalSystemEntries(fixture.channelKey, fixture.hw, tc.entries); !errors.Is(err, dberrors.ErrCorruptState) {
				t.Fatalf("error = %v, want ErrCorruptState", err)
			}
		})
	}
}

func TestBackupProposalSystemEntriesRejectRowsBeyondCommittedHW(t *testing.T) {
	fixture := newBackupProposalContractFixture(t)
	first := fixture.proposals[0]
	firstValue := encodeDurableProposalRecord(first)
	cutInsideProposal := []backupRawEntry{
		{Key: encodeProposalByLastKey(fixture.channelKey, first.manifest.LastOffset), Value: firstValue},
		{Key: encodeProposalByCommandKey(fixture.channelKey, first.manifest.CommandID), Value: firstValue},
	}
	if err := validateBackupProposalSystemEntries(fixture.channelKey, 1, cutInsideProposal); !errors.Is(err, dberrors.ErrCorruptState) {
		t.Fatalf("proposal crossing HW error = %v, want ErrCorruptState", err)
	}

	aboveHW := fixture.identities[len(fixture.identities)-1]
	aboveHW.PreviousTerm = aboveHW.LeaderTerm
	aboveHW.PreviousIndex = aboveHW.Index
	aboveHW.PreviousDigest = aboveHW.Digest
	aboveHW.Index++
	aboveHW.Digest[0]++
	entriesWithUncommittedIdentity := append(cloneBackupProposalContractEntries(fixture.entries), backupRawEntry{
		Key:   encodeEntryIdentityKey(fixture.channelKey, aboveHW.Index),
		Value: encodeDurableEntryIdentity(aboveHW),
	})
	if err := validateBackupProposalSystemEntries(fixture.channelKey, fixture.hw, entriesWithUncommittedIdentity); !errors.Is(err, dberrors.ErrCorruptState) {
		t.Fatalf("entry identity above HW error = %v, want ErrCorruptState", err)
	}
}

func TestBackupProposalSystemEntriesRejectBrokenProposalPredecessorChain(t *testing.T) {
	fixture := newBackupProposalContractFixture(t)
	first := fixture.proposals[0]
	second := fixture.proposals[1]

	missingPredecessor := withoutBackupProposalContractEntry(t, fixture.entries,
		encodeProposalByLastKey(fixture.channelKey, first.manifest.LastOffset))
	missingPredecessor = withoutBackupProposalContractEntry(t, missingPredecessor,
		encodeProposalByCommandKey(fixture.channelKey, first.manifest.CommandID))

	brokenLink := second
	brokenLink.manifest.PreviousTerm++
	brokenLinkValue := encodeDurableProposalRecord(brokenLink)
	brokenPredecessor := withBackupProposalContractValue(t, fixture.entries,
		encodeProposalByLastKey(fixture.channelKey, second.manifest.LastOffset), brokenLinkValue)
	brokenPredecessor = withBackupProposalContractValue(t, brokenPredecessor,
		encodeProposalByCommandKey(fixture.channelKey, second.manifest.CommandID), brokenLinkValue)

	for _, tc := range []struct {
		name    string
		entries []backupRawEntry
	}{
		{name: "missing predecessor proposal", entries: missingPredecessor},
		{name: "predecessor authority disagrees", entries: brokenPredecessor},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateBackupProposalSystemEntries(fixture.channelKey, fixture.hw, tc.entries); !errors.Is(err, dberrors.ErrCorruptState) {
				t.Fatalf("error = %v, want ErrCorruptState", err)
			}
		})
	}
}

func TestBackupProposalSystemEntriesRejectIdentityManifestDisagreement(t *testing.T) {
	fixture := newBackupProposalContractFixture(t)
	first := fixture.proposals[0]
	second := fixture.proposals[1]

	missingIdentity := withoutBackupProposalContractEntry(t, fixture.entries,
		encodeEntryIdentityKey(fixture.channelKey, fixture.identities[1].Index))

	wrongCommand := fixture.identities[1]
	wrongCommand.CommandID = quorumlog.CommandID{9}
	commandMismatch := withBackupProposalContractValue(t, fixture.entries,
		encodeEntryIdentityKey(fixture.channelKey, wrongCommand.Index), encodeDurableEntryIdentity(wrongCommand))

	brokenIdentityLink := fixture.identities[1]
	brokenIdentityLink.PreviousDigest[0]++
	chainMismatch := withBackupProposalContractValue(t, fixture.entries,
		encodeEntryIdentityKey(fixture.channelKey, brokenIdentityLink.Index), encodeDurableEntryIdentity(brokenIdentityLink))

	wrongTail := first
	wrongTail.manifest.Digest[0]++
	wrongTailValue := encodeDurableProposalRecord(wrongTail)
	tailMismatch := withBackupProposalContractValue(t, fixture.entries,
		encodeProposalByLastKey(fixture.channelKey, first.manifest.LastOffset), wrongTailValue)
	tailMismatch = withBackupProposalContractValue(t, tailMismatch,
		encodeProposalByCommandKey(fixture.channelKey, first.manifest.CommandID), wrongTailValue)

	orphanIdentity := withoutBackupProposalContractEntry(t, fixture.entries,
		encodeProposalByLastKey(fixture.channelKey, second.manifest.LastOffset))
	orphanIdentity = withoutBackupProposalContractEntry(t, orphanIdentity,
		encodeProposalByCommandKey(fixture.channelKey, second.manifest.CommandID))

	for _, tc := range []struct {
		name    string
		entries []backupRawEntry
	}{
		{name: "missing proposal entry identity", entries: missingIdentity},
		{name: "entry command disagrees with manifest", entries: commandMismatch},
		{name: "entry predecessor digest breaks hash chain", entries: chainMismatch},
		{name: "manifest tail digest disagrees with final entry", entries: tailMismatch},
		{name: "entry identity has no covering proposal", entries: orphanIdentity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateBackupProposalSystemEntries(fixture.channelKey, fixture.hw, tc.entries); !errors.Is(err, dberrors.ErrCorruptState) {
				t.Fatalf("error = %v, want ErrCorruptState", err)
			}
		})
	}
}

func TestBackupProposalSystemEntriesRejectAmbiguousOrMalformedRows(t *testing.T) {
	fixture := newBackupProposalContractFixture(t)
	duplicate := append(cloneBackupProposalContractEntries(fixture.entries), backupRawEntry{
		Key:   append([]byte(nil), fixture.entries[0].Key...),
		Value: append([]byte(nil), fixture.entries[0].Value...),
	})
	malformed := withBackupProposalContractValue(t, fixture.entries,
		encodeProposalByCommandKey(fixture.channelKey, fixture.proposals[0].manifest.CommandID), []byte("truncated"))

	for _, tc := range []struct {
		name    string
		entries []backupRawEntry
	}{
		{name: "duplicate system key", entries: duplicate},
		{name: "malformed command-index value", entries: malformed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateBackupProposalSystemEntries(fixture.channelKey, fixture.hw, tc.entries); !errors.Is(err, dberrors.ErrCorruptState) {
				t.Fatalf("error = %v, want ErrCorruptState", err)
			}
		})
	}
}

type backupProposalContractFixture struct {
	channelKey ChannelKey
	hw         uint64
	proposals  []durableProposalRecord
	identities []quorumlog.EntryIdentity
	entries    []backupRawEntry
}

func newBackupProposalContractFixture(t *testing.T) backupProposalContractFixture {
	t.Helper()
	channelKey := ChannelKey("backup:proposal-contract")
	firstManifest := DurableProposalManifest{
		Version: DurableProposalManifestVersion, ChannelEpoch: 3, LeaderTerm: 5, FenceVersion: 7,
		CommandID: quorumlog.CommandID{1}, BaseOffset: 0, LastOffset: 2,
	}
	firstRecords := []quorumlog.Record{
		{ID: 101, Index: 1, Epoch: 3, FromUID: "alice", ClientMsgNo: "c1", ServerTimestampMS: 1001, Payload: []byte("one")},
		{ID: 102, Index: 2, Epoch: 3, FromUID: "bob", ClientMsgNo: "c2", ServerTimestampMS: 1002, Payload: []byte("two")},
	}
	firstManifest, firstIdentities, ok := quorumlog.SealProposalManifest(firstManifest, firstRecords)
	if !ok {
		t.Fatal("seal first backup proposal fixture")
	}
	secondManifest := DurableProposalManifest{
		Version: DurableProposalManifestVersion, ChannelEpoch: 3, LeaderTerm: 8, FenceVersion: 9,
		CommandID: quorumlog.CommandID{2}, BaseOffset: 2, LastOffset: 3,
		PreviousTerm: firstManifest.LeaderTerm, PreviousIndex: 2, PreviousDigest: firstManifest.Digest,
	}
	secondRecords := []quorumlog.Record{
		{ID: 103, Index: 3, Epoch: 3, FromUID: "carol", ClientMsgNo: "c3", ServerTimestampMS: 1003, Payload: []byte("three")},
	}
	secondManifest, secondIdentities, ok := quorumlog.SealProposalManifest(secondManifest, secondRecords)
	if !ok {
		t.Fatal("seal second backup proposal fixture")
	}
	proposals := []durableProposalRecord{{manifest: firstManifest}, {manifest: secondManifest}}
	identities := append(append([]quorumlog.EntryIdentity(nil), firstIdentities...), secondIdentities...)
	entries := make([]backupRawEntry, 0, len(proposals)*2+len(identities))
	for _, proposal := range proposals {
		value := encodeDurableProposalRecord(proposal)
		entries = append(entries,
			backupRawEntry{Key: encodeProposalByLastKey(channelKey, proposal.manifest.LastOffset), Value: value},
			backupRawEntry{Key: encodeProposalByCommandKey(channelKey, proposal.manifest.CommandID), Value: value},
		)
	}
	for _, identity := range identities {
		entries = append(entries, backupRawEntry{
			Key:   encodeEntryIdentityKey(channelKey, identity.Index),
			Value: encodeDurableEntryIdentity(identity),
		})
	}
	return backupProposalContractFixture{
		channelKey: channelKey,
		hw:         secondManifest.LastOffset,
		proposals:  proposals,
		identities: identities,
		entries:    entries,
	}
}

func withoutBackupProposalContractEntry(t *testing.T, entries []backupRawEntry, key []byte) []backupRawEntry {
	t.Helper()
	cloned := make([]backupRawEntry, 0, len(entries)-1)
	found := false
	for _, entry := range entries {
		if bytes.Equal(entry.Key, key) {
			found = true
			continue
		}
		cloned = append(cloned, backupRawEntry{
			Key:   append([]byte(nil), entry.Key...),
			Value: append([]byte(nil), entry.Value...),
		})
	}
	if !found {
		t.Fatalf("backup proposal fixture key not found: %x", key)
	}
	return cloned
}

func withBackupProposalContractValue(t *testing.T, entries []backupRawEntry, key, value []byte) []backupRawEntry {
	t.Helper()
	cloned := cloneBackupProposalContractEntries(entries)
	for index := range cloned {
		if bytes.Equal(cloned[index].Key, key) {
			cloned[index].Value = append([]byte(nil), value...)
			return cloned
		}
	}
	t.Fatalf("backup proposal fixture key not found: %x", key)
	return nil
}

func cloneBackupProposalContractEntries(entries []backupRawEntry) []backupRawEntry {
	cloned := make([]backupRawEntry, len(entries))
	for index, entry := range entries {
		cloned[index] = backupRawEntry{
			Key:   append([]byte(nil), entry.Key...),
			Value: append([]byte(nil), entry.Value...),
		}
	}
	return cloned
}
