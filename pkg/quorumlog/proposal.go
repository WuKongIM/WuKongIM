// Package quorumlog defines the storage-neutral identity of durable replicated
// log proposals and entries.
package quorumlog

import (
	"crypto/sha256"
	"encoding/binary"
)

// ProposalManifestVersion is the only supported manifest and entry format.
const ProposalManifestVersion uint16 = 1

// CommandID is the retry-stable identity of one immutable proposal.
type CommandID [sha256.Size]byte

// EntryDigest is the SHA-256 identity of one durable log entry.
type EntryDigest [sha256.Size]byte

// ProposalManifest binds one contiguous proposal to its authority, preceding
// entry, command, range, and final entry digest.
type ProposalManifest struct {
	// Version is the persisted manifest format version.
	Version uint16
	// ChannelEpoch fences identities from an older Channel generation.
	ChannelEpoch uint64
	// LeaderTerm is the authoritative term that created every proposal entry.
	LeaderTerm uint64
	// FenceVersion is the authoritative write-fence version.
	FenceVersion uint64
	// CommandID is stable across exact retries and unique within the Channel.
	CommandID CommandID
	// BaseOffset is the durable entry index immediately before this proposal.
	BaseOffset uint64
	// LastOffset is the final entry index in this proposal.
	LastOffset uint64
	// PreviousTerm is the term of the entry at BaseOffset, or zero at genesis.
	PreviousTerm uint64
	// PreviousIndex equals BaseOffset and makes the predecessor explicit.
	PreviousIndex uint64
	// PreviousDigest is the digest at BaseOffset, or zero at genesis.
	PreviousDigest EntryDigest
	// Digest is the derived digest of the entry at LastOffset.
	Digest EntryDigest
}

// EntryIdentity is the complete durable identity persisted beside one message
// row and certified by follower durability acknowledgements.
type EntryIdentity struct {
	// Version is the persisted entry identity format version.
	Version uint16
	// ChannelEpoch fences identities from an older Channel generation.
	ChannelEpoch uint64
	// LeaderTerm is the authority term that created this entry.
	LeaderTerm uint64
	// FenceVersion is the authority write-fence version.
	FenceVersion uint64
	// Index is the 1-based durable log offset.
	Index uint64
	// PreviousTerm is the authority term of the preceding entry.
	PreviousTerm uint64
	// PreviousIndex is the preceding durable log offset.
	PreviousIndex uint64
	// CommandID identifies the immutable proposal containing this entry.
	CommandID CommandID
	// PreviousDigest is the digest of the preceding entry.
	PreviousDigest EntryDigest
	// Digest binds this identity and the immutable record semantics.
	Digest EntryDigest
}

// Record is the storage-neutral semantic content bound into an entry digest.
type Record struct {
	// ID is the stable message identity.
	ID uint64
	// Index is the 1-based durable log offset.
	Index uint64
	// Epoch must equal the proposal Channel epoch.
	Epoch uint64
	// Setting carries the immutable message setting bits.
	Setting uint8
	// FromUID is the immutable sender identity.
	FromUID string
	// ClientMsgNo is the immutable client idempotency identity.
	ClientMsgNo string
	// ServerTimestampMS is the positive server append timestamp.
	ServerTimestampMS int64
	// SyncOnce marks one-shot command-sync content.
	SyncOnce bool
	// Payload is the immutable message body.
	Payload []byte
}

// StructurallyValid reports whether a manifest has a complete authority,
// command, range, predecessor, and tail identity.
func (m ProposalManifest) StructurallyValid() bool {
	if m.Version != ProposalManifestVersion || m.ChannelEpoch == 0 || m.LeaderTerm == 0 || m.FenceVersion == 0 ||
		m.CommandID == (CommandID{}) || m.Digest == (EntryDigest{}) ||
		m.LastOffset <= m.BaseOffset || m.PreviousIndex != m.BaseOffset {
		return false
	}
	if m.BaseOffset == 0 {
		return m.PreviousTerm == 0 && m.PreviousDigest == (EntryDigest{})
	}
	return m.PreviousTerm != 0 && m.PreviousDigest != (EntryDigest{})
}

// ValidFor reports whether the manifest describes exactly recordCount entries
// following expectedBase.
func (m ProposalManifest) ValidFor(expectedBase uint64, recordCount int) bool {
	return m.StructurallyValid() && recordCount > 0 && uint64(recordCount) <= ^uint64(0)-expectedBase &&
		m.BaseOffset == expectedBase && m.LastOffset == expectedBase+uint64(recordCount)
}

