package message

import (
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

func TestInspectDurableProposalClassifiesExactPersistedProposal(t *testing.T) {
	channelKey, proposal, entries := durableProposalFixture(t)
	view := proposalMapView{}
	view.storeProposal(channelKey, proposal)
	for _, entry := range entries {
		view[string(encodeEntryIdentityKey(channelKey, entry.Index))] = encodeDurableEntryIdentity(entry)
	}

	disposition, err := inspectDurableProposal(view, channelKey, proposal, entries)
	if err != nil {
		t.Fatalf("inspect durable proposal: %v", err)
	}
	if disposition != durableProposalAlreadyPresent {
		t.Fatalf("disposition = %v, want already present", disposition)
	}
}

func TestInspectDurableProposalRejectsIncompleteExpectedEntrySet(t *testing.T) {
	channelKey, proposal, entries := durableProposalFixture(t)
	view := proposalMapView{}
	view.storeProposal(channelKey, proposal)
	view[string(encodeEntryIdentityKey(channelKey, entries[0].Index))] = encodeDurableEntryIdentity(entries[0])

	_, err := inspectDurableProposal(view, channelKey, proposal, entries[:1])
	if !errors.Is(err, dberrors.ErrCorruptState) {
		t.Fatalf("error = %v, want ErrCorruptState", err)
	}
}

func TestInspectDurableProposalClassifiesFreshRangeOnlyWhenAllIdentityRowsAreAbsent(t *testing.T) {
	channelKey, proposal, entries := durableProposalFixture(t)

	disposition, err := inspectDurableProposal(proposalMapView{}, channelKey, proposal, entries)
	if err != nil {
		t.Fatalf("inspect fresh proposal: %v", err)
	}
	if disposition != durableProposalFresh {
		t.Fatalf("disposition = %v, want fresh", disposition)
	}

	orphaned := proposalMapView{
		string(encodeEntryIdentityKey(channelKey, entries[0].Index)): encodeDurableEntryIdentity(entries[0]),
	}
	if _, err := inspectDurableProposal(orphaned, channelKey, proposal, entries); !errors.Is(err, dberrors.ErrCorruptState) {
		t.Fatalf("orphaned entry error = %v, want ErrCorruptState", err)
	}
}

func TestInspectDurableProposalRejectsIncompleteOrMismatchedPairedIndexes(t *testing.T) {
	channelKey, proposal, entries := durableProposalFixture(t)
	encoded := encodeDurableProposalRecord(proposal)
	commandKey := string(encodeProposalByCommandKey(channelKey, proposal.manifest.CommandID))
	lastKey := string(encodeProposalByLastKey(channelKey, proposal.manifest.LastOffset))

	var otherCommand quorumlog.CommandID
	otherCommand[0] = 2
	mismatched := proposal
	mismatched.manifest.CommandID = otherCommand
	mismatched.manifest.Digest[0]++

	tests := []struct {
		name string
		view proposalMapView
	}{
		{"missing last-offset index", proposalMapView{commandKey: encoded}},
		{"missing command index", proposalMapView{lastKey: encoded}},
		{"paired indexes disagree", proposalMapView{commandKey: encoded, lastKey: encodeDurableProposalRecord(mismatched)}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := inspectDurableProposal(tc.view, channelKey, proposal, entries); !errors.Is(err, dberrors.ErrCorruptState) {
				t.Fatalf("error = %v, want ErrCorruptState", err)
			}
		})
	}
}

func TestInspectDurableProposalRejectsMissingOrChangedReplayEntry(t *testing.T) {
	channelKey, proposal, entries := durableProposalFixture(t)
	complete := proposalMapView{}
	complete.storeProposal(channelKey, proposal)
	for _, entry := range entries {
		complete[string(encodeEntryIdentityKey(channelKey, entry.Index))] = encodeDurableEntryIdentity(entry)
	}

	missing := complete.clone()
	delete(missing, string(encodeEntryIdentityKey(channelKey, entries[1].Index)))
	if _, err := inspectDurableProposal(missing, channelKey, proposal, entries); !errors.Is(err, dberrors.ErrCorruptState) {
		t.Fatalf("missing replay entry error = %v, want ErrCorruptState", err)
	}

	changed := complete.clone()
	changedEntry := entries[1]
	changedEntry.Digest[0]++
	changed[string(encodeEntryIdentityKey(channelKey, changedEntry.Index))] = encodeDurableEntryIdentity(changedEntry)
	if _, err := inspectDurableProposal(changed, channelKey, proposal, entries); !errors.Is(err, dberrors.ErrCorruptState) {
		t.Fatalf("changed replay entry error = %v, want ErrCorruptState", err)
	}
}

func TestInspectDurableProposalKeepsBoundedPointReadBudget(t *testing.T) {
	channelKey, proposal, entries := durableProposalFixture(t)
	replay := proposalMapView{}
	replay.storeProposal(channelKey, proposal)
	for _, entry := range entries {
		replay[string(encodeEntryIdentityKey(channelKey, entry.Index))] = encodeDurableEntryIdentity(entry)
	}

	for _, tc := range []struct {
		name   string
		values proposalMapView
	}{
		{name: "fresh", values: proposalMapView{}},
		{name: "replay", values: replay},
	} {
		t.Run(tc.name, func(t *testing.T) {
			view := &countingProposalView{values: tc.values}
			if _, err := inspectDurableProposal(view, channelKey, proposal, entries); err != nil {
				t.Fatalf("inspect proposal: %v", err)
			}
			if want := 2 + len(entries); view.reads != want {
				t.Fatalf("point reads = %d, want %d", view.reads, want)
			}
		})
	}
}

type proposalMapView map[string][]byte

func (v proposalMapView) Get(key []byte) ([]byte, bool, error) {
	value, ok := v[string(key)]
	return value, ok, nil
}

func (v proposalMapView) storeProposal(channelKey ChannelKey, proposal durableProposalRecord) {
	value := encodeDurableProposalRecord(proposal)
	v[string(encodeProposalByLastKey(channelKey, proposal.manifest.LastOffset))] = value
	v[string(encodeProposalByCommandKey(channelKey, proposal.manifest.CommandID))] = value
}

func (v proposalMapView) clone() proposalMapView {
	cloned := make(proposalMapView, len(v))
	for key, value := range v {
		cloned[key] = append([]byte(nil), value...)
	}
	return cloned
}

type countingProposalView struct {
	values proposalMapView
	reads  int
}

func (v *countingProposalView) Get(key []byte) ([]byte, bool, error) {
	v.reads++
	return v.values.Get(key)
}

func durableProposalFixture(t *testing.T) (ChannelKey, durableProposalRecord, []quorumlog.EntryIdentity) {
	t.Helper()
	var commandID quorumlog.CommandID
	commandID[0] = 1
	manifest := DurableProposalManifest{
		Version: DurableProposalManifestVersion, ChannelEpoch: 5, LeaderTerm: 7,
		FenceVersion: 9, CommandID: commandID, BaseOffset: 0, LastOffset: 2,
	}
	records := []quorumlog.Record{
		{ID: 101, Index: 1, Epoch: 5, FromUID: "u1", ClientMsgNo: "c1", ServerTimestampMS: 1001, Payload: []byte("one")},
		{ID: 102, Index: 2, Epoch: 5, FromUID: "u2", ClientMsgNo: "c2", ServerTimestampMS: 1002, Payload: []byte("two")},
	}
	sealed, entries, ok := quorumlog.SealProposalManifest(manifest, records)
	if !ok {
		t.Fatal("seal proposal fixture")
	}
	return ChannelKey("room:1"), durableProposalRecord{manifest: sealed}, entries
}
