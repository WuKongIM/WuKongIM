//go:build e2e

package webhook

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	wkclient "github.com/WuKongIM/WuKongIM/pkg/client"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

const (
	webhookEventMsgNotify        = "msg.notify"
	webhookEventMsgOffline       = "msg.offline"
	webhookEventUserOnlineStatus = "user.onlinestatus"
)

func TestWukongIMWebhookReceivesNotifyOfflineAndOnlineStatus(t *testing.T) {
	sink := newWebhookSink(t)
	defer sink.close()

	node := suite.New(t).StartSingleNodeCluster(suite.WithNodeConfigOverrides(1, map[string]string{
		"WK_GATEWAY_TOKEN_AUTH_ON":                 "false",
		"WK_WEBHOOK_HTTP_ADDR":                     sink.URL(),
		"WK_WEBHOOK_QUEUE_SIZE":                    "64",
		"WK_WEBHOOK_WORKERS":                       "2",
		"WK_WEBHOOK_MSG_NOTIFY_BATCH_MAX_ITEMS":    "1",
		"WK_WEBHOOK_MSG_NOTIFY_BATCH_MAX_WAIT":     "1ms",
		"WK_WEBHOOK_ONLINE_STATUS_BATCH_MAX_ITEMS": "1",
		"WK_WEBHOOK_ONLINE_STATUS_BATCH_MAX_WAIT":  "1ms",
		"WK_WEBHOOK_OFFLINE_UID_BATCH_SIZE":        "32",
		"WK_WEBHOOK_REQUEST_TIMEOUT":               "2s",
		"WK_WEBHOOK_RETRY_MAX_ATTEMPTS":            "1",
	}))

	client, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	const (
		fromUID     = "webhooksender"
		toUID       = "webhookrecipient"
		clientSeq   = uint64(1)
		clientMsgNo = "webhook-e2e-msg-1"
		topic       = "webhook-topic"
		expire      = uint32(3600)
		payload     = "hello webhook e2e"
	)

	require.NoError(t, client.Connect(node.GatewayAddr(), fromUID, fromUID+"-device"), node.DumpDiagnostics())
	onlineReq := sink.requireEvent(t, webhookEventUserOnlineStatus, node, func(req webhookRequest) error {
		values, err := decodeOnlineStatus(req.Body)
		if err != nil {
			return err
		}
		for _, value := range values {
			parts := strings.Split(value, "-")
			if len(parts) == 6 && parts[0] == fromUID && parts[1] == "0" && parts[2] == "1" {
				return nil
			}
		}
		return fmt.Errorf("online status values = %#v, want %s-0-1-*", values, fromUID)
	})
	require.Equal(t, http.MethodPost, onlineReq.Method)

	require.NoError(t, client.SendFrame(&frame.SendPacket{
		Setting:     frame.SettingTopic,
		Expire:      expire,
		ClientSeq:   clientSeq,
		ClientMsgNo: clientMsgNo,
		ChannelID:   toUID,
		ChannelType: frame.ChannelTypePerson,
		Topic:       topic,
		Payload:     []byte(payload),
	}), node.DumpDiagnostics())

	sendack, err := client.ReadSendAck()
	require.NoError(t, err, node.DumpDiagnostics())
	require.Equal(t, frame.ReasonSuccess, sendack.ReasonCode, node.DumpDiagnostics())
	require.Equal(t, clientSeq, sendack.ClientSeq)
	require.Equal(t, clientMsgNo, sendack.ClientMsgNo)
	require.NotZero(t, sendack.MessageID)
	require.NotZero(t, sendack.MessageSeq)

	notifyReq := sink.requireEvent(t, webhookEventMsgNotify, node, func(req webhookRequest) error {
		messages, err := decodeNotify(req.Body)
		if err != nil {
			return err
		}
		for _, msg := range messages {
			if err := requireWebhookMessage(msg, sendack.MessageID, sendack.MessageSeq, fromUID, clientMsgNo, topic, expire, payload); err == nil {
				return nil
			}
		}
		return fmt.Errorf("notify messages = %#v, want committed sendack message", messages)
	})
	require.Equal(t, "application/json", notifyReq.ContentType)

	sink.requireEvent(t, webhookEventMsgOffline, node, func(req webhookRequest) error {
		offline, err := decodeOffline(req.Body)
		if err != nil {
			return err
		}
		if err := requireWebhookMessage(offline.webhookMessagePayload, sendack.MessageID, sendack.MessageSeq, fromUID, clientMsgNo, topic, expire, payload); err != nil {
			return err
		}
		if !containsString(offline.ToUIDs, toUID) {
			return fmt.Errorf("offline to_uids = %#v, want %s", offline.ToUIDs, toUID)
		}
		if offline.SourceID == 0 {
			return fmt.Errorf("offline source_id = 0, want sender node id")
		}
		return nil
	})
}

