package replication

import (
	"encoding/binary"
	"errors"
	"math"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

const (
	// MaxExchangeBatchItems is the fixed wire-level item allocation bound.
	MaxExchangeBatchItems = 64
	// MaxExchangeBatchBytes is the fixed wire-level frame allocation bound.
	MaxExchangeBatchBytes = 4 << 20
)

var errInvalidExchangeFrame = errors.New("channel replication: invalid exchange frame")

// EncodeExchangeBatch encodes one bounded peer request batch.
func EncodeExchangeBatch(batch ExchangeBatch) ([]byte, error) {
	if batch.Version != ExchangeVersion || len(batch.Items) == 0 || len(batch.Items) > MaxExchangeBatchItems {
		return nil, ch.ErrInvalidConfig
	}
	buf := appendCodecUvarint(nil, uint64(batch.Version))
	buf = appendCodecUvarint(buf, uint64(len(batch.Items)))
	for _, item := range batch.Items {
		if item.RequestID == 0 {
			return nil, ch.ErrInvalidConfig
		}
		buf = appendCodecUvarint(buf, item.RequestID)
		buf = append(buf, byte(item.Kind))
		switch item.Kind {
		case ExchangeReplicate:
			if item.Replicate == nil || item.Probe != nil || item.Fetch != nil || !item.Replicate.Valid() {
				return nil, ch.ErrInvalidConfig
			}
			buf = appendReplicateRequest(buf, *item.Replicate)
		case ExchangeProbe:
			if item.Probe == nil || item.Replicate != nil || item.Fetch != nil || !item.Probe.Valid() {
				return nil, ch.ErrInvalidConfig
			}
			buf = appendProbeRequest(buf, *item.Probe)
		case ExchangeFetch:
			if item.Fetch == nil || item.Replicate != nil || item.Probe != nil || !item.Fetch.Valid() {
				return nil, ch.ErrInvalidConfig
			}
			buf = appendFetchRequest(buf, *item.Fetch)
		default:
			return nil, ch.ErrInvalidConfig
		}
		if len(buf) > MaxExchangeBatchBytes {
			return nil, ch.ErrBackpressured
		}
	}
	return buf, nil
}

// DecodeExchangeBatch decodes one bounded peer request batch.
func DecodeExchangeBatch(data []byte) (ExchangeBatch, error) {
	if len(data) == 0 || len(data) > MaxExchangeBatchBytes {
		return ExchangeBatch{}, errInvalidExchangeFrame
	}
	c := exchangeCursor{data: data}
	version, ok := c.uvarint()
	if !ok || version != uint64(ExchangeVersion) {
		return ExchangeBatch{}, errInvalidExchangeFrame
	}
	count, ok := c.count(MaxExchangeBatchItems)
	if !ok || count == 0 {
		return ExchangeBatch{}, errInvalidExchangeFrame
	}
	batch := ExchangeBatch{Version: ExchangeVersion, Items: make([]ExchangeItem, count)}
	for index := range batch.Items {
		requestID, valid := c.uvarint()
		kind, validKind := c.byte()
		if !valid || !validKind || requestID == 0 {
			return ExchangeBatch{}, errInvalidExchangeFrame
		}
		item := ExchangeItem{RequestID: requestID, Kind: ExchangeKind(kind)}
		switch item.Kind {
		case ExchangeReplicate:
			request, valid := c.replicateRequest()
			if !valid || !request.Valid() {
				return ExchangeBatch{}, errInvalidExchangeFrame
			}
			item.Replicate = &request
		case ExchangeProbe:
			request, valid := c.probeRequest()
			if !valid || !request.Valid() {
				return ExchangeBatch{}, errInvalidExchangeFrame
			}
			item.Probe = &request
		case ExchangeFetch:
			request, valid := c.fetchRequest()
			if !valid || !request.Valid() {
				return ExchangeBatch{}, errInvalidExchangeFrame
			}
			item.Fetch = &request
		default:
			return ExchangeBatch{}, errInvalidExchangeFrame
		}
		batch.Items[index] = item
	}
	if c.offset != len(data) {
		return ExchangeBatch{}, errInvalidExchangeFrame
	}
	return batch, nil
}

// EncodeExchangeBatchResult encodes one bounded position-correlated response.
func EncodeExchangeBatchResult(result ExchangeBatchResult) ([]byte, error) {
	if result.Version != ExchangeVersion || len(result.Items) == 0 || len(result.Items) > MaxExchangeBatchItems {
		return nil, ch.ErrInvalidConfig
	}
	buf := appendCodecUvarint(nil, uint64(result.Version))
	buf = appendCodecUvarint(buf, uint64(len(result.Items)))
	for _, item := range result.Items {
		if item.RequestID == 0 || len(item.Probe.Entries) > maxRecoveryProbeIndexes ||
			len(item.Fetch.Proposals) > maxRecoveryReplacementProposals {
			return nil, ch.ErrInvalidConfig
		}
		buf = appendCodecUvarint(buf, item.RequestID)
		buf = appendReplicateResult(buf, item.Replicate)
		buf = appendProbeResult(buf, item.Probe)
		buf = appendFetchResult(buf, item.Fetch)
		if len(buf) > MaxExchangeBatchBytes {
			return nil, ch.ErrBackpressured
		}
	}
	return buf, nil
}

// DecodeExchangeBatchResult decodes one bounded position-correlated response.
func DecodeExchangeBatchResult(data []byte) (ExchangeBatchResult, error) {
	if len(data) == 0 || len(data) > MaxExchangeBatchBytes {
		return ExchangeBatchResult{}, errInvalidExchangeFrame
	}
	c := exchangeCursor{data: data}
	version, ok := c.uvarint()
	if !ok || version != uint64(ExchangeVersion) {
		return ExchangeBatchResult{}, errInvalidExchangeFrame
	}
	count, ok := c.count(MaxExchangeBatchItems)
	if !ok || count == 0 {
		return ExchangeBatchResult{}, errInvalidExchangeFrame
	}
	result := ExchangeBatchResult{Version: ExchangeVersion, Items: make([]ExchangeItemResult, count)}
	for index := range result.Items {
		requestID, valid := c.uvarint()
		replicate, validReplicate := c.replicateResult()
		probe, validProbe := c.probeResult()
		fetch, validFetch := c.fetchResult()
		if !valid || requestID == 0 || !validReplicate || !validProbe || !validFetch {
			return ExchangeBatchResult{}, errInvalidExchangeFrame
		}
		result.Items[index] = ExchangeItemResult{RequestID: requestID, Replicate: replicate, Probe: probe, Fetch: fetch}
	}
	if c.offset != len(data) {
		return ExchangeBatchResult{}, errInvalidExchangeFrame
	}
	return result, nil
}

func appendReplicateRequest(dst []byte, request ReplicateRequest) []byte {
	dst = appendChannelIdentity(dst, request.ChannelKey, request.ChannelID)
	dst = appendCodecUvarint(dst, uint64(request.Leader))
	dst = appendCodecUvarint(dst, uint64(request.Follower))
	dst = appendProposalManifest(dst, request.Manifest)
	dst = appendRecords(dst, request.Records)
	return appendCodecUvarint(dst, request.Committed)
}

func appendProbeRequest(dst []byte, request ProbeRequest) []byte {
	dst = appendChannelIdentity(dst, request.ChannelKey, request.ChannelID)
	dst = appendCodecUvarint(dst, uint64(request.Leader))
	dst = appendCodecUvarint(dst, uint64(request.Follower))
	dst = appendCodecSliceCount(dst, len(request.Indexes), request.Indexes == nil)
	for _, index := range request.Indexes {
		dst = appendCodecUvarint(dst, index)
	}
	return dst
}

func appendFetchRequest(dst []byte, request FetchRequest) []byte {
	dst = appendChannelIdentity(dst, request.ChannelKey, request.ChannelID)
	dst = appendCodecUvarint(dst, uint64(request.Leader))
	dst = appendCodecUvarint(dst, uint64(request.Follower))
	dst = appendReplicaState(dst, request.Expected)
	dst = appendCodecUvarint(dst, request.From)
	dst = appendCodecUvarint(dst, request.Through)
	dst = appendEntryIdentity(dst, request.Previous)
	return appendCodecUvarint(dst, uint64(request.MaxBytes))
}

func appendReplicateResult(dst []byte, result ReplicateResult) []byte {
	dst = append(dst, byte(result.Status))
	dst = appendCodecUvarint(dst, result.LastOffset)
	dst = appendCodecUvarint(dst, result.NeedFrom)
	return appendReplicateProof(dst, result.Proof)
}

func appendProbeResult(dst []byte, result ProbeResult) []byte {
	dst = appendProbeProof(dst, result.Proof)
	dst = appendReplicaState(dst, result.State)
	dst = appendCodecSliceCount(dst, len(result.Entries), result.Entries == nil)
	for _, entry := range result.Entries {
		dst = appendCodecUvarint(dst, entry.Index)
		dst = appendCodecBool(dst, entry.Present)
		dst = appendEntryIdentity(dst, entry.Identity)
	}
	return dst
}

func appendFetchResult(dst []byte, result FetchResult) []byte {
	dst = appendFetchProof(dst, result.Proof)
	dst = appendReplicaState(dst, result.State)
	dst = appendCodecSliceCount(dst, len(result.Proposals), result.Proposals == nil)
	for _, proposal := range result.Proposals {
		dst = appendProposalManifest(dst, proposal.Manifest)
		dst = appendRecords(dst, proposal.Records)
	}
	return dst
}

func appendReplicateProof(dst []byte, proof ReplicateProof) []byte {
	dst = appendChannelIdentity(dst, proof.ChannelKey, proof.ChannelID)
	dst = appendCodecUvarint(dst, uint64(proof.Leader))
	dst = appendCodecUvarint(dst, uint64(proof.Follower))
	return appendProposalManifest(dst, proof.Manifest)
}

func appendProbeProof(dst []byte, proof ProbeProof) []byte {
	dst = appendChannelIdentity(dst, proof.ChannelKey, proof.ChannelID)
	dst = appendCodecUvarint(dst, uint64(proof.Leader))
	dst = appendCodecUvarint(dst, uint64(proof.Follower))
	dst = appendCodecSliceCount(dst, len(proof.Indexes), proof.Indexes == nil)
	for _, index := range proof.Indexes {
		dst = appendCodecUvarint(dst, index)
	}
	return dst
}

func appendFetchProof(dst []byte, proof FetchProof) []byte {
	dst = appendChannelIdentity(dst, proof.ChannelKey, proof.ChannelID)
	dst = appendCodecUvarint(dst, uint64(proof.Leader))
	dst = appendCodecUvarint(dst, uint64(proof.Follower))
	dst = appendReplicaState(dst, proof.Expected)
	dst = appendCodecUvarint(dst, proof.From)
	dst = appendCodecUvarint(dst, proof.Through)
	dst = appendEntryIdentity(dst, proof.Previous)
	return appendCodecUvarint(dst, uint64(proof.MaxBytes))
}

func appendChannelIdentity(dst []byte, key ch.ChannelKey, id ch.ChannelID) []byte {
	dst = appendCodecString(dst, string(key))
	dst = appendCodecString(dst, id.ID)
	return append(dst, id.Type)
}

func appendProposalManifest(dst []byte, manifest ch.ProposalManifest) []byte {
	dst = appendCodecUvarint(dst, uint64(manifest.Version))
	dst = appendCodecUvarint(dst, manifest.ChannelEpoch)
	dst = appendCodecUvarint(dst, manifest.LeaderTerm)
	dst = appendCodecUvarint(dst, manifest.FenceVersion)
	dst = append(dst, manifest.CommandID[:]...)
	dst = appendCodecUvarint(dst, manifest.BaseOffset)
	dst = appendCodecUvarint(dst, manifest.LastOffset)
	dst = appendCodecUvarint(dst, manifest.PreviousTerm)
	dst = appendCodecUvarint(dst, manifest.PreviousIndex)
	dst = append(dst, manifest.PreviousDigest[:]...)
	return append(dst, manifest.Digest[:]...)
}

func appendEntryIdentity(dst []byte, identity ch.EntryIdentity) []byte {
	dst = appendCodecUvarint(dst, uint64(identity.Version))
	dst = appendCodecUvarint(dst, identity.ChannelEpoch)
	dst = appendCodecUvarint(dst, identity.LeaderTerm)
	dst = appendCodecUvarint(dst, identity.FenceVersion)
	dst = appendCodecUvarint(dst, identity.Index)
	dst = appendCodecUvarint(dst, identity.PreviousTerm)
	dst = appendCodecUvarint(dst, identity.PreviousIndex)
	dst = append(dst, identity.CommandID[:]...)
	dst = append(dst, identity.PreviousDigest[:]...)
	return append(dst, identity.Digest[:]...)
}

func appendReplicaState(dst []byte, state ReplicaState) []byte {
	dst = appendCodecUvarint(dst, state.LEO)
	dst = appendCodecUvarint(dst, state.Committed)
	dst = appendProposalManifest(dst, state.Manifest)
	return appendEntryIdentity(dst, state.TailIdentity)
}

func appendRecords(dst []byte, records []ch.Record) []byte {
	dst = appendCodecSliceCount(dst, len(records), records == nil)
	for _, record := range records {
		dst = appendCodecUvarint(dst, record.ID)
		dst = appendCodecUvarint(dst, record.Index)
		dst = appendCodecUvarint(dst, record.Epoch)
		dst = append(dst, record.Setting)
		dst = appendCodecString(dst, record.FromUID)
		dst = appendCodecString(dst, record.ClientMsgNo)
		dst = binary.AppendVarint(dst, record.ServerTimestampMS)
		dst = appendCodecBool(dst, record.SyncOnce)
		dst = appendCodecBytes(dst, record.Payload)
		dst = appendCodecUvarint(dst, uint64(record.SizeBytes))
	}
	return dst
}

func appendCodecUvarint(dst []byte, value uint64) []byte { return binary.AppendUvarint(dst, value) }

func appendCodecBool(dst []byte, value bool) []byte {
	if value {
		return append(dst, 1)
	}
	return append(dst, 0)
}

func appendCodecString(dst []byte, value string) []byte {
	dst = appendCodecUvarint(dst, uint64(len(value)))
	return append(dst, value...)
}

func appendCodecBytes(dst []byte, value []byte) []byte {
	dst = appendCodecUvarint(dst, uint64(len(value)))
	return append(dst, value...)
}

func appendCodecSliceCount(dst []byte, count int, isNil bool) []byte {
	if isNil {
		return appendCodecUvarint(dst, 0)
	}
	return appendCodecUvarint(dst, uint64(count)+1)
}

type exchangeCursor struct {
	data   []byte
	offset int
}

func (c *exchangeCursor) uvarint() (uint64, bool) {
	if c.offset >= len(c.data) {
		return 0, false
	}
	value, size := binary.Uvarint(c.data[c.offset:])
	if size <= 0 {
		return 0, false
	}
	c.offset += size
	return value, true
}

func (c *exchangeCursor) varint() (int64, bool) {
	if c.offset >= len(c.data) {
		return 0, false
	}
	value, size := binary.Varint(c.data[c.offset:])
	if size <= 0 {
		return 0, false
	}
	c.offset += size
	return value, true
}

func (c *exchangeCursor) count(maximum int) (int, bool) {
	value, ok := c.uvarint()
	if !ok || value > uint64(maximum) || value > uint64(math.MaxInt) {
		return 0, false
	}
	return int(value), true
}

func (c *exchangeCursor) sliceCount(maximum int) (count int, isNil bool, ok bool) {
	value, valid := c.uvarint()
	if !valid {
		return 0, false, false
	}
	if value == 0 {
		return 0, true, true
	}
	value--
	if value > uint64(maximum) || value > uint64(math.MaxInt) {
		return 0, false, false
	}
	return int(value), false, true
}

func (c *exchangeCursor) byte() (byte, bool) {
	if c.offset >= len(c.data) {
		return 0, false
	}
	value := c.data[c.offset]
	c.offset++
	return value, true
}

func (c *exchangeCursor) boolean() (bool, bool) {
	value, ok := c.byte()
	return value == 1, ok && value <= 1
}

func (c *exchangeCursor) bytes() ([]byte, bool) {
	count, ok := c.count(MaxExchangeBatchBytes)
	if !ok || count > len(c.data)-c.offset {
		return nil, false
	}
	value := append([]byte(nil), c.data[c.offset:c.offset+count]...)
	c.offset += count
	return value, true
}

func (c *exchangeCursor) string() (string, bool) {
	value, ok := c.bytes()
	return string(value), ok
}

func (c *exchangeCursor) fixed32() ([32]byte, bool) {
	var value [32]byte
	if len(c.data)-c.offset < len(value) {
		return value, false
	}
	copy(value[:], c.data[c.offset:c.offset+len(value)])
	c.offset += len(value)
	return value, true
}

func (c *exchangeCursor) channelIdentity() (ch.ChannelKey, ch.ChannelID, bool) {
	key, okKey := c.string()
	id, okID := c.string()
	typeValue, okType := c.byte()
	return ch.ChannelKey(key), ch.ChannelID{ID: id, Type: typeValue}, okKey && okID && okType
}

func (c *exchangeCursor) replicateRequest() (ReplicateRequest, bool) {
	key, id, ok := c.channelIdentity()
	leader, okLeader := c.uvarint()
	follower, okFollower := c.uvarint()
	manifest, okManifest := c.proposalManifest()
	records, okRecords := c.records()
	committed, okCommitted := c.uvarint()
	return ReplicateRequest{
		ChannelKey: key, ChannelID: id, Leader: ch.NodeID(leader), Follower: ch.NodeID(follower),
		Manifest: manifest, Records: records, Committed: committed,
	}, ok && okLeader && okFollower && okManifest && okRecords && okCommitted
}

func (c *exchangeCursor) probeRequest() (ProbeRequest, bool) {
	key, id, ok := c.channelIdentity()
	leader, okLeader := c.uvarint()
	follower, okFollower := c.uvarint()
	count, nilIndexes, okCount := c.sliceCount(maxRecoveryProbeIndexes)
	var indexes []uint64
	if !nilIndexes {
		indexes = make([]uint64, count)
	}
	for index := range indexes {
		var valid bool
		indexes[index], valid = c.uvarint()
		okCount = okCount && valid
	}
	return ProbeRequest{ChannelKey: key, ChannelID: id, Leader: ch.NodeID(leader), Follower: ch.NodeID(follower), Indexes: indexes},
		ok && okLeader && okFollower && okCount
}

func (c *exchangeCursor) fetchRequest() (FetchRequest, bool) {
	key, id, ok := c.channelIdentity()
	leader, okLeader := c.uvarint()
	follower, okFollower := c.uvarint()
	expected, okExpected := c.replicaState()
	from, okFrom := c.uvarint()
	through, okThrough := c.uvarint()
	previous, okPrevious := c.entryIdentity()
	maxBytes, okMaxBytes := c.uvarint()
	return FetchRequest{
		ChannelKey: key, ChannelID: id, Leader: ch.NodeID(leader), Follower: ch.NodeID(follower),
		Expected: expected, From: from, Through: through, Previous: previous, MaxBytes: int(maxBytes),
	}, ok && okLeader && okFollower && okExpected && okFrom && okThrough && okPrevious && okMaxBytes && maxBytes <= math.MaxInt
}

func (c *exchangeCursor) replicateResult() (ReplicateResult, bool) {
	status, okStatus := c.byte()
	last, okLast := c.uvarint()
	needFrom, okNeed := c.uvarint()
	proof, okProof := c.replicateProof()
	return ReplicateResult{Status: ReplicateStatus(status), LastOffset: last, NeedFrom: needFrom, Proof: proof},
		okStatus && okLast && okNeed && okProof
}

func (c *exchangeCursor) probeResult() (ProbeResult, bool) {
	proof, okProof := c.probeProof()
	state, okState := c.replicaState()
	count, nilEntries, okCount := c.sliceCount(maxRecoveryProbeIndexes)
	var entries []EntryProbe
	if !nilEntries {
		entries = make([]EntryProbe, count)
	}
	for index := range entries {
		entryIndex, okIndex := c.uvarint()
		present, okPresent := c.boolean()
		identity, okIdentity := c.entryIdentity()
		entries[index] = EntryProbe{Index: entryIndex, Present: present, Identity: identity}
		okCount = okCount && okIndex && okPresent && okIdentity
	}
	return ProbeResult{Proof: proof, State: state, Entries: entries}, okProof && okState && okCount
}

func (c *exchangeCursor) fetchResult() (FetchResult, bool) {
	proof, okProof := c.fetchProof()
	state, okState := c.replicaState()
	count, nilProposals, okCount := c.sliceCount(maxRecoveryReplacementProposals)
	var proposals []RecoveryProposal
	if !nilProposals {
		proposals = make([]RecoveryProposal, count)
	}
	for index := range proposals {
		manifest, okManifest := c.proposalManifest()
		records, okRecords := c.records()
		proposals[index] = RecoveryProposal{Manifest: manifest, Records: records}
		okCount = okCount && okManifest && okRecords
	}
	return FetchResult{Proof: proof, State: state, Proposals: proposals}, okProof && okState && okCount
}

func (c *exchangeCursor) replicateProof() (ReplicateProof, bool) {
	key, id, ok := c.channelIdentity()
	leader, okLeader := c.uvarint()
	follower, okFollower := c.uvarint()
	manifest, okManifest := c.proposalManifest()
	return ReplicateProof{ChannelKey: key, ChannelID: id, Leader: ch.NodeID(leader), Follower: ch.NodeID(follower), Manifest: manifest},
		ok && okLeader && okFollower && okManifest
}

func (c *exchangeCursor) probeProof() (ProbeProof, bool) {
	key, id, ok := c.channelIdentity()
	leader, okLeader := c.uvarint()
	follower, okFollower := c.uvarint()
	count, nilIndexes, okCount := c.sliceCount(maxRecoveryProbeIndexes)
	var indexes []uint64
	if !nilIndexes {
		indexes = make([]uint64, count)
	}
	for index := range indexes {
		var valid bool
		indexes[index], valid = c.uvarint()
		okCount = okCount && valid
	}
	return ProbeProof{ChannelKey: key, ChannelID: id, Leader: ch.NodeID(leader), Follower: ch.NodeID(follower), Indexes: indexes},
		ok && okLeader && okFollower && okCount
}

func (c *exchangeCursor) fetchProof() (FetchProof, bool) {
	key, id, ok := c.channelIdentity()
	leader, okLeader := c.uvarint()
	follower, okFollower := c.uvarint()
	expected, okExpected := c.replicaState()
	from, okFrom := c.uvarint()
	through, okThrough := c.uvarint()
	previous, okPrevious := c.entryIdentity()
	maxBytes, okMaxBytes := c.uvarint()
	return FetchProof{
		ChannelKey: key, ChannelID: id, Leader: ch.NodeID(leader), Follower: ch.NodeID(follower), Expected: expected,
		From: from, Through: through, Previous: previous, MaxBytes: int(maxBytes),
	}, ok && okLeader && okFollower && okExpected && okFrom && okThrough && okPrevious && okMaxBytes && maxBytes <= math.MaxInt
}

func (c *exchangeCursor) proposalManifest() (ch.ProposalManifest, bool) {
	version, okVersion := c.uvarint()
	epoch, okEpoch := c.uvarint()
	term, okTerm := c.uvarint()
	fence, okFence := c.uvarint()
	command, okCommand := c.fixed32()
	base, okBase := c.uvarint()
	last, okLast := c.uvarint()
	previousTerm, okPreviousTerm := c.uvarint()
	previousIndex, okPreviousIndex := c.uvarint()
	previousDigest, okPreviousDigest := c.fixed32()
	digest, okDigest := c.fixed32()
	return ch.ProposalManifest{
			Version: uint16(version), ChannelEpoch: epoch, LeaderTerm: term, FenceVersion: fence, CommandID: command,
			BaseOffset: base, LastOffset: last, PreviousTerm: previousTerm, PreviousIndex: previousIndex,
			PreviousDigest: previousDigest, Digest: digest,
		}, okVersion && version <= math.MaxUint16 && okEpoch && okTerm && okFence && okCommand && okBase && okLast &&
			okPreviousTerm && okPreviousIndex && okPreviousDigest && okDigest
}

func (c *exchangeCursor) entryIdentity() (ch.EntryIdentity, bool) {
	version, okVersion := c.uvarint()
	epoch, okEpoch := c.uvarint()
	term, okTerm := c.uvarint()
	fence, okFence := c.uvarint()
	index, okIndex := c.uvarint()
	previousTerm, okPreviousTerm := c.uvarint()
	previousIndex, okPreviousIndex := c.uvarint()
	command, okCommand := c.fixed32()
	previousDigest, okPreviousDigest := c.fixed32()
	digest, okDigest := c.fixed32()
	return ch.EntryIdentity{
			Version: uint16(version), ChannelEpoch: epoch, LeaderTerm: term, FenceVersion: fence, Index: index,
			PreviousTerm: previousTerm, PreviousIndex: previousIndex, CommandID: command,
			PreviousDigest: previousDigest, Digest: digest,
		}, okVersion && version <= math.MaxUint16 && okEpoch && okTerm && okFence && okIndex && okPreviousTerm &&
			okPreviousIndex && okCommand && okPreviousDigest && okDigest
}

func (c *exchangeCursor) replicaState() (ReplicaState, bool) {
	leo, okLEO := c.uvarint()
	committed, okCommitted := c.uvarint()
	manifest, okManifest := c.proposalManifest()
	tail, okTail := c.entryIdentity()
	return ReplicaState{LEO: leo, Committed: committed, Manifest: manifest, TailIdentity: tail},
		okLEO && okCommitted && okManifest && okTail
}

func (c *exchangeCursor) records() ([]ch.Record, bool) {
	count, nilRecords, okCount := c.sliceCount(maxRecoveryProbeIndexes)
	var records []ch.Record
	if !nilRecords {
		records = make([]ch.Record, count)
	}
	for index := range records {
		id, okID := c.uvarint()
		recordIndex, okIndex := c.uvarint()
		epoch, okEpoch := c.uvarint()
		setting, okSetting := c.byte()
		fromUID, okFrom := c.string()
		clientMsgNo, okClient := c.string()
		timestamp, okTimestamp := c.varint()
		syncOnce, okSync := c.boolean()
		payload, okPayload := c.bytes()
		sizeBytes, okSize := c.uvarint()
		if sizeBytes > math.MaxInt {
			okSize = false
		}
		records[index] = ch.Record{
			ID: id, Index: recordIndex, Epoch: epoch, Setting: setting, FromUID: fromUID, ClientMsgNo: clientMsgNo,
			ServerTimestampMS: timestamp, SyncOnce: syncOnce, Payload: payload, SizeBytes: int(sizeBytes),
		}
		okCount = okCount && okID && okIndex && okEpoch && okSetting && okFrom && okClient && okTimestamp && okSync && okPayload && okSize
	}
	return records, okCount
}
