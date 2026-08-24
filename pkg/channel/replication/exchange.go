package replication

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// ExchangeVersion is the only supported data-bearing peer protocol version.
const ExchangeVersion uint16 = 3

// ExchangePriority separates quorum-critical work from trailing convergence
// without changing the durability or validation required at the follower.
type ExchangePriority uint8

const (
	// ExchangePriorityForeground is the default for quorum, probe, and recovery work.
	ExchangePriorityForeground ExchangePriority = iota
	// ExchangePriorityBackground is valid only for post-quorum trailing replication.
	ExchangePriorityBackground
)

// Valid reports whether the priority is part of the closed wire contract.
func (p ExchangePriority) Valid() bool {
	return p == ExchangePriorityForeground || p == ExchangePriorityBackground
}

// ExchangeKind identifies one bounded peer operation.
type ExchangeKind uint8

const (
	// ExchangeReplicate carries an immutable proposal to one follower.
	ExchangeReplicate ExchangeKind = iota + 1
	// ExchangeProbe reads one bounded exact recovery view from a follower.
	ExchangeProbe
	// ExchangeFetch reads one bounded proposal-aligned recovery page.
	ExchangeFetch
)

// ReplicateStatus is the closed durable result of one follower replication.
type ReplicateStatus uint8

const (
	ReplicateDurable ReplicateStatus = iota + 1
	ReplicateAlreadyDurable
	ReplicateNeedFrom
	ReplicateStaleFence
	ReplicateConflict
	ReplicateBackpressured
	ReplicateOutcomeUnknown
)

// Valid reports whether status is one closed peer result.
func (s ReplicateStatus) Valid() bool {
	return s >= ReplicateDurable && s <= ReplicateOutcomeUnknown
}

// Durable reports whether the follower proved the requested immutable range.
func (s ReplicateStatus) Durable() bool {
	return s == ReplicateDurable || s == ReplicateAlreadyDurable
}

// ReplicateRequest carries records and their immutable manifest to one voter.
type ReplicateRequest struct {
	ChannelKey ch.ChannelKey
	ChannelID  ch.ChannelID
	Leader     ch.NodeID
	Follower   ch.NodeID
	Manifest   ch.ProposalManifest
	Records    []ch.Record
	// Committed is the leader's current committed frontier, never above this proposal.
	Committed uint64
	// ServerAllocatedMessageIDs carries the leader's all-record allocator proof.
	ServerAllocatedMessageIDs bool
}

// ReplicateProof is the follower's exact durable identity echo. A durable
// vote is valid only when every field equals the request that produced it.
type ReplicateProof struct {
	ChannelKey ch.ChannelKey
	ChannelID  ch.ChannelID
	Leader     ch.NodeID
	Follower   ch.NodeID
	Manifest   ch.ProposalManifest
}

func replicateProofFor(request ReplicateRequest) ReplicateProof {
	return ReplicateProof{
		ChannelKey: request.ChannelKey,
		ChannelID:  request.ChannelID,
		Leader:     request.Leader,
		Follower:   request.Follower,
		Manifest:   request.Manifest,
	}
}

// Valid reports whether the request carries one complete immutable proposal.
func (r ReplicateRequest) Valid() bool {
	if r.ChannelKey == "" || r.ChannelID.ID == "" || r.Leader == 0 || r.Follower == 0 || r.Leader == r.Follower ||
		!r.Manifest.ValidFor(r.Manifest.BaseOffset, len(r.Records)) || r.Committed > r.Manifest.LastOffset {
		return false
	}
	entries, ok := ch.DeriveProposalEntries(r.Manifest, len(r.Records), func(index int) ch.Record { return r.Records[index] })
	return ok && len(entries) == len(r.Records) && entries[len(entries)-1].Digest == r.Manifest.Digest
}

// ReplicateResult is the follower's durable response for one request.
type ReplicateResult struct {
	Status     ReplicateStatus
	LastOffset uint64
	// Proof is mandatory for durable statuses and zero for every other status.
	Proof ReplicateProof
	// NeedFrom is the follower's exact next offset when Status is ReplicateNeedFrom.
	NeedFrom uint64
}

// ProbeRequest asks one follower for its exact frontier and selected entry
// identities. It is read-only and cannot install authority.
type ProbeRequest struct {
	ChannelKey ch.ChannelKey
	ChannelID  ch.ChannelID
	Leader     ch.NodeID
	Follower   ch.NodeID
	Indexes    []uint64
}

// ProbeProof binds one read result to the exact Channel, participants, and
// requested positions that produced it. Indexes are frozen by the producer.
type ProbeProof struct {
	ChannelKey ch.ChannelKey
	ChannelID  ch.ChannelID
	Leader     ch.NodeID
	Follower   ch.NodeID
	Indexes    []uint64
}

func probeProofFor(request ProbeRequest) ProbeProof {
	return ProbeProof{
		ChannelKey: request.ChannelKey,
		ChannelID:  request.ChannelID,
		Leader:     request.Leader,
		Follower:   request.Follower,
		Indexes:    append([]uint64(nil), request.Indexes...),
	}
}

