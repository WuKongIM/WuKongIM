//go:build e2e

package message_updates

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

const room = "edit-stability-room"
const control = "edit-stability-control"
const reader = "edit-stability-reader"
const sender = "edit-stability-sender"

// wireUint accepts the legacy numeric identity and the new decimal-string DTO.
type wireUint uint64

func (v *wireUint) UnmarshalJSON(b []byte) error {
	n, err := strconv.ParseUint(strings.Trim(string(b), `"`), 10, 64)
	*v = wireUint(n)
	return err
}

type message struct {
	ID        wireUint `json:"message_id"`
	Seq       wireUint `json:"message_seq"`
	Version   wireUint `json:"version"`
	Payload   []byte   `json:"payload"`
	Client    string   `json:"client_msg_no"`
	From      string   `json:"from_uid"`
	Timestamp int64    `json:"timestamp"`
	ServerMS  int64    `json:"server_timestamp_ms"`
}

type deltaPage struct {
	Updates []message `json:"updates"`
	Cursor  string    `json:"next_update_cursor"`
	More    bool      `json:"more"`
	Reset   bool      `json:"reset_required"`
}

type conversation struct {
	ID       string    `json:"channel_id"`
	Unread   uint64    `json:"unread"`
	ActiveAt int64     `json:"active_at"`
	Last     message   `json:"last_message"`
	Recents  []message `json:"recents"`
}

type counters struct {
	Success    map[string]uint64 `json:"success"`
	Temporary  map[string]uint64 `json:"temporary"`
	FirstError map[string]string `json:"first_error,omitempty"`
	Writers    [3]uint64         `json:"writers"`
	More       uint64            `json:"more_pages"`
}

type phase struct {
	Name            string    `json:"name"`
	Started         time.Time `json:"started"`
	Finished        time.Time `json:"finished"`
	Victim          uint64    `json:"victim,omitempty"`
	AuthorityBefore uint64    `json:"authority_before,omitempty"`
	AuthorityAfter  uint64    `json:"authority_after,omitempty"`
	Counts          counters  `json:"counts"`
}

type receipt struct {
	BinarySHA     string   `json:"binary_sha256"`
	Phases        []phase  `json:"phases"`
	FinalVersions []uint64 `json:"final_versions"`
	Complete      bool     `json:"complete"`
	FinalCounts   counters `json:"final_counts"`
}

// workload owns the acknowledged-version oracle and atomically persisted delta cache.
// Writers never share an original; reads capture their lower bound before dispatch.
type workload struct {
	addresses      [3]string
	client         *http.Client
	epoch          string
	mu             sync.Mutex
	originals      []message
	versions       []uint64
	controlMessage message
	listBaseline   []conversation
	cursor         string
	cached         map[uint64]uint64
	counts         counters
	seedBody       map[string]any
	seedReply      []byte
	victim         atomic.Uint64
	ingress        atomic.Uint64
	fault          atomic.Bool
}

func payload(i int, version uint64) []byte {
	return []byte(fmt.Sprintf("original=%02d;version=%020d;%s", i, version, strings.Repeat("x", 200)))
}

func (w *workload) addr() string {
	for {
		n := w.ingress.Add(1)%3 + 1
		if n != w.victim.Load() {
			return w.addresses[n-1]
		}
	}
}

type temporaryError struct{ reason string }

func (e temporaryError) Error() string { return e.reason }

// call returns explicit transient outcomes separately; it never retries a write.
func (w *workload) call(ctx context.Context, addr, path string, body any) ([]byte, http.Header, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, nil, err
	}
	request, err := http.NewRequestWithContext(ctx, "POST", "http://"+addr+path, bytes.NewReader(raw))
	if err != nil {
		return nil, nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := w.client.Do(request)
	if err != nil {
		return nil, nil, temporaryError{"transport: " + err.Error()}
	}
	defer response.Body.Close()
	raw, err = io.ReadAll(io.LimitReader(response.Body, (8<<20)+1))
	if err != nil {
		return nil, nil, temporaryError{"read: " + err.Error()}
	}
	if len(raw) > 8<<20 {
		return nil, nil, fmt.Errorf("oversized %s response", path)
	}
	if response.StatusCode == 200 {
		if w.epoch != "" && response.Header.Get("X-WK-Content-Epoch") != w.epoch {
			return nil, nil, fmt.Errorf("%s restore epoch changed", path)
		}
		return raw, response.Header, nil
	}
	var failure struct {
		Code  string `json:"code"`
		Error string `json:"error"`
		Msg   string `json:"msg"`
	}
	_ = json.Unmarshal(raw, &failure)
	// Only stable public codes authorize an HTTP retry; legacy error text never does.
	if (response.StatusCode == 503 && failure.Code == "unavailable") || (response.StatusCode == 409 && failure.Code == "stale_meta") {
		return raw, response.Header, temporaryError{fmt.Sprintf("%s HTTP %d %s", path, response.StatusCode, raw)}
	}
	return raw, response.Header, fmt.Errorf("%s HTTP %d %s", path, response.StatusCode, raw)
}

