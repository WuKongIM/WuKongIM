package replication

import (
	"reflect"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

func TestExchangeCodecRoundTripsEveryRequestAndResultKind(t *testing.T) {
	t.Parallel()

	first := testReplicateRequest(t, "1:codec", "codec", 7, []byte("first"))
	manifest, records := first.Manifest, first.Records
	_, firstEntries, ok := ch.SealProposalManifest(manifest, records)
	if !ok {
		t.Fatal("SealProposalManifest(first) failed")
	}
	state := ReplicaState{LEO: 1, Manifest: manifest, TailIdentity: firstEntries[0]}
	previous := state.TailIdentity
	secondRecords := []ch.Record{{
		ID: 8, Epoch: 3, FromUID: "sender", ClientMsgNo: "second", ServerTimestampMS: 2,
		Payload: []byte("second"), SizeBytes: len("second"),
	}}
	secondManifest, _, ok := ch.SealProposalManifest(ch.ProposalManifest{
		Version: ch.ProposalManifestVersion, ChannelEpoch: 3, LeaderTerm: 5, FenceVersion: 7,
		CommandID: ch.CommandID{8}, BaseOffset: 1, LastOffset: 2,
		PreviousTerm: previous.LeaderTerm, PreviousIndex: previous.Index, PreviousDigest: previous.Digest,
	}, secondRecords)
	if !ok {
		t.Fatal("SealProposalManifest(second) failed")
	}
	batch := ExchangeBatch{Version: ExchangeVersion, Items: []ExchangeItem{
		{RequestID: 1, Kind: ExchangeReplicate, Replicate: &ReplicateRequest{
			ChannelKey: "1:codec", ChannelID: ch.ChannelID{ID: "codec", Type: 1}, Leader: 1, Follower: 2,
			Manifest: manifest, Records: records,
		}},
		{RequestID: 2, Kind: ExchangeProbe, Probe: &ProbeRequest{
			ChannelKey: "1:codec", ChannelID: ch.ChannelID{ID: "codec", Type: 1}, Leader: 1, Follower: 2,
			Indexes: []uint64{1, 2},
		}},
		{RequestID: 3, Kind: ExchangeFetch, Fetch: &FetchRequest{
			ChannelKey: "1:codec", ChannelID: ch.ChannelID{ID: "codec", Type: 1}, Leader: 1, Follower: 2,
			Expected: state, From: 1, Through: 1, MaxBytes: 4096,
		}},
	}}
	encoded, err := EncodeExchangeBatch(batch)
	if err != nil {
		t.Fatalf("EncodeExchangeBatch() error = %v", err)
	}
	decoded, err := DecodeExchangeBatch(encoded)
	if err != nil {
		t.Fatalf("DecodeExchangeBatch() error = %v", err)
	}
	if !reflect.DeepEqual(decoded, batch) {
		t.Fatalf("DecodeExchangeBatch() = %#v, want %#v", decoded, batch)
	}

	result := ExchangeBatchResult{Version: ExchangeVersion, Items: []ExchangeItemResult{
		{RequestID: 1, Replicate: ReplicateResult{Status: ReplicateDurable, LastOffset: 1, Proof: replicateProofFor(*batch.Items[0].Replicate)}},
		{RequestID: 2, Probe: ProbeResult{
			Proof: probeProofFor(*batch.Items[1].Probe), State: state,
			Entries: []EntryProbe{{Index: 1, Present: true, Identity: previous}, {Index: 2}},
		}},
		{RequestID: 3, Fetch: FetchResult{
			Proof: fetchProofFor(*batch.Items[2].Fetch), State: state,
			Proposals: []RecoveryProposal{{Manifest: secondManifest, Records: secondRecords}},
		}},
	}}
	encodedResult, err := EncodeExchangeBatchResult(result)
	if err != nil {
		t.Fatalf("EncodeExchangeBatchResult() error = %v", err)
	}
	decodedResult, err := DecodeExchangeBatchResult(encodedResult)
	if err != nil {
		t.Fatalf("DecodeExchangeBatchResult() error = %v", err)
	}
	if !reflect.DeepEqual(decodedResult, result) {
		t.Fatalf("DecodeExchangeBatchResult() = %#v, want %#v", decodedResult, result)
	}
}

func TestExchangeCodecRejectsOversizedCountsBeforeAllocation(t *testing.T) {
	t.Parallel()

	tooManyItems := appendCodecUvarint(nil, uint64(ExchangeVersion))
	tooManyItems = appendCodecUvarint(tooManyItems, MaxExchangeBatchItems+1)
	if _, err := DecodeExchangeBatch(tooManyItems); err == nil {
		t.Fatal("DecodeExchangeBatch(too many items) error = nil")
	}

	request := testReplicateRequest(t, "1:codec-bounds", "codec-bounds", 9, []byte("payload"))
	manifest, records := request.Manifest, request.Records
	batch := ExchangeBatch{Version: ExchangeVersion, Items: []ExchangeItem{{
		RequestID: 1, Kind: ExchangeReplicate, Replicate: &ReplicateRequest{
			ChannelKey: "1:codec-bounds", ChannelID: ch.ChannelID{ID: "codec-bounds", Type: 1},
			Leader: 1, Follower: 2, Manifest: manifest, Records: records,
		},
	}}}
	encoded, err := EncodeExchangeBatch(batch)
	if err != nil {
		t.Fatalf("EncodeExchangeBatch() error = %v", err)
	}
	encoded = append(encoded, make([]byte, MaxExchangeBatchBytes-len(encoded)+1)...)
	if _, err := DecodeExchangeBatch(encoded); err == nil {
		t.Fatal("DecodeExchangeBatch(oversized bytes) error = nil")
	}
}
