package channel

import "github.com/WuKongIM/WuKongIM/pkg/quorumlog"

// ProposalManifestVersion is the only exact-append manifest format.
const ProposalManifestVersion = quorumlog.ProposalManifestVersion

// CommandID is the retry-stable identity of one immutable Channel proposal.
type CommandID = quorumlog.CommandID

// EntryDigest is the SHA-256 identity of one durable Channel log entry.
type EntryDigest = quorumlog.EntryDigest

// ProposalManifest binds a Channel proposal to authority and entry identity.
type ProposalManifest = quorumlog.ProposalManifest

// EntryIdentity is the durable identity of one Channel log entry.
type EntryIdentity = quorumlog.EntryIdentity

// AppendOutcome is the closed storage proof for one immutable append attempt.
type AppendOutcome = quorumlog.AppendOutcome

const (
	AppendOutcomeDurable              = quorumlog.AppendOutcomeDurable
	AppendOutcomeAlreadyDurable       = quorumlog.AppendOutcomeAlreadyDurable
	AppendOutcomeDefinitelyNotWritten = quorumlog.AppendOutcomeDefinitelyNotWritten
	AppendOutcomeConflict             = quorumlog.AppendOutcomeConflict
	AppendOutcomeUnknown              = quorumlog.AppendOutcomeUnknown
)

// DeriveProposalEntries constructs the entry hash chain for Channel records.
func DeriveProposalEntries(manifest ProposalManifest, recordCount int, recordAt func(int) Record) ([]EntryIdentity, bool) {
	return quorumlog.DeriveProposalEntries(manifest, recordCount, func(index int) quorumlog.Record {
		record := recordAt(index)
		return quorumlog.Record{
			ID: record.ID, Index: record.Index, Epoch: record.Epoch, Setting: record.Setting,
			FromUID: record.FromUID, ClientMsgNo: record.ClientMsgNo,
			ServerTimestampMS: record.ServerTimestampMS, SyncOnce: record.SyncOnce, Payload: record.Payload,
		}
	})
}

// SealProposalManifest derives and assigns the Channel proposal tail digest.
func SealProposalManifest(manifest ProposalManifest, records []Record) (ProposalManifest, []EntryIdentity, bool) {
	manifest.Digest = EntryDigest{}
	entries, ok := DeriveProposalEntries(manifest, len(records), func(index int) Record { return records[index] })
	if !ok {
		return ProposalManifest{}, nil, false
	}
	manifest.Digest = entries[len(entries)-1].Digest
	return manifest, entries, true
}