func (w *workload) body(i int, v uint64) map[string]any {
	return map[string]any{"login_uid": sender, "channel_id": room, "channel_type": 2, "message_id": fmt.Sprint(uint64(w.originals[i].ID)), "expected_content_epoch": w.epoch, "expected_version": fmt.Sprint(v - 1), "request_id": fmt.Sprintf("edit-%d-%d", i, v), "payload": payload(i, v)}
}

func (w *workload) edit(ctx context.Context, i int, v uint64, body map[string]any) ([]byte, error) {
	raw, _, err := w.call(ctx, w.addr(), "/message/update", body)
	if err != nil {
		return raw, err
	}
	var reply struct {
		Data message `json:"data"`
	}
	if err = json.Unmarshal(raw, &reply); err != nil {
		return nil, err
	}
	if reply.Data.ID != w.originals[i].ID || reply.Data.Seq != w.originals[i].Seq || uint64(reply.Data.Version) != v {
		return nil, fmt.Errorf("incorrect edit result %s", raw)
	}
	return raw, nil
}

func (w *workload) floor() []uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]uint64(nil), w.versions...)
}

func (w *workload) validate(m message, floor []uint64) error {
	for i, original := range w.originals {
		if m.ID != original.ID {
			continue
		}
		v := uint64(m.Version)
		if m.Seq != original.Seq || m.Client != original.Client || m.From != original.From || m.Timestamp != original.Timestamp {
			return fmt.Errorf("immutable identity changed id=%d seq=%d/%d client=%q/%q from=%q/%q timestamp=%d/%d", m.ID, m.Seq, original.Seq, m.Client, original.Client, m.From, original.From, m.Timestamp, original.Timestamp)
		}
		if v < floor[i] || !bytes.Equal(m.Payload, payload(i, v)) {
			return fmt.Errorf("stale/corrupt id=%d version=%d acknowledged_before_read=%d", m.ID, v, floor[i])
		}
		return nil
	}
	return fmt.Errorf("unknown message id=%d", m.ID)
}

func (w *workload) read(ctx context.Context, endpoint, addr string) error {
	floor := w.floor()
	body := map[string]any{"login_uid": reader, "channel_id": room, "channel_type": 2, "limit": 100}
	if endpoint == "/messages" {
		ids := make([]uint64, len(w.originals))
		for i, m := range w.originals {
			ids[i] = uint64(m.ID)
		}
		body["message_ids"] = ids
	}
	if endpoint == "/conversation/list" {
		body = map[string]any{"uid": reader, "limit": 10}
	}
	if endpoint == "/conversation/sync" {
		body = map[string]any{"uid": reader, "msg_count": 1, "version": 0, "last_msg_seqs": fmt.Sprintf("%s:2:%d", room, w.originals[len(w.originals)-1].Seq)}
	}
	raw, _, err := w.call(ctx, addr, endpoint, body)
	if err != nil {
		return err
	}
	if endpoint == "/conversation/list" || endpoint == "/conversation/sync" {
		var rows []conversation
		if endpoint == "/conversation/list" {
			var r struct {
				Rows []conversation `json:"conversations"`
			}
			err = json.Unmarshal(raw, &r)
			rows = r.Rows
		} else {
			err = json.Unmarshal(raw, &rows)
		}
		if err != nil {
			return err
		}
		if len(rows) != len(w.listBaseline) {
			return fmt.Errorf("%s missing conversation rows: %s", endpoint, raw)
		}
		for i, row := range rows {
			if endpoint == "/conversation/list" {
				base := w.listBaseline[i]
				if row.ID != base.ID || row.Unread != base.Unread || row.ActiveAt != base.ActiveAt || row.Last.ServerMS != base.Last.ServerMS {
					return fmt.Errorf("list order/unread/activation/timestamp changed")
				}
				row.Last.Timestamp = row.Last.ServerMS / 1000
				row.Last.ServerMS = 0
			} else {
				if len(row.Recents) != 1 {
					return fmt.Errorf("caught-up sync lost edited tail: %s", raw)
				}
				row.Last = row.Recents[0]
			}
			if row.ID == room {
				if row.Last.ID != w.originals[len(w.originals)-1].ID {
					return fmt.Errorf("old-message edit replaced tail")
				}
				if err = w.validate(row.Last, floor); err != nil {
					return err
				}
			} else if row.ID != control || !reflect.DeepEqual(row.Last, w.controlMessage) {
				return fmt.Errorf("control conversation changed: %s", raw)
			}
		}
		return nil
	}
	var result struct {
		Messages []message `json:"messages"`
	}
	if err = json.Unmarshal(raw, &result); err != nil {
		return err
	}
	if len(result.Messages) != len(w.originals) {
		return fmt.Errorf("%s incomplete originals %d", endpoint, len(result.Messages))
	}
	seen := map[wireUint]bool{}
	for _, m := range result.Messages {
		if seen[m.ID] {
			return fmt.Errorf("duplicate message")
		}
		seen[m.ID] = true
		if err = w.validate(m, floor); err != nil {
			return err
		}
	}
	return nil
}

