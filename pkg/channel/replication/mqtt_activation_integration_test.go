//go:build integration

package replication

import (
	"context"
	"errors"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

type mqttActivationWireLink struct{ runtimeTestLink }

func (l mqttActivationWireLink) Exchange(ctx context.Context, target ch.NodeID, batch ExchangeBatch) (ExchangeBatchResult, error) {
	b, err := EncodeExchangeBatch(batch)
	if err != nil {
		return ExchangeBatchResult{}, err
	}
	decoded, err := DecodeExchangeBatch(b)
	if err != nil {
		return ExchangeBatchResult{}, err
	}
	r, err := l.runtimeTestLink.Exchange(ctx, target, decoded)
	if err != nil {
		return r, err
	}
	b, err = EncodeExchangeBatchResult(r)
	if err != nil {
		return ExchangeBatchResult{}, err
	}
	return DecodeExchangeBatchResult(b)
}

func TestMQTTSourceActivationQuorumRestartRecoveryAndLearner(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	router := &runtimeTestRouter{servers: make(map[ch.NodeID]*ExchangeServer)}
	paths := map[ch.NodeID]string{}
	runtimes := map[ch.NodeID]*Runtime{}
	factories := map[ch.NodeID]*channelstore.MessageDBFactory{}
	stores := map[ch.NodeID]ReplicaStore{}
	open := func() {
		for _, node := range []ch.NodeID{1, 2, 3, 4} {
			if paths[node] == "" {
				paths[node] = t.TempDir()
			}
			factory := channelstore.NewMessageDBFactory(paths[node])
			store, err := NewStoreAdapter(StoreAdapterConfig{Factory: factory, MaxBatchItems: 64, MaxBatchBytes: 4 << 20})
			if err != nil {
				t.Fatal(err)
			}
			runtime, err := NewRuntime(RuntimeConfig{LocalNode: node, Store: store, Link: mqttActivationWireLink{runtimeTestLink{from: node, router: router}}, Goroutines: goruntimeregistry.New()})
			if err != nil {
				t.Fatal(err)
			}
			factories[node], stores[node], runtimes[node] = factory, store, runtime
			router.register(node, runtime.ExchangeServer())
		}
	}
	closeAll := func() {
		closeCtx, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		for node, r := range runtimes {
			if err := r.Close(closeCtx); err != nil {
				t.Errorf("close runtime: %v", err)
			}
			delete(runtimes, node)
		}
		for node, f := range factories {
			if err := f.Close(); err != nil {
				t.Errorf("close store: %v", err)
			}
			delete(factories, node)
		}
	}
	t.Cleanup(closeAll)
	open()
	authority := Authority{Key: "1:mqtt-activation", ChannelID: ch.ChannelID{ID: "mqtt-activation", Type: 1},
		ID: AuthorityID{ChannelEpoch: 1, LeaderTerm: 1, FenceVersion: 1}, Leader: 1, Voters: []ch.NodeID{1, 2, 3}, Learners: []ch.NodeID{4}, WriteQuorum: 2}
	if _, err := runtimes[1].Log().Install(ctx, authority); err != nil {
		t.Fatal(err)
	}
	activation := func(command byte) Proposal {
		return Proposal{Key: authority.Key, Expected: authority.ID, CommandID: ch.CommandID{command}, MQTTSourceActivation: true,
			Records: []ch.Record{{ID: uint64(command) + 100, Epoch: 1, ServerTimestampMS: int64(command) + 1000, SyncOnce: true, Payload: []byte(quorumlog.MQTTSourceActivationPayload), SizeBytes: len(quorumlog.MQTTSourceActivationPayload)}}}
	}
	first := activation(1)
	receipt, err := runtimes[1].Log().Commit(ctx, first)
	if err != nil || receipt.HW != 1 {
		t.Fatalf("quorum activation: %+v %v", receipt, err)
	}
	for _, node := range []ch.NodeID{1, 2, 3, 4} {
		waitForRuntimeReplicaLEO(t, stores[node], authority, 1)
	}
	wrong := first
	wrong.MQTTSourceActivation = false
	if _, err := runtimes[1].Log().Commit(ctx, wrong); !errors.Is(err, ch.ErrLogConflict) {
		t.Fatalf("changed control intent retry: %v", err)
	}
	business := Proposal{Key: authority.Key, Expected: authority.ID, CommandID: ch.CommandID{2}, Records: []ch.Record{{ID: 200, Epoch: 1, ServerTimestampMS: 2000, FromUID: "sender", Payload: []byte("message"), SizeBytes: 7}}}
	if _, err := runtimes[1].Log().Commit(ctx, business); err != nil {
		t.Fatal(err)
	}
	for _, node := range []ch.NodeID{1, 2, 3, 4} {
		waitForRuntimeReplicaLEO(t, stores[node], authority, 2)
	}
	checkProtected := func(node ch.NodeID, hw uint64) {
		t.Helper()
		s, err := factories[node].ChannelStore(authority.Key, authority.ChannelID)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if err := s.StoreCheckpoint(ctx, ch.Checkpoint{HW: hw}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AdoptRetentionBoundary(ctx, hw, "committed"); err != nil {
			t.Fatal(err)
		}
		trim, err := s.TrimMessagesThrough(ctx, hw, channelstore.RetentionTrimOptions{MaxMessages: 16, MaxBytes: 4096})
		if err != nil || trim.Deleted != 0 {
			t.Fatalf("node %d lost source fence: %+v %v", node, trim, err)
		}
	}
	for _, node := range []ch.NodeID{1, 2, 3, 4} {
		checkProtected(node, 2)
	}
	closeAll()
	open()
	authority.Leader = 2
	authority.ID.LeaderTerm++
	installed, err := runtimes[2].Log().Install(ctx, authority)
	if err != nil || installed.HW < 2 {
		t.Fatalf("recovered authority: %+v %v", installed, err)
	}
	receipt, err = runtimes[2].Log().Commit(ctx, activation(3))
	if err != nil {
		t.Fatal(err)
	}
	checkProtected(2, receipt.HW)
	for _, node := range []ch.NodeID{1, 3, 4} {
		router.register(node, nil)
	}
	noQuorum, done := context.WithTimeout(ctx, 100*time.Millisecond)
	defer done()
	if r, err := runtimes[2].Log().Commit(noQuorum, activation(4)); err == nil || r.HW != 0 {
		t.Fatalf("local persistence claimed quorum: %+v %v", r, err)
	}
	t.Log("mqtt_source_activation_evidence: replicas=3 learner=1 durable_storage=true wire_codec=true restart=true authority_recovery=true protected_trim=true no_quorum_rejected=true product_admission=false")
}