// TestBeforeSendWebhookAuthenticatedFaults exercises callback failure boundaries
// through real authenticated sessions and verifies decisions against public metrics.
func TestBeforeSendWebhookAuthenticatedFaults(t *testing.T) {
	var mu sync.Mutex
	counts, active, peak := map[string]int{}, map[string]int{}, map[string]int{}
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ClientMsgNo string `json:"client_msg_no"`
			Payload     []byte `json:"payload"`
		}
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" ||
			r.URL.Query().Get("event") != "msg.before_send" ||
			json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req) != nil {
			http.Error(w, "invalid callback", http.StatusBadRequest)
			return
		}
		mu.Lock()
		counts[req.ClientMsgNo]++
		active[r.URL.Path]++
		if active[r.URL.Path] > peak[r.URL.Path] {
			peak[r.URL.Path] = active[r.URL.Path]
		}
		mu.Unlock()
		defer func() {
			mu.Lock()
			active[r.URL.Path]--
			mu.Unlock()
		}()
		switch string(req.Payload) {
		case "deny":
			_ = json.NewEncoder(w).Encode(map[string]any{"allow": false, "reason_code": 200})
		case "modify":
			_ = json.NewEncoder(w).Encode(map[string]any{"allow": true, "payload": []byte("modified")})
		case "timeout":
			<-r.Context().Done()
		case "block":
			select {
			case entered <- struct{}{}:
			case <-r.Context().Done():
				return
			}
			select {
			case <-release:
				_ = json.NewEncoder(w).Encode(map[string]any{"allow": true})
			case <-r.Context().Done():
			}
		case "slow":
			timer := time.NewTimer(100 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-timer.C:
				_ = json.NewEncoder(w).Encode(map[string]any{"allow": true})
			case <-r.Context().Done():
			}
		case "http_error":
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		case "malformed":
			_, _ = io.WriteString(w, `{"allow":`)
		case "redirect":
			http.Redirect(w, r, "/must-not-follow", http.StatusTemporaryRedirect)
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"allow": true})
		}
	}))
	defer func() {
		unblock()
		sink.Close()
	}()
	options := []suite.Option{suite.WithManagerHTTP()}
	for id := uint64(1); id <= 3; id++ {
		onTimeout, onError := "deny", "deny"
		if id == 2 {
			onTimeout = "allow"
		}
		if id == 3 {
			onError = "allow"
		}
		options = append(options, suite.WithNodeConfigOverrides(id, map[string]string{
			"WK_GATEWAY_TOKEN_AUTH_ON":             "true",
			"WK_CLUSTER_HASH_SLOT_COUNT":           "256",
			"WK_CLUSTER_SLOT_REPLICA_N":            "2",
			"WK_PLUGIN_ENABLE":                     "false",
			"WK_WEBHOOK_BEFORE_SEND_ENABLED":       "true",
			"WK_WEBHOOK_BEFORE_SEND_HTTP_ADDR":     sink.URL + "/" + strconv.FormatUint(id, 10),
			"WK_WEBHOOK_BEFORE_SEND_TIMEOUT":       "3s",
			"WK_WEBHOOK_BEFORE_SEND_MAX_IN_FLIGHT": "2",
			"WK_WEBHOOK_BEFORE_SEND_ON_TIMEOUT":    onTimeout,
			"WK_WEBHOOK_BEFORE_SEND_ON_ERROR":      onError,
		}))
	}
	cluster := suite.New(t).StartThreeNodeCluster(options...)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitHTTPReady(ctx), cluster.DumpDiagnostics())
	_, err := cluster.WaitSlotLeadersStable(ctx, time.Second)
	require.NoError(t, err, cluster.DumpDiagnostics())
	const sender, receiver, group = "fault-sender", "fault-receiver", "fault-group"
	for _, uid := range []string{sender, receiver} {
		_, err = suite.PostJSON(ctx, "http://"+cluster.MustNode(1).APIAddr()+"/user/token", map[string]any{
			"uid": uid, "token": uid + "-test-token", "device_flag": uint8(frame.APP), "device_level": 0,
		}, nil)
		require.NoError(t, err)
	}
	connect := func(node uint64, uid string) *wkclient.Client {
		t.Helper()
		conn, err := wkclient.New(wkclient.Config{Addr: cluster.MustNode(node).GatewayAddr(), AutoRecvAck: true})
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })
		_, err = conn.Connect(ctx, wkclient.ConnectOptions{UID: uid, Token: uid + "-test-token", DeviceID: uid + "-device", DeviceFlag: frame.APP})
		require.NoError(t, err)
		return conn
	}
	senderConn, receiverConn := connect(1, sender), connect(3, receiver)
	bad, err := wkclient.New(wkclient.Config{Addr: cluster.MustNode(2).GatewayAddr()})
	require.NoError(t, err)
	t.Cleanup(func() { _ = bad.Close() })
	_, err = bad.Connect(ctx, wkclient.ConnectOptions{UID: sender, Token: "incorrect", DeviceID: "invalid-device", DeviceFlag: frame.APP})
	require.EqualError(t, err, "client: connack reason=ReasonAuthFail")
	_ = bad.Close()
	require.NoError(t, suite.PostChannel(ctx, cluster.MustNode(1).APIAddr(), map[string]any{
		"channel_id": group, "channel_type": 2, "reset": 1, "subscribers": []string{sender, receiver},
	}))
	body := func(id, payload string) map[string]any {
		return map[string]any{"from_uid": sender, "channel_id": group, "channel_type": 2, "client_msg_no": id, "payload": []byte(payload)}
	}
	warm, err := suite.PostMessageSendEventually(ctx, cluster.MustNode(1).APIAddr(), body("fault-warm", "warm"))
	require.NoError(t, err)
	require.EqualValues(t, frame.ReasonSuccess, warm.Reason)
	expectedHistory := map[string]string{"fault-warm": "warm"}
	receive := func(id, payload string) {
		t.Helper()
		recvCtx, stop := context.WithTimeout(ctx, 5*time.Second)
		defer stop()
		received, err := receiverConn.Recv(recvCtx)
		if err != nil {
			mu.Lock()
			observed := maps.Clone(counts)
			mu.Unlock()
			t.Logf("[DEBUG-webhook-warm] receive=%s callback_counts=%v", id, observed)
			diagnosticCtx, finish := context.WithTimeout(context.Background(), 5*time.Second)
			defer finish()
			for node := uint64(1); node <= 3; node++ {
				samples, metricErr := suite.FetchMetricSamples(diagnosticCtx, cluster.MustNode(node).APIAddr())
				t.Logf("[DEBUG-webhook-warm] node=%d metrics_error=%v", node, metricErr)
				shown := 0
				for _, sample := range samples {
					if shown == 100 {
						break
					}
					if sample.Value != 0 && !strings.HasSuffix(sample.Name, "_bucket") && (strings.Contains(sample.Name, "delivery") || strings.Contains(sample.Name, "presence") || strings.Contains(sample.Name, "webhook") || strings.Contains(sample.Name, "gateway")) {
						t.Logf("[DEBUG-webhook-warm] node=%d metric=%s value=%v labels=%v", node, sample.Name, sample.Value, sample.Labels)
						shown++
					}
				}
			}
			t.Logf("[DEBUG-webhook-warm] %s", cluster.DumpDiagnostics())
		}
		require.NoError(t, err)
		require.Equal(t, id, received.ClientMsgNo)
		require.Equal(t, payload, string(received.Payload))
		expectedHistory[id] = payload
	}
	receive("fault-warm", "warm")
	baseline := map[uint64][]suite.MetricSample{}
	for id := uint64(1); id <= 3; id++ {
		baseline[id], err = suite.FetchMetricSamples(ctx, cluster.MustNode(id).APIAddr())
		require.NoError(t, err)
	}
	wantedMetrics := map[uint64]map[string]float64{1: {}, 2: {}, 3: {}}
	wantedCallbacks := map[string]int{}
	for seq, mode := range []string{"allow", "modify", "deny"} {
		id := "fault-wk-" + mode
		start := time.Now()
		result, sendErr := senderConn.Send(ctx, wkclient.Message{ClientSeq: uint64(seq + 1), ClientMsgNo: id, ChannelID: group, ChannelType: 2, Payload: []byte(mode)})
		t.Logf("transport=wkproto mode=%s latency_ms=%.2f", mode, float64(time.Since(start).Microseconds())/1000)
		wantedCallbacks[id] = 1
		if mode == "deny" {
			require.Error(t, sendErr)
			require.EqualValues(t, 200, result.ReasonCode)
			require.Zero(t, result.MessageSeq)
			wantedMetrics[1]["reject"]++
		} else {
			require.NoError(t, sendErr)
			payload := mode
			if mode == "modify" {
				payload = "modified"
			}
			receive(id, payload)
			wantedMetrics[1]["allow"]++
		}
	}
	for _, tc := range []struct {
		node          uint64
		mode, outcome string
		reason        uint8
	}{
		{1, "slow", "allow", 1}, {1, "timeout", "timeout_deny", uint8(frame.ReasonSystemError)},
		{2, "timeout", "timeout_allow", 1}, {2, "deny", "reject", 200}, {3, "deny", "reject", 200},
		{1, "http_error", "error_deny", uint8(frame.ReasonSystemError)}, {3, "http_error", "error_allow", 1},
		{1, "malformed", "error_deny", uint8(frame.ReasonSystemError)}, {3, "malformed", "error_allow", 1},
		{1, "redirect", "error_deny", uint8(frame.ReasonSystemError)},
	} {
		id := fmt.Sprintf("fault-http-%d-%s", tc.node, tc.mode)
		start := time.Now()
		result, err := suite.PostMessageSend(ctx, cluster.MustNode(tc.node).APIAddr(), body(id, tc.mode))
		require.NoError(t, err)
		require.Equal(t, tc.reason, result.Reason, id)
		t.Logf("node=%d mode=%s result=%s latency_ms=%.2f", tc.node, tc.mode, tc.outcome, float64(time.Since(start).Microseconds())/1000)
		wantedCallbacks[id] = 1
		wantedMetrics[tc.node][tc.outcome]++
		if tc.reason == 1 {
			receive(id, tc.mode)
		} else {
			require.Zero(t, result.MessageSeq)
			require.Zero(t, result.MessageID)
		}
	}
	// Saturate node 3 with on_error=allow: overload must still reject without
	// invoking the callback. Node 2 stays available, and node 3 recovers on release.
	const saturatedNode = uint64(3)
	type response struct {
		id     string
		result suite.MessageSendResponse
		err    error
	}
	blocked := make(chan response, 2)
	for i := 0; i < 2; i++ {
		id := fmt.Sprintf("fault-block-%d", i)
		go func() {
			result, err := suite.PostMessageSend(ctx, cluster.MustNode(saturatedNode).APIAddr(), body(id, "block"))
			blocked <- response{id, result, err}
		}()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("callbacks did not saturate")
		}
	}
	var overloadMax time.Duration
	for i := 0; i < 16; i++ {
		id := fmt.Sprintf("fault-overload-%d", i)
		start := time.Now()
		result, err := suite.PostMessageSend(ctx, cluster.MustNode(saturatedNode).APIAddr(), body(id, "allow"))
		require.NoError(t, err)
		require.EqualValues(t, frame.ReasonSystemError, result.Reason)
		require.Zero(t, result.MessageSeq)
		require.Zero(t, result.MessageID)
		overloadMax = max(overloadMax, time.Since(start))
		wantedCallbacks[id] = 0
		wantedMetrics[saturatedNode]["overloaded"]++
	}
	t.Logf("overload_requests=16 callback_requests=0 max_latency_ms=%.2f", float64(overloadMax.Microseconds())/1000)
	result, err := suite.PostMessageSend(ctx, cluster.MustNode(2).APIAddr(), body("fault-other-node", "allow"))
	require.NoError(t, err)
	require.EqualValues(t, frame.ReasonSuccess, result.Reason)
	receive("fault-other-node", "allow")
	wantedCallbacks["fault-other-node"] = 1
	wantedMetrics[2]["allow"]++
	unblock()
	blockedReceives := map[string]bool{}
	for i := 0; i < 2; i++ {
		var r response
		select {
		case r = <-blocked:
		case <-ctx.Done():
			t.Fatal("blocked sends did not finish")
		}
		require.NoError(t, r.err)
		require.EqualValues(t, frame.ReasonSuccess, r.result.Reason)
		wantedCallbacks[r.id] = 1
		wantedMetrics[saturatedNode]["allow"]++
		expectedHistory[r.id] = "block"
		blockedReceives[r.id] = true
	}
	for i := 0; i < 2; i++ {
		recv, err := receiverConn.Recv(ctx)
		require.NoError(t, err)
		require.Contains(t, blockedReceives, recv.ClientMsgNo)
		require.Equal(t, "block", string(recv.Payload))
		delete(blockedReceives, recv.ClientMsgNo)
	}
	result, err = suite.PostMessageSend(ctx, cluster.MustNode(saturatedNode).APIAddr(), body("fault-recovered", "allow"))
	require.NoError(t, err)
	require.EqualValues(t, frame.ReasonSuccess, result.Reason)
	receive("fault-recovered", "allow")
	wantedCallbacks["fault-recovered"] = 1
	wantedMetrics[saturatedNode]["allow"]++
	sink.Close()
	for _, id := range []uint64{1, 3} {
		key := fmt.Sprintf("fault-unreachable-%d", id)
		result, err := suite.PostMessageSend(ctx, cluster.MustNode(id).APIAddr(), body(key, "unreachable"))
		require.NoError(t, err)
		wantedCallbacks[key] = 0
		if id == 1 {
			require.EqualValues(t, frame.ReasonSystemError, result.Reason)
			require.Zero(t, result.MessageSeq)
			wantedMetrics[id]["error_deny"]++
		} else {
			require.EqualValues(t, frame.ReasonSuccess, result.Reason)
			receive(key, "unreachable")
			wantedMetrics[id]["error_allow"]++
		}
	}
	var history struct {
		Messages []struct {
			ClientMsgNo string `json:"client_msg_no"`
			Payload     []byte `json:"payload"`
		} `json:"messages"`
	}
	_, err = suite.PostJSON(ctx, "http://"+cluster.MustNode(2).APIAddr()+"/channel/messagesync", map[string]any{"login_uid": receiver, "channel_id": group, "channel_type": 2, "limit": 100}, &history)
	require.NoError(t, err)
	require.Len(t, history.Messages, len(expectedHistory))
	for _, msg := range history.Messages {
		require.Contains(t, expectedHistory, msg.ClientMsgNo)
		require.Equal(t, expectedHistory[msg.ClientMsgNo], string(msg.Payload))
		delete(expectedHistory, msg.ClientMsgNo)
	}
	require.Empty(t, expectedHistory)
	mu.Lock()
	observedCounts, observedPeak, observedActive := maps.Clone(counts), maps.Clone(peak), maps.Clone(active)
	mu.Unlock()
	for id, want := range wantedCallbacks {
		require.Equal(t, want, observedCounts[id], id)
	}
	for path, peakCount := range observedPeak {
		require.LessOrEqual(t, peakCount, 2, path)
		require.Zero(t, observedActive[path], path)
		t.Logf("callback_node=%s peak_in_flight=%d active_after_recovery=%d", path, peakCount, observedActive[path])
	}
	require.Equal(t, 2, observedPeak["/3"])

	for id := uint64(1); id <= 3; id++ {
		samples, err := suite.FetchMetricSamples(ctx, cluster.MustNode(id).APIAddr())
		require.NoError(t, err)
		for _, outcome := range []string{"allow", "reject", "error_allow", "error_deny", "timeout_allow", "timeout_deny", "overloaded", "invalid_request", "canceled"} {
			want := wantedMetrics[id][outcome]
			labels := map[string]string{"result": outcome}
			delta := suite.SumMetricSamples(samples, "wukongim_webhook_before_send_total", labels) - suite.SumMetricSamples(baseline[id], "wukongim_webhook_before_send_total", labels)
			require.Equal(t, want, delta, "node=%d outcome=%s", id, outcome)
		}
		cpuDelta := "unavailable"
		hasCPU := func(samples []suite.MetricSample) bool {
			return slices.ContainsFunc(samples, func(sample suite.MetricSample) bool { return sample.Name == "process_cpu_seconds_total" })
		}
		if hasCPU(samples) && hasCPU(baseline[id]) {
			cpuDelta = fmt.Sprintf("%.3f", suite.SumMetricSamples(samples, "process_cpu_seconds_total", nil)-suite.SumMetricSamples(baseline[id], "process_cpu_seconds_total", nil))
		}
		t.Logf("node=%d heap_before_bytes=%.0f heap_after_bytes=%.0f goroutines_before=%.0f goroutines_after=%.0f cpu_seconds_delta=%s", id,
			suite.SumMetricSamples(baseline[id], "go_memstats_heap_alloc_bytes", nil), suite.SumMetricSamples(samples, "go_memstats_heap_alloc_bytes", nil),
			suite.SumMetricSamples(baseline[id], "go_goroutines", nil), suite.SumMetricSamples(samples, "go_goroutines", nil), cpuDelta)
	}
}

