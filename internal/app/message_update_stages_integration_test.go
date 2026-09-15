//go:build integration

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	accessapi "github.com/WuKongIM/WuKongIM/internal/access/api"
	deliveryinfra "github.com/WuKongIM/WuKongIM/internal/infra/delivery"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// dispatchTimings measures wall time at existing ports without replacing their
// authority, storage, delivery or CAS behavior. It is test-only instrumentation.
type dispatchTimings struct {
	mu      sync.Mutex
	count   map[string]int
	elapsed map[string]time.Duration
}

func (m *dispatchTimings) observe(name string, start time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.count[name]++
	m.elapsed[name] += time.Since(start)
}

type timedUpdateStore struct {
	message.UpdateStore
	m *dispatchTimings
}

func (s timedUpdateStore) ReadMessageUpdatesBatch(ctx context.Context, q []metadb.MessageUpdateRead) ([]metadb.MessageUpdatePage, error) {
	defer s.m.observe("pending_read", time.Now())
	return s.UpdateStore.ReadMessageUpdatesBatch(ctx, q)
}
func (s timedUpdateStore) GetChannelRuntimeMeta(ctx context.Context, id string, typ int64) (metadb.ChannelRuntimeMeta, error) {
	defer s.m.observe("runtime_read", time.Now())
	return s.UpdateStore.GetChannelRuntimeMeta(ctx, id, typ)
}
func (s timedUpdateStore) ApplyMessageUpdate(ctx context.Context, q metadb.MessageUpdateMutation) (metadb.MessageUpdateMutationResult, error) {
	defer s.m.observe("progress_commit", time.Now())
	return s.UpdateStore.ApplyMessageUpdate(ctx, q)
}

type timedSubscribers struct {
	message.UpdateSubscribers
	m *dispatchTimings
}

func (s timedSubscribers) ListChannelSubscribersAuthoritative(ctx context.Context, id string, typ int64, after string, limit int) ([]string, string, bool, error) {
	defer s.m.observe("subscribers", time.Now())
	return s.UpdateSubscribers.ListChannelSubscribersAuthoritative(ctx, id, typ, after, limit)
}

type timedUpdateHints struct {
	message.UpdateHintSender
	m *dispatchTimings
}

func (s timedUpdateHints) SendMessageUpdateHint(ctx context.Context, uids []string, hint message.MessageUpdateHint) error {
	defer s.m.observe("presence_and_hint", time.Now())
	return s.UpdateHintSender.SendMessageUpdateHint(ctx, uids, hint)
}

