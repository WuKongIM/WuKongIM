package replication

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

func TestMQTTActivationRejectsStorageWithoutProtectionCapability(t *testing.T) {
	s, err := NewStoreAdapter(StoreAdapterConfig{Factory: channelstore.NewMemoryFactory(), MaxBatchItems: 4, MaxBatchBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	r := ch.Record{ID: 1, Epoch: 1, ServerTimestampMS: 1000, SyncOnce: true, Payload: []byte(quorumlog.MQTTSourceActivationPayload), SizeBytes: len(quorumlog.MQTTSourceActivationPayload)}
	m, _, ok := ch.SealProposalManifest(ch.ProposalManifest{Version: quorumlog.MQTTSourceProposalManifestVersion, ChannelEpoch: 1, LeaderTerm: 1, FenceVersion: 1, CommandID: ch.CommandID{1}, LastOffset: 1}, []ch.Record{r})
	if !ok {
		t.Fatal("seal")
	}
	result := s.Sync(context.Background(), []Mutation{{ChannelKey: "1:source", ChannelID: ch.ChannelID{ID: "source", Type: 1}, Manifest: m, Records: []ch.Record{r}}})
	if len(result) != 1 || result[0].Err == nil || result[0].Outcome.Durable() {
		t.Fatalf("unsupported store acknowledged activation: %+v", result)
	}
}