type webhookSink struct {
	server *httptest.Server
	mu     sync.Mutex
	events []webhookRequest
}

type webhookRequest struct {
	Method      string
	ContentType string
	Event       string
	Body        []byte
}

func newWebhookSink(t *testing.T) *webhookSink {
	t.Helper()
	sink := &webhookSink{}
	sink.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		sink.mu.Lock()
		sink.events = append(sink.events, webhookRequest{
			Method:      r.Method,
			ContentType: r.Header.Get("Content-Type"),
			Event:       r.URL.Query().Get("event"),
			Body:        append([]byte(nil), body...),
		})
		sink.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	return sink
}

func (s *webhookSink) URL() string {
	if s == nil || s.server == nil {
		return ""
	}
	return s.server.URL + "/webhook"
}

func (s *webhookSink) close() {
	if s != nil && s.server != nil {
		s.server.Close()
	}
}

func (s *webhookSink) requireEvent(t *testing.T, event string, node *suite.StartedNode, check func(webhookRequest) error) webhookRequest {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		for _, req := range s.snapshot() {
			if req.Event != event {
				continue
			}
			if check == nil {
				return req
			}
			if err := check(req); err == nil {
				return req
			} else {
				lastErr = err
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("webhook event %q timed out: lastErr=%v captured=%s\n%s", event, lastErr, s.dump(), node.DumpDiagnostics())
	return webhookRequest{}
}

func (s *webhookSink) snapshot() []webhookRequest {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]webhookRequest, len(s.events))
	copy(out, s.events)
	return out
}

