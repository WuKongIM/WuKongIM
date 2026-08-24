package store

import (
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// ProposalManifestVersion is the only exact-append manifest format.
const ProposalManifestVersion = ch.ProposalManifestVersion

// ProposalManifest is the shared immutable authority and hash-chain identity
// persisted by every Channel store implementation.
type ProposalManifest = ch.ProposalManifest

type proposalRecord struct {
	manifest ProposalManifest
}

func buildProposalRecord(manifest ProposalManifest, expectedBase uint64, records []ch.Record) (proposalRecord, []ch.EntryIdentity, error) {
	if !manifest.ValidFor(expectedBase, len(records)) {
		return proposalRecord{}, nil, ch.ErrInvalidConfig
	}
	entries, ok := ch.DeriveProposalEntries(manifest, len(records), func(index int) ch.Record {
		return records[index]
	})
	if !ok {
		return proposalRecord{}, nil, ch.ErrInvalidConfig
	}
	if entries[len(entries)-1].Digest != manifest.Digest {
		return proposalRecord{}, nil, ch.ErrLogConflict
	}
	return proposalRecord{manifest: manifest}, entries, nil
}