func (w *workload) delta(ctx context.Context, addr string) error {
	w.mu.Lock()
	cursor := w.cursor
	w.mu.Unlock()
	raw, _, err := w.call(ctx, addr, "/channel/messageupdates", map[string]any{"login_uid": reader, "channel_id": room, "channel_type": 2, "update_cursor": cursor, "limit": 2})
	if err != nil {
		return err
	}
	var page deltaPage
	if err = json.Unmarshal(raw, &page); err != nil {
		return err
	}
	if page.Reset || page.Cursor == "" || len(page.Updates) > 2 || (page.More && page.Cursor == cursor) {
		return fmt.Errorf("invalid cursor/reset during restart: %s", raw)
	}
	zero := make([]uint64, len(w.originals))
	for _, m := range page.Updates {
		if err = w.validate(m, zero); err != nil {
			return err
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, m := range page.Updates {
		if uint64(m.Version) < w.cached[uint64(m.ID)] {
			return fmt.Errorf("delta version rollback id=%d", m.ID)
		}
		w.cached[uint64(m.ID)] = uint64(m.Version)
	}
	w.cursor = page.Cursor
	if page.More {
		w.counts.More++
	}
	return nil
}

func (w *workload) count(key string, err error, writer int) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err != nil {
		var transient temporaryError
		if errors.As(err, &transient) && w.fault.Load() {
			w.counts.Temporary[key]++
			if w.counts.FirstError[key] == "" {
				w.counts.FirstError[key] = transient.reason
			}
			return nil
		}
		return err
	}
	w.counts.Success[key]++
	if writer >= 0 {
		w.counts.Writers[writer]++
	}
	return nil
}

func (w *workload) snapshot() counters {
	w.mu.Lock()
	defer w.mu.Unlock()
	r := counters{Success: map[string]uint64{}, Temporary: map[string]uint64{}, FirstError: map[string]string{}, Writers: w.counts.Writers, More: w.counts.More}
	for k, v := range w.counts.Success {
		r.Success[k] = v
	}
	for k, v := range w.counts.Temporary {
		r.Temporary[k] = v
	}
	for k, v := range w.counts.FirstError {
		r.FirstError[k] = v
	}
	return r
}

func TestMessageUpdateConcurrentRecovery(t *testing.T) {
	if os.Getenv("WK_E2E_MESSAGE_UPDATE_STABILITY") != "1" {
		t.Skip("explicit bounded message-update stability run")
	}
	output := os.Getenv("WK_E2E_MESSAGE_UPDATE_STABILITY_REPORT")
	require.NotEmpty(t, output)
	require.NoError(t, os.MkdirAll(filepath.Dir(output), 0755))
	r := receipt{}
	defer func() {
		b, _ := json.MarshalIndent(r, "", "  ")
		require.NoError(t, os.WriteFile(output, append(b, '\n'), 0644))
	}()
	opts := []suite.Option{suite.WithManagerHTTP()}
	for n := uint64(1); n <= 3; n++ {
		opts = append(opts, suite.WithNodeConfigOverrides(n, map[string]string{
			"WK_GATEWAY_TOKEN_AUTH_ON": "false", "WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_INITIAL_SLOT_COUNT": "12", "WK_CLUSTER_CHANNEL_REPLICA_N": "3",
			"WK_CLUSTER_NODE_HEALTH_REPORT_INTERVAL": "500ms", "WK_CLUSTER_NODE_HEALTH_REPORT_TTL": "5s", "WK_CHANNEL_MIGRATION_ENABLE": "true", "WK_CHANNEL_MIGRATION_SCAN_INTERVAL": "100ms", "WK_CHANNEL_MIGRATION_SCAN_LIMIT": "16", "WK_CHANNEL_MIGRATION_MAX_PAGES_PER_TICK": "2", "WK_CHANNEL_MIGRATION_MAX_TASKS_PER_TICK": "2", "WK_CHANNEL_MIGRATION_TASK_LIMIT": "2",
		}), suite.WithNodeEnv(n, "GOMAXPROCS=2"))
	}
	c := suite.New(t).StartStaticCluster(3, opts...)
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Minute)
	defer cancel()
	require.NoError(t, c.WaitHTTPReady(ctx))
	b, err := os.ReadFile(c.Nodes[0].Process.BinaryPath)
	require.NoError(t, err)
	hash := sha256.Sum256(b)
	r.BinarySHA = hex.EncodeToString(hash[:])
	w := &workload{addresses: [3]string{c.Nodes[0].APIAddr(), c.Nodes[1].APIAddr(), c.Nodes[2].APIAddr()}, client: &http.Client{Timeout: 3 * time.Second}, cached: map[uint64]uint64{}, counts: counters{Success: map[string]uint64{}, Temporary: map[string]uint64{}, FirstError: map[string]string{}}}
	defer w.client.CloseIdleConnections()
	defer func() {
		r.FinalCounts = w.snapshot()
		if t.Failed() {
			t.Log(c.DumpDiagnostics())
		}
	}()
	for _, id := range []string{room, control} {
		require.NoError(t, suite.PostChannel(ctx, c.Nodes[0].APIAddr(), map[string]any{"channel_id": id, "channel_type": 2, "subscribers": []string{sender, reader}}))
		n := 12
		if id == control {
			n = 1
		}
		for i := 0; i < n; i++ {
			_, err := suite.PostMessageSendEventually(ctx, w.addr(), map[string]any{"from_uid": sender, "channel_id": id, "channel_type": 2, "client_msg_no": fmt.Sprintf("%s-%d", id, i), "payload": payload(i, 0), "header": map[string]any{"red_dot": 1}})
			require.NoError(t, err)
		}
	}
	// Baseline precedes history and all edits, as required by the SDK merge contract.
	raw, headers, err := w.call(ctx, w.addr(), "/channel/messageupdates", map[string]any{"login_uid": reader, "channel_id": room, "channel_type": 2})
	require.NoError(t, err)
	w.epoch = headers.Get("X-WK-Content-Epoch")
	require.NotEmpty(t, w.epoch)
	var baseline deltaPage
	require.NoError(t, json.Unmarshal(raw, &baseline))
	require.True(t, baseline.Reset)
	w.cursor = baseline.Cursor
	raw, _, err = w.call(ctx, w.addr(), "/channel/messagesync", map[string]any{"login_uid": reader, "channel_id": room, "channel_type": 2, "limit": 100})
	require.NoError(t, err)
	var history struct {
		Messages []message `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(raw, &history))
	require.Len(t, history.Messages, 12)
	// Normalize by immutable sequence; response ordering is validated separately by APIs.
	w.originals = make([]message, 12)
	w.versions = make([]uint64, 12)
	for _, m := range history.Messages {
		require.True(t, m.Seq >= 1 && m.Seq <= 12)
		w.originals[int(m.Seq)-1] = m
	}
	for i := range w.originals {
		body := w.body(i, 1)
		reply, e := w.edit(ctx, i, 1, body)
		require.NoError(t, e)
		w.versions[i] = 1
		if i == 11 {
			w.seedBody = body
			w.seedReply = reply
		}
	}
	// Competing control edits start from the same version; exactly one may commit.
	type candidate struct {
		payload []byte
		err     error
		raw     []byte
	}
	results := make(chan candidate, 2)
	start := make(chan struct{})
	raw, _, err = w.call(ctx, w.addr(), "/channel/messagesync", map[string]any{"login_uid": reader, "channel_id": control, "channel_type": 2, "limit": 10})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &history))
	require.Len(t, history.Messages, 1)
	for _, label := range []string{"A", "B"} {
		go func(label string) {
			<-start
			p := []byte("winner-" + label)
			b, _, e := w.call(ctx, w.addr(), "/message/update", map[string]any{"login_uid": sender, "channel_id": control, "channel_type": 2, "message_id": fmt.Sprint(uint64(history.Messages[0].ID)), "expected_content_epoch": w.epoch, "expected_version": "0", "request_id": "race-" + label, "payload": p})
			results <- candidate{p, e, b}
		}(label)
	}
	close(start)
	wins := 0
	for i := 0; i < 2; i++ {
		got := <-results
		if got.err == nil {
			wins++
		} else {
			require.Contains(t, got.err.Error(), "version_conflict")
		}
	}
	require.Equal(t, 1, wins)
	raw, _, err = w.call(ctx, w.addr(), "/conversation/list", map[string]any{"uid": reader, "limit": 10})
	require.NoError(t, err)
	var list struct {
		Rows []conversation `json:"conversations"`
	}
	require.NoError(t, json.Unmarshal(raw, &list))
	require.Len(t, list.Rows, 2)
	w.listBaseline = list.Rows
	for _, row := range list.Rows {
		if row.ID == control {
			w.controlMessage = row.Last
			w.controlMessage.Timestamp = row.Last.ServerMS / 1000
			w.controlMessage.ServerMS = 0
		}
	}
	require.NotZero(t, w.controlMessage.ID)
	for _, path := range []string{"/channel/messagesync", "/messages", "/conversation/list", "/conversation/sync"} {
		require.NoError(t, w.read(ctx, path, w.addr()), path)
	}

	workers, cancelWorkers := context.WithCancel(ctx)
	var wg sync.WaitGroup
	failures := make(chan error, 1)
	launch := func(fn func(context.Context) error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := fn(workers); e != nil && workers.Err() == nil {
				select {
				case failures <- e:
				default:
				}
				cancelWorkers()
			}
		}()
	}
	defer func() { cancelWorkers(); wg.Wait() }()
	for writer := 0; writer < 3; writer++ {
		launch(func(ctx context.Context) error {
			i := writer
			var body map[string]any
			var version uint64
			ticker := time.NewTicker(200 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return nil
				case <-ticker.C:
				}
				if body == nil {
					version = w.floor()[i] + 1
					body = w.body(i, version)
				}
				_, e := w.edit(ctx, i, version, body)
				if e == nil {
					w.mu.Lock()
					w.versions[i] = version
					w.mu.Unlock()
					i = (i + 3) % 12
					body = nil
				}
				if ctx.Err() != nil {
					return nil
				}
				if e = w.count("edit", e, writer); e != nil {
					return e
				}
			}
		})
	}
	for _, path := range []string{"/channel/messagesync", "/messages", "/conversation/list", "/conversation/sync", "delta", "idempotency"} {
		launch(func(ctx context.Context) error {
			ticker := time.NewTicker(200 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return nil
				case <-ticker.C:
				}
				var e error
				switch path {
				case "delta":
					e = w.delta(ctx, w.addr())
				case "idempotency":
					var b []byte
					b, _, e = w.call(ctx, w.addr(), "/message/update", w.seedBody)
					if e == nil && !bytes.Equal(b, w.seedReply) {
						e = fmt.Errorf("historical idempotency changed result")
					}
				default:
					e = w.read(ctx, path, w.addr())
				}
				if ctx.Err() != nil {
					return nil
				}
				if e = w.count(path, e, -1); e != nil {
					return e
				}
			}
		})
	}
	wait := func(d time.Duration) {
		t.Helper()
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case e := <-failures:
			t.Fatal(e)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-timer.C:
		}
	}
	progress := func(before, after counters) {
		t.Helper()
		for i := 0; i < 3; i++ {
			require.Greater(t, after.Writers[i], before.Writers[i]+10, "each writer must progress in this phase")
		}
		for _, key := range []string{"/channel/messagesync", "/messages", "/conversation/list", "/conversation/sync", "delta", "idempotency"} {
			require.Greater(t, after.Success[key], before.Success[key]+10, "%s must progress in this phase", key)
		}
	}
	finish := func(p phase) {
		p.Finished = time.Now().UTC()
		p.Counts = w.snapshot()
		r.Phases = append(r.Phases, p)
		b, _ := json.MarshalIndent(r, "", "  ")
		require.NoError(t, os.WriteFile(output, b, 0644))
		t.Logf("phase=%s victim=%d leader=%d->%d writes=%v more=%d transient_kinds=%d", p.Name, p.Victim, p.AuthorityBefore, p.AuthorityAfter, p.Counts.Writers, p.Counts.More, len(p.Counts.Temporary))
	}
	p := phase{Name: "healthy", Started: time.Now().UTC()}
	wait(60 * time.Second)
	finish(p)
	for _, authority := range []string{"channel", "slot"} {
		meta := suite.RequireChannelRuntimeMetaEventually(t, c, &c.Nodes[0], room, 2, 15*time.Second)
		victim := meta.Leader
		if authority == "slot" {
			victim = meta.SlotLeader
		}
		require.NotZero(t, victim)
		before := w.snapshot()
		p = phase{Name: authority + "_leader_down", Started: time.Now().UTC(), Victim: victim, AuthorityBefore: victim}
		w.fault.Store(true)
		w.victim.Store(victim)
		require.NoError(t, c.MustNode(victim).Process.Cmd.Process.Kill())
		require.NoError(t, c.MustNode(victim).Stop())
		wait(45 * time.Second)
		for _, n := range c.Nodes {
			if n.Spec.ID != victim {
				meta, err = suite.GetChannelRuntimeMeta(ctx, &n, room, 2)
				require.NoError(t, err)
				break
			}
		}
		p.AuthorityAfter = meta.Leader
		if authority == "slot" {
			p.AuthorityAfter = meta.SlotLeader
		}
		require.NotZero(t, p.AuthorityAfter)
		require.NotEqual(t, victim, p.AuthorityAfter)
		after := w.snapshot()
		progress(before, after)
		finish(p)
		p = phase{Name: authority + "_leader_restored", Started: time.Now().UTC(), Victim: victim}
		require.NoError(t, c.StartStoppedNode(victim))
		require.NoError(t, c.WaitHTTPReady(ctx))
		w.victim.Store(0)
		wait(45 * time.Second)
		progress(after, w.snapshot())
		finish(p)
	}
	cancelWorkers()
	wg.Wait()
	select {
	case e := <-failures:
		t.Fatal(e)
	default:
	}
	// Resolve any canceled in-flight edit by exact retry before freezing final state.
	for i, v := range w.floor() {
		_, e := w.edit(ctx, i, v+1, w.body(i, v+1))
		require.NoError(t, e)
		w.mu.Lock()
		w.versions[i] = v + 1
		w.mu.Unlock()
	}
	for i := 0; i < 100; i++ {
		require.NoError(t, w.delta(ctx, w.addr()))
		w.mu.Lock()
		matched := len(w.cached) == 12
		for j, m := range w.originals {
			matched = matched && w.cached[uint64(m.ID)] == w.versions[j]
		}
		w.mu.Unlock()
		if matched {
			break
		}
		require.Less(t, i, 99, "delta cursor missed final edits")
	}
	for _, n := range c.Nodes {
		for _, path := range []string{"/channel/messagesync", "/messages", "/conversation/list", "/conversation/sync"} {
			require.NoError(t, w.read(ctx, path, n.APIAddr()))
		}
	}
	w.mu.Lock()
	cursor := w.cursor
	w.mu.Unlock()
	raw, _, err = w.call(ctx, w.addr(), "/channel/messageupdates", map[string]any{"login_uid": reader, "channel_id": room, "channel_type": 2, "update_cursor": cursor, "limit": 2})
	require.NoError(t, err)
	var final deltaPage
	require.NoError(t, json.Unmarshal(raw, &final))
	require.False(t, final.Reset)
	require.False(t, final.More)
	require.Empty(t, final.Updates)
	require.Greater(t, w.snapshot().More, uint64(0))
	r.FinalVersions = w.floor()
	r.Complete = true
}