func (s *webhookSink) dump() string {
	var b strings.Builder
	for i, req := range s.snapshot() {
		fmt.Fprintf(&b, "\n[%d] method=%s event=%s content_type=%s body=%s", i, req.Method, req.Event, req.ContentType, strings.TrimSpace(string(req.Body)))
	}
	if b.Len() == 0 {
		return "<none>"
	}
	return b.String()
}

type webhookMessagePayload struct {
	Header struct {
		NoPersist uint8 `json:"no_persist"`
		RedDot    uint8 `json:"red_dot"`
		SyncOnce  uint8 `json:"sync_once"`
	} `json:"header"`
	Setting      uint8  `json:"setting"`
	Topic        string `json:"topic"`
	Expire       uint32 `json:"expire"`
	MessageID    uint64 `json:"message_id"`
	MessageIDStr string `json:"message_idstr"`
	ClientMsgNo  string `json:"client_msg_no"`
	MessageSeq   uint64 `json:"message_seq"`
	FromUID      string `json:"from_uid"`
	ChannelID    string `json:"channel_id"`
	ChannelType  uint8  `json:"channel_type"`
	Timestamp    int32  `json:"timestamp"`
	Payload      []byte `json:"payload"`
}

type webhookOfflinePayload struct {
	webhookMessagePayload
	ToUIDs   []string `json:"to_uids"`
	SourceID int64    `json:"source_id"`
}