// DeriveProposalEntries constructs the entry-by-entry hash chain for records.
// recordAt must return immutable semantic records in proposal order.
func DeriveProposalEntries(manifest ProposalManifest, recordCount int, recordAt func(int) Record) ([]EntryIdentity, bool) {
	if recordAt == nil || recordCount <= 0 || uint64(recordCount) > ^uint64(0)-manifest.BaseOffset ||
		manifest.Version != ProposalManifestVersion || manifest.ChannelEpoch == 0 || manifest.LeaderTerm == 0 || manifest.FenceVersion == 0 ||
		manifest.CommandID == (CommandID{}) || manifest.LastOffset != manifest.BaseOffset+uint64(recordCount) ||
		manifest.PreviousIndex != manifest.BaseOffset {
		return nil, false
	}
	if manifest.BaseOffset == 0 {
		if manifest.PreviousTerm != 0 || manifest.PreviousDigest != (EntryDigest{}) {
			return nil, false
		}
	} else if manifest.PreviousTerm == 0 || manifest.PreviousDigest == (EntryDigest{}) {
		return nil, false
	}
	entries := make([]EntryIdentity, 0, recordCount)
	previousTerm := manifest.PreviousTerm
	previousIndex := manifest.PreviousIndex
	previousDigest := manifest.PreviousDigest
	for offset := 0; offset < recordCount; offset++ {
		index := manifest.BaseOffset + uint64(offset) + 1
		record := recordAt(offset)
		if record.ID == 0 || (record.Index != 0 && record.Index != index) || record.Epoch != manifest.ChannelEpoch || record.ServerTimestampMS <= 0 {
			return nil, false
		}
		entry := EntryIdentity{
			Version: ProposalManifestVersion, ChannelEpoch: manifest.ChannelEpoch,
			LeaderTerm: manifest.LeaderTerm, FenceVersion: manifest.FenceVersion,
			Index: index, PreviousTerm: previousTerm, PreviousIndex: previousIndex,
			CommandID: manifest.CommandID, PreviousDigest: previousDigest,
		}
		entry.Digest = digestProposalEntry(entry, record)
		entries = append(entries, entry)
		previousTerm = entry.LeaderTerm
		previousIndex = entry.Index
		previousDigest = entry.Digest
	}
	return entries, true
}

// SealProposalManifest derives and assigns the proposal's final entry digest.
func SealProposalManifest(manifest ProposalManifest, records []Record) (ProposalManifest, []EntryIdentity, bool) {
	manifest.Digest = EntryDigest{}
	entries, ok := DeriveProposalEntries(manifest, len(records), func(index int) Record { return records[index] })
	if !ok {
		return ProposalManifest{}, nil, false
	}
	manifest.Digest = entries[len(entries)-1].Digest
	return manifest, entries, true
}

// VerifyEntry reports whether record is the semantic content certified by
// entry's authority, predecessor, command, index, and digest.
func VerifyEntry(entry EntryIdentity, record Record) bool {
	if entry.Version != ProposalManifestVersion || entry.ChannelEpoch == 0 || entry.LeaderTerm == 0 || entry.FenceVersion == 0 ||
		entry.Index == 0 || entry.CommandID == (CommandID{}) || entry.Digest == (EntryDigest{}) ||
		entry.PreviousIndex+1 != entry.Index || record.ID == 0 || (record.Index != 0 && record.Index != entry.Index) ||
		record.Epoch != entry.ChannelEpoch || record.ServerTimestampMS <= 0 {
		return false
	}
	if entry.PreviousIndex == 0 {
		if entry.PreviousTerm != 0 || entry.PreviousDigest != (EntryDigest{}) {
			return false
		}
	} else if entry.PreviousTerm == 0 || entry.PreviousDigest == (EntryDigest{}) {
		return false
	}
	return digestProposalEntry(entry, record) == entry.Digest
}

func digestProposalEntry(entry EntryIdentity, record Record) EntryDigest {
	hash := sha256.New()
	_, _ = hash.Write([]byte("wukongim/channel-entry/v1\x00"))
	var encoded [8]byte
	writeUint64 := func(value uint64) {
		binary.BigEndian.PutUint64(encoded[:], value)
		_, _ = hash.Write(encoded[:])
	}
	writeUint64(entry.ChannelEpoch)
	writeUint64(entry.LeaderTerm)
	writeUint64(entry.FenceVersion)
	writeUint64(entry.Index)
	writeUint64(entry.PreviousTerm)
	writeUint64(entry.PreviousIndex)
	_, _ = hash.Write(entry.CommandID[:])
	_, _ = hash.Write(entry.PreviousDigest[:])
	writeUint64(record.ID)
	_, _ = hash.Write([]byte{record.Setting})
	if record.SyncOnce {
		_, _ = hash.Write([]byte{1})
	} else {
		_, _ = hash.Write([]byte{0})
	}
	writeUint64(uint64(record.ServerTimestampMS))
	writeBytes := func(value []byte) {
		writeUint64(uint64(len(value)))
		_, _ = hash.Write(value)
	}
	writeBytes([]byte(record.FromUID))
	writeBytes([]byte(record.ClientMsgNo))
	writeBytes(record.Payload)
	var digest EntryDigest
	copy(digest[:], hash.Sum(nil))
	return digest
}
