//go:build e2e

package first_person_history

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

type observation struct {
	At       time.Time       `json:"at"`
	Finished time.Time       `json:"finished"`
	Phase    string          `json:"phase"`
	Path     string          `json:"path"`
	Request  any             `json:"request"`
	Response json.RawMessage `json:"response,omitempty"`
	Error    string          `json:"error,omitempty"`
}

type pair struct {
	A   string
	B   string
	Key string
	Ack suite.MessageSendResponse
}

type recent struct {
	MessageID   int64  `json:"message_id"`
	MessageSeq  uint64 `json:"message_seq"`
	ClientMsgNo string `json:"client_msg_no"`
}

func TestFirstPersonHTTPHistory(t *testing.T) {
	pairCount := 1
	if value := os.Getenv("WK_E2E_1066_PAIRS"); value != "" {
		var err error
		pairCount, err = strconv.Atoi(value)
		require.NoError(t, err)
		require.GreaterOrEqual(t, pairCount, 1)
		require.LessOrEqual(t, pairCount, 128)
	}
	nodes := 1
	if os.Getenv("WK_E2E_1066_NODES") == "3" {
		nodes = 3
	}
	crashAfterACK := os.Getenv("WK_E2E_1066_CRASH_AFTER_ACK") == "1"
	if crashAfterACK {
		require.Equal(t, 1, nodes)
		require.Equal(t, 1, pairCount)
	}
	options := []suite.Option{suite.WithManagerHTTP()}
	if os.Getenv("WK_E2E_1066_PROFILE") != "" {
		options = append(options, suite.WithNodeConfigOverrides(1, map[string]string{"WK_DEBUG_API_ENABLE": "true"}))
	}
	for id := 1; id <= nodes; id++ {
		options = append(options, suite.WithNodeConfigOverrides(uint64(id), map[string]string{
			"WK_GATEWAY_TOKEN_AUTH_ON": "false", "WK_CLUSTER_HASH_SLOT_COUNT": "256",
		}))
	}
	cluster := suite.New(t).StartStaticCluster(nodes, options...)
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(cluster.DumpDiagnostics())
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())
	_, err := cluster.WaitSlotLeadersStable(ctx, time.Second)
	require.NoError(t, err, cluster.DumpDiagnostics())

	var mu sync.Mutex
	report := struct {
		Hot                     any           `json:"hot,omitempty"`
		Nodes                   int           `json:"nodes"`
		Pairs                   int           `json:"pairs"`
		ImmediateUnavailable    int           `json:"immediate_unavailable"`
		ImmediateMissing        int           `json:"immediate_missing"`
		ImmediateMissingQueries int           `json:"immediate_missing_queries"`
		Converged               int           `json:"converged"`
		AfterRestart            int           `json:"after_restart"`
		CrashAfterACK           bool          `json:"crash_after_ack"`
		MetricsBefore           any           `json:"metrics_before"`
		MetricsAfter            any           `json:"metrics_after"`
		Observations            []observation `json:"observations"`
		DroppedObservations     int           `json:"dropped_observations"`
	}{Nodes: nodes, Pairs: pairCount, CrashAfterACK: crashAfterACK}
	defer func() {
		path := os.Getenv("WK_E2E_1066_REPORT")
		if path == "" {
			path = filepath.Join(t.TempDir(), "report.json")
		}
		data, marshalErr := json.MarshalIndent(report, "", "  ")
		require.NoError(t, marshalErr)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		require.NoError(t, os.WriteFile(path, data, 0600))
		t.Logf("report=%s pairs=%d immediate_missing=%d missing_queries=%d converged=%d after_restart=%d", path, report.Pairs, report.ImmediateMissing, report.ImmediateMissingQueries, report.Converged, report.AfterRestart)
	}()
	record := func(row observation) {
		mu.Lock()
		defer mu.Unlock()
		if len(report.Observations) >= 8192 {
			report.DroppedObservations++
			return
		}
		report.Observations = append(report.Observations, row)
	}
	query := func(node *suite.StartedNode, phase, path string, request, out any) error {
		at := time.Now().UTC()
		body, queryErr := suite.PostJSON(ctx, "http://"+node.APIAddr()+path, request, out)
		row := observation{At: at, Finished: time.Now().UTC(), Phase: phase, Path: path, Request: request, Response: body}
		if queryErr != nil {
			row.Error = queryErr.Error()
		}
		record(row)
		return queryErr
	}
	check := func(node *suite.StartedNode, phase string, p pair) (int, error) {
		missing := 0
		for _, user := range [][2]string{{p.A, p.B}, {p.B, p.A}} {
			var history struct {
				Messages []recent `json:"messages"`
			}
			if err := query(node, phase, "/channel/messagesync", map[string]any{
				"login_uid": user[0], "channel_id": user[1], "channel_type": 1,
				"start_message_seq": 0, "end_message_seq": 0, "pull_mode": 0, "limit": 20,
			}, &history); err != nil {
				return missing, err
			}
			found := false
			for _, message := range history.Messages {
				if message.ClientMsgNo == p.Key && message.MessageID == p.Ack.MessageID && message.MessageSeq == p.Ack.MessageSeq {
					found = true
				}
			}
			if !found {
				missing++
			}
			var conversations []struct {
				ChannelID string   `json:"channel_id"`
				Recents   []recent `json:"recents"`
			}
			if err := query(node, phase, "/conversation/sync", map[string]any{"uid": user[0], "version": 0, "msg_count": 20}, &conversations); err != nil {
				return missing, err
			}
			found = false
			for _, conversation := range conversations {
				for _, message := range conversation.Recents {
					if conversation.ChannelID == user[1] && message.ClientMsgNo == p.Key && message.MessageID == p.Ack.MessageID && message.MessageSeq == p.Ack.MessageSeq {
						found = true
					}
				}
			}
			if !found {
				missing++
			}
		}
		return missing, nil
	}
	report.MetricsBefore, err = suite.FetchMetricSamples(ctx, cluster.MustNode(1).APIAddr())
	require.NoError(t, err)
	pairs := make([]pair, report.Pairs)
	jobs := make(chan int)
	errors := make(chan error, report.Pairs)
	var workers sync.WaitGroup
	for worker := 0; worker < min(16, pairCount); worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range jobs {
				p := pair{A: fmt.Sprintf("issue1066-a-%03d", i), B: fmt.Sprintf("issue1066-b-%03d", i), Key: fmt.Sprintf("issue1066-first-%03d", i)}
				node := cluster.MustNode(uint64(i%nodes + 1))
				client, e := suite.NewWKProtoClient()
				if e != nil {
					errors <- e
					continue
				}
				if e = client.Connect(cluster.MustNode(uint64((i+1)%nodes+1)).GatewayAddr(), p.B, p.B+"-device"); e != nil {
					errors <- e
					_ = client.Close()
					continue
				}
				e = query(node, "send", "/message/send", map[string]any{
					"from_uid": p.A, "channel_id": p.B, "channel_type": 1, "client_msg_no": p.Key,
					"header": map[string]int{"no_persist": 0, "red_dot": 1, "sync_once": 0}, "payload": base64.StdEncoding.EncodeToString([]byte(p.Key)),
				}, &p.Ack)
				if e == nil && (p.Ack.Reason != uint8(frame.ReasonSuccess) || p.Ack.MessageID == 0 || p.Ack.MessageSeq == 0) {
					e = fmt.Errorf("%s unexpected send result %+v", p.Key, p.Ack)
				}
				if e == nil {
					recv, readErr := client.ReadRecv()
					e = readErr
					if e == nil && (recv.MessageID != p.Ack.MessageID || recv.MessageSeq != p.Ack.MessageSeq || recv.ClientMsgNo != p.Key) {
						e = fmt.Errorf("%s recv mismatch", p.Key)
					}
					if e == nil {
						body, _ := json.Marshal(recv)
						record(observation{At: time.Now().UTC(), Phase: "online_recv", Path: "WKProto RECV", Request: map[string]string{"uid": p.B}, Response: body})
						e = client.RecvAck(recv.MessageID, recv.MessageSeq)
					}
				}
				_ = client.Close()
				if e == nil {
					e = client.Connect(node.GatewayAddr(), p.B, p.B+"-device")
					if e == nil {
						record(observation{At: time.Now().UTC(), Phase: "reconnect", Path: "WKProto CONNECT", Request: map[string]string{"uid": p.B}})
					}
				}
				if e == nil {
					missing, checkErr := check(node, "immediate_after_recv_reconnect", p)
					e = checkErr
					mu.Lock()
					if checkErr != nil && strings.Contains(checkErr.Error(), `returned 503: {"code":"unavailable","msg":"temporarily unavailable","status":503}`) {
						report.ImmediateUnavailable++
						if os.Getenv("WK_E2E_1066_ALLOW_READ_UNAVAILABLE") == "1" {
							e = nil
						}
					}
					if missing > 0 {
						report.ImmediateMissing++
					}
					report.ImmediateMissingQueries += missing
					mu.Unlock()
				}
				_ = client.Close()
				pairs[i] = p
				if e != nil {
					errors <- fmt.Errorf("pair %d: %w", i, e)
				}
			}
		}()
	}
	for i := range pairs {
		jobs <- i
	}
	close(jobs)
	workers.Wait()
	close(errors)
	for e := range errors {
		require.NoError(t, e, cluster.DumpDiagnostics())
	}
	if crashAfterACK {
		// Kill only the exact fixture process, before waiting for directory repair.
		process := cluster.MustNode(1).Process
		require.NoError(t, process.Cmd.Process.Kill())
		record(observation{At: time.Now().UTC(), Phase: "crash_after_ack", Path: "process SIGKILL", Request: map[string]int{"pid": process.Cmd.Process.Pid}})
		select {
		case <-process.Done():
		case <-time.After(5 * time.Second):
			t.Fatal("killed fixture did not exit")
		}
		require.NoError(t, cluster.StartStoppedNode(1))
		require.NoError(t, cluster.WaitClusterReady(ctx))
	}
	for _, p := range pairs {
		require.Eventually(t, func() bool { missing, e := check(cluster.MustNode(1), "eventual", p); return e == nil && missing == 0 }, 15*time.Second, 100*time.Millisecond, p.Key)
		report.Converged++
	}
	report.MetricsAfter, err = suite.FetchMetricSamples(ctx, cluster.MustNode(1).APIAddr())
	require.NoError(t, err)
	for id := 1; id <= nodes; id++ {
		require.NoError(t, cluster.RestartNode(uint64(id)))
	}
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())
	for _, p := range pairs {
		require.Eventually(t, func() bool {
			missing, e := check(cluster.MustNode(1), "after_restart", p)
			return e == nil && missing == 0
		}, 15*time.Second, 100*time.Millisecond, p.Key)
		report.AfterRestart++
	}
	if os.Getenv("WK_E2E_1066_REQUIRE_GAP") == "1" {
		require.Positive(t, report.ImmediateMissing, "no immediate visibility gap observed")
	}
	// HTTP success and durable business success have separate contracts.
	node := cluster.MustNode(1)
	var rejected suite.MessageSendResponse
	require.NoError(t, query(node, "control_missing_group", "/message/send", map[string]any{
		"from_uid": "issue1066-control", "channel_id": "issue1066-absent-group", "channel_type": 2,
		"client_msg_no": "issue1066-rejected", "payload": "aGk=",
	}, &rejected))
	require.Equal(t, uint8(frame.ReasonChannelNotExist), rejected.Reason)
	require.Zero(t, rejected.MessageID)
	require.Zero(t, rejected.MessageSeq)
	var transient suite.MessageSendResponse
	require.NoError(t, query(node, "control_transient", "/message/send", map[string]any{
		"from_uid": "issue1066-transient-a", "channel_id": "issue1066-transient-b", "channel_type": 1,
		"client_msg_no": "issue1066-transient", "payload": "aGk=", "no_persist": 1,
		"header": map[string]int{"no_persist": 0, "sync_once": 0, "red_dot": 1},
	}, &transient))
	require.Equal(t, uint8(frame.ReasonSuccess), transient.Reason)
	require.NotZero(t, transient.MessageID)
	require.Zero(t, transient.MessageSeq)
	missing, err := check(node, "control_transient_history", pair{A: "issue1066-transient-a", B: "issue1066-transient-b", Key: "issue1066-transient", Ack: transient})
	require.NoError(t, err)
	require.Equal(t, 4, missing)
	err = query(node, "control_person_subscriber_add", "/channel/subscriber_add", map[string]any{
		"channel_id": pairs[0].B, "channel_type": 1, "subscribers": []string{pairs[0].A, pairs[0].B},
	}, nil)
	require.ErrorContains(t, err, "个人频道不支持添加订阅者")
	if os.Getenv("WK_E2E_1066_REQUIRE_IMMEDIATE") != "0" && os.Getenv("WK_E2E_1066_REQUIRE_GAP") != "1" {
		require.Zero(t, report.ImmediateMissing, "issue #1066: a durable successful first send was received online but missing from history after reconnect")
	}
	if os.Getenv("WK_E2E_1066_HOT") == "1" {
		// Same public HTTP workload for baseline and candidate binaries. Run only
		// after directory convergence/restart checks so this measures warm channels.
		const concurrency = 16
		total := 4096
		if value := os.Getenv("WK_E2E_1066_HOT_MESSAGES"); value != "" {
			parsed, err := strconv.Atoi(value)
			require.NoError(t, err)
			require.GreaterOrEqual(t, parsed, 4096)
			require.LessOrEqual(t, parsed, 65536)
			total = parsed
		}
		send := func(i int) error {
			p := pairs[i%len(pairs)]
			var ack suite.MessageSendResponse
			body := map[string]any{"from_uid": p.A, "channel_id": p.B, "channel_type": 1, "client_msg_no": fmt.Sprintf("issue1066-hot-%d", i), "payload": "aGk="}
			var err error
			if i < 128 {
				// Recovery warmup may retry only the exact public 503 signal with the
				// same key/body. Timed sends below never retry or hide an error.
				ack, err = suite.PostMessageSendEventually(ctx, cluster.MustNode(1).APIAddr(), body)
			} else {
				_, err = suite.PostJSON(ctx, "http://"+cluster.MustNode(1).APIAddr()+"/message/send", body, &ack)
			}
			if err != nil {
				return err
			}
			if ack.Reason != uint8(frame.ReasonSuccess) || ack.MessageSeq == 0 {
				return fmt.Errorf("hot ack: %+v", ack)
			}
			return nil
		}
		for i := 0; i < 128; i++ {
			require.NoError(t, send(i))
		}
		// Snapshot all fixture nodes outside the measured interval. This keeps
		// remote membership writes and the added fresh-read cost observable.
		metrics := func() []suite.MetricSample {
			var all []suite.MetricSample
			for id := 1; id <= nodes; id++ {
				samples, err := suite.FetchMetricSamples(ctx, cluster.MustNode(uint64(id)).APIAddr())
				require.NoError(t, err)
				all = append(all, samples...)
			}
			return all
		}
		layout := func() []suite.ChannelRuntimeMeta {
			rows := make([]suite.ChannelRuntimeMeta, 0, len(pairs))
			for _, p := range pairs {
				row, err := suite.GetChannelRuntimeMeta(ctx, cluster.MustNode(1), channelid.EncodePersonChannel(p.A, p.B), 1)
				require.NoError(t, err)
				row.MaxMessageSeq = nil // Message progress changes during a healthy run.
				rows = append(rows, row)
			}
			return rows
		}
		layoutBefore := layout()
		before := metrics()
		latencies := make([]float64, total)
		failures := make(chan error, total)
		var wg sync.WaitGroup
		profileDone := make(chan error, 1)
		if path := os.Getenv("WK_E2E_1066_PROFILE"); path != "" {
			go func() {
				req, e := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+cluster.MustNode(1).APIAddr()+"/debug/pprof/profile?seconds=10", nil)
				if e != nil {
					profileDone <- e
					return
				}
				response, e := http.DefaultClient.Do(req)
				if e != nil {
					profileDone <- e
					return
				}
				defer response.Body.Close()
				body, e := io.ReadAll(io.LimitReader(response.Body, 32<<20))
				if e == nil && response.StatusCode != 200 {
					e = fmt.Errorf("profile status %d", response.StatusCode)
				}
				if e == nil {
					e = os.WriteFile(path, body, 0600)
				}
				profileDone <- e
			}()
		} else {
			profileDone <- nil
		}
		started := time.Now()
		for worker := 0; worker < concurrency; worker++ {
			wg.Add(1)
			go func(worker int) {
				defer wg.Done()
				for i := worker; i < total; i += concurrency {
					at := time.Now()
					err := send(128 + i)
					latencies[i] = float64(time.Since(at)) / float64(time.Millisecond)
					if err != nil {
						failures <- err
					}
				}
			}(worker)
		}
		wg.Wait()
		elapsed := time.Since(started)
		require.NoError(t, <-profileDone)
		close(failures)
		after := metrics()
		layoutAfter := layout()
		layoutBeforeJSON, _ := json.Marshal(layoutBefore)
		layoutAfterJSON, _ := json.Marshal(layoutAfter)
		rows := func(samples []suite.MetricSample) float64 {
			var sum float64
			for _, sample := range samples {
				if sample.Name == "wukongim_conversation_membership_mutation_rows_total" {
					sum += sample.Value
				}
			}
			return sum
		}
		sort.Float64s(latencies)
		report.Hot = map[string]any{"messages": total, "concurrency": concurrency, "seconds": elapsed.Seconds(), "qps": float64(total) / elapsed.Seconds(), "p50_ms": latencies[total/2], "p99_ms": latencies[total*99/100], "membership_writes": rows(after) - rows(before), "errors": len(failures), "metric_nodes": nodes, "layout_before": layoutBefore, "layout_after": layoutAfter, "layout_changed": string(layoutBeforeJSON) != string(layoutAfterJSON)}
		durations := func(samples []suite.MetricSample) map[string]float64 {
			values := make(map[string]float64)
			for _, sample := range samples {
				if strings.HasPrefix(sample.Name, "wukongim_slot_read_barrier_duration_seconds_") || strings.HasPrefix(sample.Name, "wukongim_channelv2_append_stage_duration_seconds_") {
					if strings.HasSuffix(sample.Name, "_sum") || strings.HasSuffix(sample.Name, "_count") {
						values[sample.Name+"/"+sample.Labels["stage"]+"/"+sample.Labels["result"]] += sample.Value
					}
				}
			}
			return values
		}
		delta := durations(after)
		for key, value := range durations(before) {
			delta[key] -= value
		}
		require.LessOrEqual(t, len(delta), 128, "bounded closed metric families only")
		report.Hot.(map[string]any)["stage_metric_deltas"] = delta
		for err := range failures {
			require.NoError(t, err)
		}
		require.Equal(t, rows(before), rows(after), "hot SEND must not rewrite membership")
	}

}