func TestMessageUpdateDispatchStages(t *testing.T) {
	if os.Getenv("WK_MESSAGE_UPDATE_STAGES") != "1" {
		t.Skip("explicit bounded stage diagnostic")
	}
	voters := make([]cluster.ControlVoter, 3)
	for i := range voters {
		voters[i] = cluster.ControlVoter{NodeID: uint64(i + 1), Addr: freeSendackSmokeTCPAddr(t)}
	}
	var apps []*App
	var nodes []*cluster.Node
	// Register last so all nodes stop before configuration-owned directories close.
	defer t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		done := make(chan error, len(apps))
		for _, a := range apps {
			go func() { done <- a.Stop(ctx) }()
		}
		for range apps {
			if err := <-done; err != nil {
				t.Error(err)
			}
		}
	})
	for _, voter := range voters {
		cfg := singleNodeClusterAppConfig(t)
		cfg.NodeID, cfg.Cluster.NodeID, cfg.Cluster.ListenAddr = voter.NodeID, voter.NodeID, voter.Addr
		cfg.Cluster.Control.Voters = voters
		cfg.Cluster.Slots.HashSlotCount, cfg.Cluster.Slots.InitialSlotCount = 256, 8
		cfg.Cluster.Slots.ReplicaCount, cfg.Cluster.Channel.ReplicaCount = 3, 3
		cfg.API.ListenAddr = "127.0.0.1:0"
		a, err := New(cfg, WithLogger(wklog.NewNop()))
		if err != nil {
			t.Fatal(err)
		}
		apps = append(apps, a)
		nodes = append(nodes, a.cluster.(*cluster.Node))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	done := make(chan error, len(apps))
	for _, a := range apps {
		go func() { done <- a.Start(ctx) }()
	}
	for range apps {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	waitAppClusterSnapshotsConverge(t, nodes)
	for i, n := range nodes {
		waitSingleNodeClusterNodeSchedulable(t, n, uint64(i+1))
	}

	for _, a := range apps {
		if e := a.messageUpdateWorker.Stop(ctx); e != nil {
			t.Fatal(e)
		}
	}
	ctx, cancel = context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	h := apps[0].api.(*accessapi.Server).Handler()
	postAppJSON(t, h, "/channel", `{"channel_id":"stage-group","channel_type":2,"subscribers":["stage-author"]}`, http.StatusOK)
	// Only the author needs a UID membership for this notification-port test.
	uids := make([]string, 1023)
	for i := range uids {
		uids[i] = fmt.Sprintf("stage-member-%04d", i)
	}
	for start := 0; start < len(uids); start += 1000 {
		if _, e := nodes[0].AddChannelSubscribersCounted(ctx, "stage-group", 2, uids[start:min(start+1000, len(uids))], 2); e != nil {
			t.Fatal(e)
		}
	}
	var tasks []metadb.MessageUpdate
	for i := 0; i < 33; i++ {
		id, kind := "stage-reader", 1
		if i == 32 {
			id, kind = "stage-group", 2
		}
		reply := postAppJSON(t, h, "/message/send", fmt.Sprintf(`{"from_uid":"stage-author","channel_id":"%s","channel_type":%d,"client_msg_no":"stage-%d","payload":"b2xk"}`, id, kind, i), http.StatusOK)
		var sent struct {
			ID  uint64 `json:"message_id"`
			Seq uint64 `json:"message_seq"`
		}
		if e := json.Unmarshal(reply, &sent); e != nil || sent.ID == 0 {
			t.Fatalf("send=%s error=%v", reply, e)
		}
		postAppJSON(t, h, "/message/update", fmt.Sprintf(`{"login_uid":"stage-author","channel_id":"%s","channel_type":%d,"message_id":"%d","expected_content_epoch":"0","expected_version":"0","request_id":"stage-%d","payload":"bmV3"}`, id, kind, sent.ID, i), http.StatusOK)
		if kind == 1 {
			id = channelid.EncodePersonChannel("stage-author", id)
		}
		tasks = append(tasks, metadb.MessageUpdate{ChannelID: id, ChannelType: int64(kind), MessageID: sent.ID, MessageSeq: sent.Seq, Version: 1})
	}
	m := &dispatchTimings{count: map[string]int{}, elapsed: map[string]time.Duration{}}
	hints := &deliveryinfra.MessageUpdateHints{Online: apps[0].online, Presence: apps[0].presence, Peers: nodes[0], NodeID: apps[0].cfg.NodeID}
	// A new usecase with the same real notification ports permits timing without
	// changing the live edit API's dependencies or starting another worker.
	d := message.New(message.Options{Updates: timedUpdateStore{nodes[0], m}, UpdateSubscribers: timedSubscribers{nodes[0], m}, UpdateHints: timedUpdateHints{hints, m}})
	started := time.Now()
	for _, task := range tasks {
		for {
			more, e := d.DispatchMessageUpdate(ctx, task)
			if e != nil {
				t.Fatal(e)
			}
			if !more {
				break
			}
		}
	}
	report := map[string]any{"seconds": time.Since(started).Seconds(), "messages": len(tasks)}
	for name, count := range m.count {
		report[name] = map[string]any{"count": count, "total_ms": float64(m.elapsed[name]) / float64(time.Millisecond), "mean_ms": float64(m.elapsed[name]) / float64(time.Millisecond) / float64(count)}
	}
	b, _ := json.MarshalIndent(report, "", "  ")
	t.Log(string(b))
	if path := os.Getenv("WK_MESSAGE_UPDATE_STAGES_REPORT"); path != "" {
		if e := os.WriteFile(path, append(b, '\n'), 0644); e != nil {
			t.Fatal(e)
		}
	}
}