func sameProbeProof(first, second ProbeProof) bool {
	if first.ChannelKey != second.ChannelKey || first.ChannelID != second.ChannelID ||
		first.Leader != second.Leader || first.Follower != second.Follower || len(first.Indexes) != len(second.Indexes) {
		return false
	}
	for index := range first.Indexes {
		if first.Indexes[index] != second.Indexes[index] {
			return false
		}
	}
	return true
}

func zeroProbeProof(proof ProbeProof) bool {
	return proof.ChannelKey == "" && proof.ChannelID == (ch.ChannelID{}) && proof.Leader == 0 && proof.Follower == 0 && len(proof.Indexes) == 0
}

// Valid reports whether the probe is one bounded, identity-safe request.
func (r ProbeRequest) Valid() bool {
	return r.ChannelKey != "" && r.ChannelID.ID != "" && r.Leader != 0 && r.Follower != 0 &&
		r.Leader != r.Follower && len(r.Indexes) <= maxRecoveryProbeIndexes && validProbeIndexes(r.Indexes)
}

// ProbeResult is one exact follower recovery view.
type ProbeResult struct {
	Proof   ProbeProof
	State   ReplicaState
	Entries []EntryProbe
}

// FetchRequest asks one quorum supporter for a proposal-aligned donor page
// while fencing the read to its previously probed exact frontier.
type FetchRequest struct {
	ChannelKey ch.ChannelKey
	ChannelID  ch.ChannelID
	Leader     ch.NodeID
	Follower   ch.NodeID
	Expected   ReplicaState
	From       uint64
	Through    uint64
	Previous   ch.EntryIdentity
	MaxBytes   int
}

// FetchProof binds a donor page to the complete request that produced it.
type FetchProof struct {
	ChannelKey ch.ChannelKey
	ChannelID  ch.ChannelID
	Leader     ch.NodeID
	Follower   ch.NodeID
	Expected   ReplicaState
	From       uint64
	Through    uint64
	Previous   ch.EntryIdentity
	MaxBytes   int
}

func fetchProofFor(request FetchRequest) FetchProof {
	return FetchProof{
		ChannelKey: request.ChannelKey, ChannelID: request.ChannelID,
		Leader: request.Leader, Follower: request.Follower, Expected: request.Expected,
		From: request.From, Through: request.Through, Previous: request.Previous, MaxBytes: request.MaxBytes,
	}
}

// Valid reports whether the fetch is one exact bounded donor request.
func (r FetchRequest) Valid() bool {
	validPrevious := r.From == 1 && r.Previous == (ch.EntryIdentity{}) || r.From > 1 && validEntryIdentity(r.Previous) && r.Previous.Index == r.From-1
	return r.ChannelKey != "" && r.ChannelID.ID != "" && r.Leader != 0 && r.Follower != 0 && r.Leader != r.Follower && validPrevious &&
		validReplicaState(r.Expected) && r.From > 0 && r.Through >= r.From &&
		r.Through-r.From < maxRecoveryProbeIndexes && r.Through <= r.Expected.LEO && r.MaxBytes > 0
}

// FetchResult is one complete proposal-aligned donor page.
type FetchResult struct {
	Proof     FetchProof
	State     ReplicaState
	Proposals []RecoveryProposal
}

// ExchangeItem is one correlated request in a peer batch.
type ExchangeItem struct {
	RequestID uint64
	Kind      ExchangeKind
	Replicate *ReplicateRequest
	Probe     *ProbeRequest
	Fetch     *FetchRequest
}

// ExchangeBatch carries ready work for one target without another collection timer.
type ExchangeBatch struct {
	Version  uint16
	Priority ExchangePriority
	Items    []ExchangeItem
}

// ExchangeItemResult correlates one peer result to its request identity.
type ExchangeItemResult struct {
	RequestID uint64
	Replicate ReplicateResult
	Probe     ProbeResult
	Fetch     FetchResult
}

// ExchangeBatchResult returns exactly one correlated result per input item.
type ExchangeBatchResult struct {
	Version uint16
	Items   []ExchangeItemResult
}

// PeerLink sends one bounded batch to a repository-owned peer node.
type PeerLink interface {
	Exchange(context.Context, ch.NodeID, ExchangeBatch) (ExchangeBatchResult, error)
}

func estimateReplicateRequestBytes(request ReplicateRequest) int {
	const fixedBytes = 256
	total := fixedBytes + len(request.ChannelKey) + len(request.ChannelID.ID)
	for _, record := range request.Records {
		total += 96 + len(record.FromUID) + len(record.ClientMsgNo) + len(record.Payload)
	}
	return total
}

func estimateProbeRequestBytes(request ProbeRequest) int {
	const fixedBytes = 192
	return fixedBytes + len(request.ChannelKey) + len(request.ChannelID.ID) + len(request.Indexes)*192
}

func estimateFetchRequestBytes(request FetchRequest) int {
	const fixedBytes = 256
	return fixedBytes + len(request.ChannelKey) + len(request.ChannelID.ID) + request.MaxBytes
}
