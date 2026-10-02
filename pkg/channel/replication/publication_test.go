package replication_test

import (
	"bytes"
	"encoding/hex"
	"reflect"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/replication"
)

func publicationRequest(t *testing.T, metadata []byte) replication.ReplicateRequest {
	t.Helper()
	r := ch.Record{ID: 11, Index: 1, Epoch: 3, FromUID: "sender", ClientMsgNo: "client", ServerTimestampMS: 2000, Payload: []byte("body"), PublicationMetadata: metadata, SizeBytes: 4 + len(metadata)}
	mf, _, ok := ch.SealProposalManifest(ch.ProposalManifest{Version: 3, ChannelEpoch: 3, LeaderTerm: 5, FenceVersion: 7, CommandID: ch.CommandID{1}, LastOffset: 1}, []ch.Record{r})
	if !ok {
		t.Fatal("seal failed")
	}
	return replication.ReplicateRequest{ChannelKey: "2:publication", ChannelID: ch.ChannelID{ID: "publication", Type: 2}, Leader: 1, Follower: 2, Manifest: mf, Records: []ch.Record{r}}
}

func TestPublicationExchangeCarriesOwnedMetadataAndRejectsOldVersion(t *testing.T) {
	metadata, err := hex.DecodeString("01010100000000000003e800016e00016300017400030600017800036f6e65020000003c06000178000374776f")
	if err != nil {
		t.Fatal(err)
	}
	r := publicationRequest(t, metadata)
	batch := replication.ExchangeBatch{Version: replication.ExchangeVersion, Items: []replication.ExchangeItem{{RequestID: 1, Kind: replication.ExchangeReplicate, Replicate: &r}}}
	data, err := replication.EncodeExchangeBatch(batch)
	if err != nil {
		t.Fatal(err)
	}
	got, err := replication.DecodeExchangeBatch(data)
	if err != nil || !reflect.DeepEqual(got, batch) {
		t.Fatalf("publication request lost fields: %#v %v", got, err)
	}
	clear(data)
	if !bytes.Equal(got.Items[0].Replicate.Records[0].PublicationMetadata, metadata) {
		t.Fatal("request decoder borrowed metadata")
	}
	result := replication.ExchangeBatchResult{Version: replication.ExchangeVersion, Items: []replication.ExchangeItemResult{{RequestID: 1, Fetch: replication.FetchResult{Proposals: []replication.RecoveryProposal{{Manifest: r.Manifest, Records: r.Records}}}}}}
	data, err = replication.EncodeExchangeBatchResult(result)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := replication.DecodeExchangeBatchResult(data)
	if err != nil || !reflect.DeepEqual(decoded, result) {
		t.Fatalf("recovery response lost fields: %#v %v", decoded, err)
	}
	for n := 0; n < len(data); n++ {
		if _, err := replication.DecodeExchangeBatchResult(data[:n]); err == nil {
			t.Fatalf("partial response %d accepted", n)
		}
	}
	old := bytes.Clone(data)
	old[0] = byte(replication.ExchangeVersion - 1)
	if _, err := replication.DecodeExchangeBatchResult(old); err == nil {
		t.Fatal("lossy exchange version accepted")
	}
	batch.Version = replication.ExchangeVersion - 1
	if _, err := replication.EncodeExchangeBatch(batch); err == nil {
		t.Fatal("publication encoded for an older peer")
	}
	clear(data)
	if !bytes.Equal(decoded.Items[0].Fetch.Proposals[0].Records[0].PublicationMetadata, metadata) {
		t.Fatal("response decoder borrowed metadata")
	}
}

func TestPublicationExchangeRejectsInvalidMetadataAndUnderstatedSize(t *testing.T) {
	for _, bad := range [][]byte{{1}, {2}, make([]byte, 32769)} {
		r := publicationRequest(t, bad)
		if r.Valid() {
			t.Fatal("a digest does not make malformed publication metadata valid")
		}
		result := replication.ExchangeBatchResult{Version: replication.ExchangeVersion, Items: []replication.ExchangeItemResult{{RequestID: 1, Fetch: replication.FetchResult{Proposals: []replication.RecoveryProposal{{Manifest: r.Manifest, Records: r.Records}}}}}}
		if _, err := replication.EncodeExchangeBatchResult(result); err == nil {
			t.Fatal("malformed metadata emitted in a recovery response")
		}
	}
	metadata, _ := hex.DecodeString("01010100000000000003e800016e00016300017400030600017800036f6e65020000003c06000178000374776f")
	r := publicationRequest(t, metadata)
	r.Records[0].SizeBytes = 4
	if r.Valid() {
		t.Fatal("publication bytes bypassed the declared content size")
	}
}
