//go:build integration

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sort"
	"sync"
	"testing"
	"time"

	accessapi "github.com/WuKongIM/WuKongIM/internal/access/api"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

// TestMessageUpdateReadComparison uses only pre-existing HTTP contracts so the
// exact fixture can run on the baseline. Measurements include node background
// work; they are bounded regression evidence, not production capacity claims.
func TestMessageUpdateReadComparison(t *testing.T) {
	if os.Getenv("WK_MESSAGE_UPDATE_PERF") != "1" {
		t.Skip("explicit performance comparison only")
	}
	cfg := singleNodeClusterAppConfig(t)
	cfg.Cluster.Slots.HashSlotCount = 256
	cfg.Cluster.Slots.InitialSlotCount = 8
	cfg.API.ListenAddr = "127.0.0.1:0"
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := a.Stop(ctx); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err = a.Start(ctx); err != nil {
		t.Fatal(err)
	}
	n := a.cluster.(*cluster.Node)
	waitSingleNodeClusterNodeSchedulable(t, n, cfg.NodeID)
	h := a.api.(*accessapi.Server).Handler()
	targetIDs := make([]uint64, 32)
	for i := 0; i < 32; i++ {
		uid := fmt.Sprintf("perf-%02d", i)
		for j := 0; j < 4; j++ {
			payload := bytes.Repeat([]byte("x"), 1024)
			if i == 0 {
				payload = bytes.Repeat([]byte("L"), 128<<10)
			}
			body, _ := json.Marshal(map[string]any{"from_uid": uid, "channel_id": "viewer", "channel_type": 1, "client_msg_no": fmt.Sprintf("fixture-%d-%d", i, j), "payload": payload, "header": map[string]int{"red_dot": 1}})
			response := postAppJSON(t, h, "/message/send", string(body), http.StatusOK)
			var sent struct {
				MessageID uint64 `json:"message_id"`
			}
			if err = json.Unmarshal(response, &sent); err != nil || sent.MessageID == 0 {
				t.Fatalf("seed send channel=%d response=%s err=%v", i, response, err)
			}
			targetIDs[i] = sent.MessageID
		}
		for {
			_, found, e := n.GetUserChannelMembership(ctx, "viewer", channelid.EncodePersonChannel(uid, "viewer"), 1)
			if e != nil {
				t.Fatalf("membership channel=%d: %v", i, e)
			}
			if found {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	deltaCursor := ""
	if os.Getenv("WK_MESSAGE_UPDATE_PERF_EDIT") == "1" {
		raw := postAppJSON(t, h, "/channel/messageupdates", `{"login_uid":"viewer","channel_id":"perf-01","channel_type":1}`, http.StatusOK)
		var page struct {
			Cursor string `json:"next_update_cursor"`
		}
		if err = json.Unmarshal(raw, &page); err != nil {
			t.Fatal(err)
		}
		deltaCursor = page.Cursor
		for i, id := range targetIDs {
			body, _ := json.Marshal(map[string]any{"login_uid": fmt.Sprintf("perf-%02d", i), "channel_id": "viewer", "channel_type": 1, "message_id": fmt.Sprint(id), "expected_version": "0", "expected_content_epoch": "0", "request_id": fmt.Sprintf("edit-%d", i), "payload": bytes.Repeat([]byte("E"), 128<<10)})
			postAppJSON(t, h, "/message/update", string(body), http.StatusOK)
		}
	}
	cases := []struct{ name, path, body string }{
		{"history_small", "/channel/messagesync", `{"login_uid":"viewer","channel_id":"perf-01","channel_type":1,"limit":50}`},
		{"history_large", "/channel/messagesync", `{"login_uid":"viewer","channel_id":"perf-00","channel_type":1,"limit":50}`},
		{"conversation_list_32", "/conversation/list", `{"uid":"viewer","limit":32}`},
		{"conversation_sync_32", "/conversation/sync", `{"uid":"viewer","msg_count":1}`},
	}
	if deltaCursor != "" {
		changed := fmt.Sprintf(`{"login_uid":"viewer","channel_id":"perf-01","channel_type":1,"update_cursor":%q}`, deltaCursor)
		cases = append(cases, struct{ name, path, body string }{"delta_changed", "/channel/messageupdates", changed})
		raw := postAppJSON(t, h, "/channel/messageupdates", changed, http.StatusOK)
		var page struct {
			Cursor string `json:"next_update_cursor"`
		}
		if err = json.Unmarshal(raw, &page); err != nil {
			t.Fatal(err)
		}
		cases = append(cases, struct{ name, path, body string }{"delta_empty", "/channel/messageupdates", fmt.Sprintf(`{"login_uid":"viewer","channel_id":"perf-01","channel_type":1,"update_cursor":%q}`, page.Cursor)})
	}
	commitIndex := func() uint64 {
		var total uint64
		for slot := uint32(1); slot <= 8; slot++ {
			s, e := n.LocalSlotRaftStatus(context.Background(), slot)
			if e != nil {
				t.Fatal(e)
			}
			total += s.CommitIndex
		}
		return total
	}
	for _, tc := range cases {
		for _, concurrency := range []int{1, 8} {
			for i := 0; i < 4; i++ {
				postAppJSON(t, h, tc.path, tc.body, http.StatusOK)
			}
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			commits := commitIndex()
			const requests = 64
			samples := make([]time.Duration, requests)
			statuses := make([]int, requests)
			var wg sync.WaitGroup
			start := time.Now()
			for worker := 0; worker < concurrency; worker++ {
				wg.Add(1)
				go func(worker int) {
					defer wg.Done()
					for i := worker; i < requests; i += concurrency {
						began := time.Now()
						rec := httptest.NewRecorder()
						req := httptest.NewRequest(http.MethodPost, tc.path, bytes.NewBufferString(tc.body))
						req.Header.Set("Content-Type", "application/json")
						h.ServeHTTP(rec, req)
						samples[i] = time.Since(began)
						statuses[i] = rec.Code
					}
				}(worker)
			}
			wg.Wait()
			elapsed := time.Since(start)
			runtime.ReadMemStats(&after)
			commitDelta := commitIndex() - commits
			errors := 0
			for _, status := range statuses {
				if status != http.StatusOK {
					errors++
				}
			}
			sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
			t.Logf("PERF scenario=%s concurrency=%d requests=%d errors=%d rps=%.1f p50_ms=%.2f p95_ms=%.2f p99_ms=%.2f bytes_per_req=%d allocs_per_req=%d raft_commits=%d", tc.name, concurrency, requests, errors, float64(requests)/elapsed.Seconds(), float64(samples[32])/float64(time.Millisecond), float64(samples[60])/float64(time.Millisecond), float64(samples[63])/float64(time.Millisecond), (after.TotalAlloc-before.TotalAlloc)/requests, (after.Mallocs-before.Mallocs)/requests, commitDelta)
			if errors > 0 {
				t.Fatalf("%s concurrency%d: statuses=%v", tc.name, concurrency, statuses)
			}
		}
	}
}
