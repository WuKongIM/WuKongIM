package quorumlog_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

func TestPublicationProposalBindsMetadataAndForbidsLossyOldFormats(t *testing.T) {
	metadata, err := hex.DecodeString("01010100000000000003e800016e00016300017400030600017800036f6e65020000003c06000178000374776f")
	if err != nil {
		t.Fatal(err)
	}
	r := quorumlog.Record{ID: 11, Index: 1, Epoch: 3, Setting: 1, FromUID: "sender", ClientMsgNo: "client", ServerTimestampMS: 1000, Expire: 60, Payload: []byte("body"), PublicationMetadata: metadata}
	m := quorumlog.ProposalManifest{Version: quorumlog.PublicationProposalManifestVersion, ChannelEpoch: 3, LeaderTerm: 5, FenceVersion: 7, CommandID: quorumlog.CommandID{1}, LastOffset: 1}
	sealed, entries, ok := quorumlog.SealProposalManifest(m, []quorumlog.Record{r})
	if !ok || !quorumlog.VerifyEntry(entries[0], r) {
		t.Fatal("publication proposal rejected")
	}
	for _, mutate := range []func(*quorumlog.Record){
		func(r *quorumlog.Record) { r.PublicationMetadata[2] = 0 },
		func(r *quorumlog.Record) { r.PublicationMetadata[16] = 'd' },
		func(r *quorumlog.Record) { r.PublicationMetadata[35] = 59 },
		func(r *quorumlog.Record) { r.PublicationMetadata = nil },
		func(r *quorumlog.Record) { r.Expire++ },
		func(r *quorumlog.Record) { r.ServerTimestampMS++ },
		func(r *quorumlog.Record) { r.Payload = []byte("changed") },
	} {
		changed := r
		changed.PublicationMetadata = bytes.Clone(metadata)
		mutate(&changed)
		if quorumlog.VerifyEntry(entries[0], changed) {
			t.Fatal("changed publication verified")
		}
		other, _, ok := quorumlog.SealProposalManifest(m, []quorumlog.Record{changed})
		if !ok || other.Digest == sealed.Digest {
			t.Fatal("publication change not bound")
		}
	}
	for _, oldVersion := range []uint16{1, 2} {
		m.Version = oldVersion
		if _, _, ok := quorumlog.SealProposalManifest(m, []quorumlog.Record{r}); ok {
			t.Fatal("old format silently omitted metadata")
		}
		plain := r
		plain.PublicationMetadata = nil
		_, oldEntries, ok := quorumlog.SealProposalManifest(m, []quorumlog.Record{plain})
		if !ok || quorumlog.VerifyEntry(oldEntries[0], r) {
			t.Fatal("old identity accepted unbound metadata")
		}
	}
	plain, expiring := r, r
	plain.PublicationMetadata, plain.Expire, expiring.PublicationMetadata = nil, 0, nil
	for _, records := range [][]quorumlog.Record{{r}, {plain, r}, {expiring, r}, {r, expiring}} {
		if quorumlog.VersionForRecords(records) != 3 {
			t.Fatal("new proposal chose a lossy format")
		}
	}
	if quorumlog.VersionForRecords([]quorumlog.Record{plain}) != 1 || quorumlog.VersionForRecords([]quorumlog.Record{plain, expiring}) != 2 || !quorumlog.SupportedProposalVersion(3) || quorumlog.SupportedProposalVersion(5) {
		t.Fatal("version selection changed native behavior")
	}
}