func decodeOnlineStatus(body []byte) ([]string, error) {
	var values []string
	if err := json.Unmarshal(body, &values); err != nil {
		return nil, err
	}
	return values, nil
}

func decodeNotify(body []byte) ([]webhookMessagePayload, error) {
	var messages []webhookMessagePayload
	if err := json.Unmarshal(body, &messages); err != nil {
		return nil, err
	}
	return messages, nil
}

func decodeOffline(body []byte) (webhookOfflinePayload, error) {
	var offline webhookOfflinePayload
	if err := json.Unmarshal(body, &offline); err != nil {
		return webhookOfflinePayload{}, err
	}
	return offline, nil
}

func requireWebhookMessage(msg webhookMessagePayload, messageID int64, messageSeq uint64, fromUID, clientMsgNo, topic string, expire uint32, payload string) error {
	if msg.MessageID != uint64(messageID) {
		return fmt.Errorf("message_id = %d, want %d", msg.MessageID, messageID)
	}
	if msg.MessageIDStr != strconv.FormatInt(messageID, 10) {
		return fmt.Errorf("message_idstr = %q, want %d", msg.MessageIDStr, messageID)
	}
	if msg.MessageSeq != messageSeq {
		return fmt.Errorf("message_seq = %d, want %d", msg.MessageSeq, messageSeq)
	}
	if msg.FromUID != fromUID {
		return fmt.Errorf("from_uid = %q, want %q", msg.FromUID, fromUID)
	}
	if msg.ClientMsgNo != clientMsgNo {
		return fmt.Errorf("client_msg_no = %q, want %q", msg.ClientMsgNo, clientMsgNo)
	}
	if msg.Setting != frame.SettingTopic.Uint8() {
		return fmt.Errorf("setting = %d, want %d", msg.Setting, frame.SettingTopic.Uint8())
	}
	if msg.Topic != topic {
		return fmt.Errorf("topic = %q, want %q", msg.Topic, topic)
	}
	if msg.Expire != expire {
		return fmt.Errorf("expire = %d, want %d", msg.Expire, expire)
	}
	if msg.ChannelType != frame.ChannelTypePerson {
		return fmt.Errorf("channel_type = %d, want %d", msg.ChannelType, frame.ChannelTypePerson)
	}
	if string(msg.Payload) != payload {
		return fmt.Errorf("payload = %q, want %q", string(msg.Payload), payload)
	}
	return nil
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestBeforeSendWebhookThreeNodeAdmission(t *testing.T) {
	var mu sync.Mutex
	counts := map[string]int{}
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("event") != "msg.before_send" {
			http.Error(w, "event", 400)
			return
		}
		var req struct {
			ClientMsgNo string `json:"client_msg_no"`
			Payload     []byte `json:"payload"`
			ChannelID   string `json:"channel_id"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			http.Error(w, "JSON", 400)
			return
		}
		mu.Lock()
		counts[req.ClientMsgNo]++
		mu.Unlock()
		switch string(req.Payload) {
		case "deny":
			_ = json.NewEncoder(w).Encode(map[string]any{"allow": false, "reason_code": 200})
		case "modify":
			_ = json.NewEncoder(w).Encode(map[string]any{"allow": true, "payload": base64.StdEncoding.EncodeToString([]byte("modified"))})
		case "timeout":
			<-r.Context().Done()
		case "http_error":
			http.Error(w, "unavailable", 503)
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"allow": true})
		}
	}))
	defer sink.Close()
	options := []suite.Option{suite.WithManagerHTTP()}
	for id := uint64(1); id <= 3; id++ {
		timeoutPolicy, errorPolicy := "deny", "deny"
		if id == 2 {
			timeoutPolicy = "allow"
		}
		if id == 3 {
			errorPolicy = "allow"
		}
		options = append(options, suite.WithNodeConfigOverrides(id, map[string]string{
			"WK_GATEWAY_TOKEN_AUTH_ON": "false",
			"WK_PLUGIN_ENABLE":         "false", "WK_WEBHOOK_BEFORE_SEND_ENABLED": "true",
			"WK_WEBHOOK_BEFORE_SEND_HTTP_ADDR": sink.URL, "WK_WEBHOOK_BEFORE_SEND_TIMEOUT": "100ms",
			"WK_WEBHOOK_BEFORE_SEND_ON_TIMEOUT": timeoutPolicy, "WK_WEBHOOK_BEFORE_SEND_ON_ERROR": errorPolicy,
		}))
	}
	cluster := suite.New(t).StartThreeNodeCluster(options...)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())
	_, err := cluster.WaitSlotLeadersStable(ctx, time.Second)
	require.NoError(t, err, cluster.DumpDiagnostics())
	const sender = "before-sender"
	const receiver = "before-receiver"
	const group = "before-send-group"
	require.NoError(t, suite.PostChannel(ctx, cluster.MustNode(1).APIAddr(), map[string]any{
		"channel_id": group, "channel_type": 2, "reset": 1, "subscribers": []string{sender, receiver},
	}), cluster.DumpDiagnostics())
	sendClient, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	defer sendClient.Close()
	recvClient, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	defer recvClient.Close()
	require.NoError(t, sendClient.Connect(cluster.MustNode(1).GatewayAddr(), sender, sender+"-device"))
	require.NoError(t, recvClient.Connect(cluster.MustNode(3).GatewayAddr(), receiver, receiver+"-device"))
	body := func(id, payload string) map[string]any {
		return map[string]any{"from_uid": sender, "channel_id": group, "channel_type": 2, "client_msg_no": id, "payload": base64.StdEncoding.EncodeToString([]byte(payload))}
	}
	warm, err := suite.PostMessageSendEventually(ctx, cluster.MustNode(1).APIAddr(), body("before-warm", "warm"))
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.EqualValues(t, frame.ReasonSuccess, warm.Reason)
	_, err = recvClient.ReadRecv()
	require.NoError(t, err, cluster.DumpDiagnostics())
	for i, payload := range []string{"deny", "modify"} {
		id := "before-wk-" + payload
		require.NoError(t, sendClient.SendFrame(&frame.SendPacket{ClientSeq: uint64(i + 1), ClientMsgNo: id, ChannelID: group, ChannelType: 2, Payload: []byte(payload)}))
		ack, err := sendClient.ReadSendAck()
		require.NoError(t, err, cluster.DumpDiagnostics())
		if payload == "deny" {
			require.EqualValues(t, 200, ack.ReasonCode)
			require.Zero(t, ack.MessageID)
			require.Zero(t, ack.MessageSeq)
		} else {
			require.Equal(t, frame.ReasonSuccess, ack.ReasonCode)
			recv, err := recvClient.ReadRecv()
			require.NoError(t, err, cluster.DumpDiagnostics())
			require.Equal(t, id, recv.ClientMsgNo)
			require.Equal(t, "modified", string(recv.Payload))
		}
	}
	for _, tc := range []struct {
		node        uint64
		id, payload string
		reason      uint8
	}{
		{1, "before-http-deny", "deny", 200},
		{1, "before-timeout-deny", "timeout", uint8(frame.ReasonSystemError)},
		{2, "before-timeout-allow", "timeout", uint8(frame.ReasonSuccess)},
		{2, "before-error-deny", "http_error", uint8(frame.ReasonSystemError)},
		{3, "before-error-allow", "http_error", uint8(frame.ReasonSuccess)},
	} {
		result, err := suite.PostMessageSend(ctx, cluster.MustNode(tc.node).APIAddr(), body(tc.id, tc.payload))
		require.NoError(t, err, cluster.DumpDiagnostics())
		require.Equal(t, tc.reason, result.Reason, tc.id)
		if tc.reason == uint8(frame.ReasonSuccess) {
			recv, err := recvClient.ReadRecv()
			require.NoError(t, err, cluster.DumpDiagnostics())
			require.Equal(t, tc.id, recv.ClientMsgNo)
			require.Equal(t, tc.payload, string(recv.Payload))
		} else {
			require.Zero(t, result.MessageID)
			require.Zero(t, result.MessageSeq)
		}
	}
	transient := body("before-transient", "transient")
	transient["no_persist"] = 1
	transient["sync_once"] = 1
	delete(transient, "channel_id")
	delete(transient, "channel_type")
	transient["subscribers"] = []string{receiver}
	result, err := suite.PostMessageSend(ctx, cluster.MustNode(1).APIAddr(), transient)
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.EqualValues(t, frame.ReasonSuccess, result.Reason)
	recv, err := recvClient.ReadRecv()
	require.NoError(t, err)
	require.Equal(t, "before-transient", recv.ClientMsgNo)
	var history struct {
		Messages []struct {
			ClientMsgNo string `json:"client_msg_no"`
			Payload     []byte `json:"payload"`
		} `json:"messages"`
	}
	_, err = suite.PostJSON(ctx, "http://"+cluster.MustNode(3).APIAddr()+"/channel/messagesync", map[string]any{
		"login_uid": receiver, "channel_id": group, "channel_type": 2, "start_message_seq": 1, "limit": 20, "pull_mode": 1,
	}, &history)
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Len(t, history.Messages, 4, "rejected and transient sends must not enter committed history")
	for _, msg := range history.Messages {
		require.NotContains(t, msg.ClientMsgNo, "deny")
		if msg.ClientMsgNo == "before-wk-modify" {
			require.Equal(t, "modified", string(msg.Payload))
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for _, id := range []string{"before-wk-deny", "before-wk-modify", "before-http-deny", "before-timeout-deny", "before-timeout-allow", "before-error-deny", "before-error-allow", "before-transient"} {
		require.Equal(t, 1, counts[id], id+": authority forwarding must not repeat the callback")
	}
}
