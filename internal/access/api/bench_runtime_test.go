package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type fakeChannelRuntimeBenchController struct {
	snapshotQuery model.ChannelRuntimeQuery
	probeQuery    model.ChannelRuntimeProbeQuery
	evictQuery    model.ChannelRuntimeQuery
	snapshotErr   error
	probeErr      error
	evictErr      error
}

func (f *fakeChannelRuntimeBenchController) Snapshot(_ context.Context, query model.ChannelRuntimeQuery) (model.ChannelRuntimeSnapshot, error) {
	f.snapshotQuery = query
	if f.snapshotErr != nil {
		return model.ChannelRuntimeSnapshot{}, f.snapshotErr
	}
	return model.ChannelRuntimeSnapshot{
		NodeID:       1,
		RunID:        query.RunID,
		Profile:      query.Profile,
		ActiveTotal:  7,
		ActiveLeader: 4,
		Reactors: []model.ChannelRuntimeReactorSnapshot{
			{ReactorID: 2, Leader: 4, Follower: 3, Parked: 1, MailboxDepth: 9},
		},
	}, nil
}

func (f *fakeChannelRuntimeBenchController) Probe(_ context.Context, query model.ChannelRuntimeProbeQuery) (model.ChannelRuntimeProbeResult, error) {
	f.probeQuery = query
	if f.probeErr != nil {
		return model.ChannelRuntimeProbeResult{}, f.probeErr
	}
	return model.ChannelRuntimeProbeResult{
		NodeID:         1,
		RunID:          query.RunID,
		Profile:        query.Profile,
		Checked:        query.Range.End - query.Range.Start,
		LoadedLeader:   2,
		LoadedFollower: 1,
		Missing:        []string{"bench-g-4"},
	}, nil
}

func (f *fakeChannelRuntimeBenchController) Evict(_ context.Context, query model.ChannelRuntimeQuery) (model.ChannelRuntimeEvictResult, error) {
	f.evictQuery = query
	if f.evictErr != nil {
		return model.ChannelRuntimeEvictResult{}, f.evictErr
	}
	return model.ChannelRuntimeEvictResult{
		NodeID:      1,
		RunID:       query.RunID,
		Profile:     query.Profile,
		Requested:   query.Range.End - query.Range.Start,
		Evicted:     3,
		SkippedBusy: 1,
		Missing:     2,
	}, nil
}

func TestBenchCapabilitiesAdvertiseChannelRuntimeWhenControllerConfigured(t *testing.T) {
	srv := New(Options{BenchEnabled: true, BenchRuntime: &fakeChannelRuntimeBenchController{}})
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)

	var caps capabilitiesResponse
	resp, err := http.Get(httpSrv.URL + "/bench/v1/capabilities")
	decodeJSON(t, resp, err, &caps)

	if !caps.Supports.ChannelRuntimeSnapshot || !caps.Supports.ChannelRuntimeProbe || !caps.Supports.ChannelRuntimeEvict {
		t.Fatalf("channel runtime supports = %+v, want snapshot/probe/evict enabled", caps.Supports)
	}
	if caps.Supports.ChannelRuntimeFaults || caps.Supports.ChannelRuntimeActivate {
		t.Fatalf("channel runtime unsupported features = %+v, want faults/activate disabled", caps.Supports)
	}
}

func TestBenchChannelRuntimeSnapshot(t *testing.T) {
	controller := &fakeChannelRuntimeBenchController{}
	srv := New(Options{BenchEnabled: true, BenchRuntime: controller})
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)

	var aggregate model.ChannelRuntimeSnapshot
	resp, err := http.Get(httpSrv.URL + "/bench/v1/channel-runtime/snapshot")
	decodeJSON(t, resp, err, &aggregate)
	if controller.snapshotQuery != (model.ChannelRuntimeQuery{}) {
		t.Fatalf("aggregate snapshot query = %+v, want zero query", controller.snapshotQuery)
	}

	var snap model.ChannelRuntimeSnapshot
	resp, err = http.Get(httpSrv.URL + "/bench/v1/channel-runtime/snapshot?run_id=run-1&profile=wide&channel_type=2&start=10&end=15")
	decodeJSON(t, resp, err, &snap)

	if got, want := snap.Version, versionV1; got != want {
		t.Fatalf("version = %q, want %q", got, want)
	}
	if got, want := snap.ActiveTotal, 7; got != want {
		t.Fatalf("active_total = %d, want %d", got, want)
	}
	wantQuery := model.ChannelRuntimeQuery{
		RunID:       "run-1",
		Profile:     "wide",
		ChannelType: 2,
		Range:       model.ChannelRuntimeRange{Start: 10, End: 15},
	}
	if controller.snapshotQuery != wantQuery {
		t.Fatalf("snapshot query = %+v, want %+v", controller.snapshotQuery, wantQuery)
	}
}

func TestBenchChannelRuntimeSnapshotRejectsInvalidSelector(t *testing.T) {
	srv := New(Options{BenchEnabled: true, BenchRuntime: &fakeChannelRuntimeBenchController{}})
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)

	resp, err := http.Get(httpSrv.URL + "/bench/v1/channel-runtime/snapshot?start=10&end=20")
	requireStatus(t, resp, err, http.StatusBadRequest)
	resp, err = http.Get(httpSrv.URL + "/bench/v1/channel-runtime/snapshot?run_id=run-1&profile=wide&channel_type=nope&start=10&end=20")
	requireStatus(t, resp, err, http.StatusBadRequest)
}

func TestBenchChannelRuntimeProbeRejectsInvalidRange(t *testing.T) {
	srv := New(Options{BenchEnabled: true, BenchRuntime: &fakeChannelRuntimeBenchController{}})
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)

	postJSON(t, httpSrv.URL+"/bench/v1/channel-runtime/probe", `{"run_id":"run-1","profile":"wide","channel_type":2,"range":{"start":10,"end":10}}`, http.StatusBadRequest)
	postJSON(t, httpSrv.URL+"/bench/v1/channel-runtime/probe", `{"run_id":"run-1","profile":"wide","channel_type":2,"range":{"start":0,"end":100001}}`, http.StatusBadRequest)
	postJSON(t, httpSrv.URL+"/bench/v1/channel-runtime/probe", `{"run_id":"run-1","profile":"wide","channel_type":2,"range":{"start":-1,"end":1}}`, http.StatusBadRequest)
}

func TestBenchChannelRuntimeProbeRejectsMissingSelectorFields(t *testing.T) {
	srv := New(Options{BenchEnabled: true, BenchRuntime: &fakeChannelRuntimeBenchController{}})
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)

	postJSON(t, httpSrv.URL+"/bench/v1/channel-runtime/probe", `{"profile":"wide","channel_type":2,"range":{"start":0,"end":1}}`, http.StatusBadRequest)
	postJSON(t, httpSrv.URL+"/bench/v1/channel-runtime/probe", `{"run_id":"run-1","channel_type":2,"range":{"start":0,"end":1}}`, http.StatusBadRequest)
	postJSON(t, httpSrv.URL+"/bench/v1/channel-runtime/probe", `{"run_id":"run-1","profile":"wide","range":{"start":0,"end":1}}`, http.StatusBadRequest)
}

func TestBenchChannelRuntimeProbeRejectsStrictJSONViolations(t *testing.T) {
	srv := New(Options{BenchEnabled: true, BenchRuntime: &fakeChannelRuntimeBenchController{}})
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)

	postJSON(t, httpSrv.URL+"/bench/v1/channel-runtime/probe", `{"run_id":"run-1","profile":"wide","channel_type":2,"range":{"start":0,"end":1},"unknown":true}`, http.StatusBadRequest)
	postJSON(t, httpSrv.URL+"/bench/v1/channel-runtime/probe", `{"run_id":"run-1","profile":"wide","channel_type":2,"range":{"start":0,"end":1}} {}`, http.StatusBadRequest)
}

func TestBenchChannelRuntimeProbeValidatesExplicitSelector(t *testing.T) {
	controller := &fakeChannelRuntimeBenchController{}
	srv := New(Options{BenchEnabled: true, BenchRuntime: controller})
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)

	channels := func(count int) []map[string]any {
		out := make([]map[string]any, 0, count)
		for i := 0; i < count; i++ {
			out = append(out, map[string]any{
				"channel_id":   fmt.Sprintf("person-%04d", i),
				"channel_type": 1,
			})
		}
		return out
	}

	tests := []struct {
		name string
		body map[string]any
	}{
		{name: "neither selector", body: map[string]any{}},
		{name: "both selectors", body: map[string]any{
			"run_id": "run-a", "profile": "person", "channel_type": 1,
			"range":    map[string]any{"start": 0, "end": 1},
			"channels": channels(1),
		}},
		{name: "empty explicit selector", body: map[string]any{"channels": []any{}}},
		{name: "over explicit selector bound", body: map[string]any{"channels": channels(1201)}},
		{name: "duplicate identity", body: map[string]any{"channels": []map[string]any{
			{"channel_id": "same", "channel_type": 1},
			{"channel_id": "same", "channel_type": 1},
		}}},
		{name: "empty id", body: map[string]any{"channels": []map[string]any{{"channel_id": "", "channel_type": 1}}}},
		{name: "whitespace id", body: map[string]any{"channels": []map[string]any{{"channel_id": " \t ", "channel_type": 1}}}},
		{name: "zero type", body: map[string]any{"channels": []map[string]any{{"channel_id": "person-a", "channel_type": 0}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.body)
			if err != nil {
				t.Fatal(err)
			}
			postJSON(t, httpSrv.URL+"/bench/v1/channel-runtime/probe", string(data), http.StatusBadRequest)
		})
	}

	for _, count := range []int{1, 1200} {
		t.Run(fmt.Sprintf("valid %d", count), func(t *testing.T) {
			data, err := json.Marshal(map[string]any{"channels": channels(count)})
			if err != nil {
				t.Fatal(err)
			}
			postJSON(t, httpSrv.URL+"/bench/v1/channel-runtime/probe", string(data), http.StatusOK)
			if got := len(controller.probeQuery.Channels); got != count {
				t.Fatalf("explicit query channels = %d, want %d", got, count)
			}
			if got := controller.probeQuery.Channels[0].ChannelID; got != "person-0000" {
				t.Fatalf("first channel id = %q, want unchanged identity", got)
			}
		})
	}
}

func TestBenchChannelRuntimeProbeGeneratedSelectorRegression(t *testing.T) {
	controller := &fakeChannelRuntimeBenchController{}
	srv := New(Options{BenchEnabled: true, BenchRuntime: controller})
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)

	postJSON(t, httpSrv.URL+"/bench/v1/channel-runtime/probe", `{"run_id":"run-1","profile":"wide","channel_type":2,"range":{"start":4,"end":6}}`, http.StatusOK)
	want := model.ChannelRuntimeProbeQuery{
		RunID:       "run-1",
		Profile:     "wide",
		ChannelType: 2,
		Range:       model.ChannelRuntimeRange{Start: 4, End: 6},
	}
	if !reflect.DeepEqual(controller.probeQuery, want) {
		t.Fatalf("generated probe query = %+v, want %+v", controller.probeQuery, want)
	}
}

func TestBenchChannelRuntimeEvictRejectsExplicitChannels(t *testing.T) {
	controller := &fakeChannelRuntimeBenchController{}
	srv := New(Options{BenchEnabled: true, BenchRuntime: controller})
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)

	postJSON(t, httpSrv.URL+"/bench/v1/channel-runtime/evict", `{"channels":[{"channel_id":"person-a","channel_type":1}]}`, http.StatusBadRequest)
	if controller.evictQuery != (model.ChannelRuntimeQuery{}) {
		t.Fatalf("evict query = %+v, want no control call", controller.evictQuery)
	}
}

func TestBenchChannelRuntimeEvict(t *testing.T) {
	controller := &fakeChannelRuntimeBenchController{}
	srv := New(Options{BenchEnabled: true, BenchRuntime: controller})
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)

	var result model.ChannelRuntimeEvictResult
	resp, err := http.Post(httpSrv.URL+"/bench/v1/channel-runtime/evict", "application/json", strings.NewReader(`{"run_id":"run-1","profile":"wide","channel_type":2,"range":{"start":3,"end":9}}`))
	decodeJSON(t, resp, err, &result)

	if got, want := result.Version, versionV1; got != want {
		t.Fatalf("version = %q, want %q", got, want)
	}
	if got, want := result.Evicted, 3; got != want {
		t.Fatalf("evicted = %d, want %d", got, want)
	}
	wantQuery := model.ChannelRuntimeQuery{
		RunID:       "run-1",
		Profile:     "wide",
		ChannelType: 2,
		Range:       model.ChannelRuntimeRange{Start: 3, End: 9},
	}
	if controller.evictQuery != wantQuery {
		t.Fatalf("evict query = %+v, want %+v", controller.evictQuery, wantQuery)
	}
}

func TestBenchChannelRuntimeRoutesDisabledWithoutBenchAPI(t *testing.T) {
	srv := New(Options{BenchRuntime: &fakeChannelRuntimeBenchController{}})
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)

	resp, err := http.Get(httpSrv.URL + "/bench/v1/channel-runtime/snapshot")
	requireStatus(t, resp, err, http.StatusNotFound)
}

func TestBenchChannelRuntimeRoutesUnavailableWithoutController(t *testing.T) {
	srv := New(Options{BenchEnabled: true})
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)

	resp, err := http.Get(httpSrv.URL + "/bench/v1/channel-runtime/snapshot")
	requireStatus(t, resp, err, http.StatusNotImplemented)
	postJSON(t, httpSrv.URL+"/bench/v1/channel-runtime/probe", `{"run_id":"run-1","profile":"wide","channel_type":2,"range":{"start":0,"end":1}}`, http.StatusNotImplemented)
	postJSON(t, httpSrv.URL+"/bench/v1/channel-runtime/evict", `{"run_id":"run-1","profile":"wide","channel_type":2,"range":{"start":0,"end":1}}`, http.StatusNotImplemented)
}

func TestBenchChannelRuntimeControllerFailureReturnsInternalServerError(t *testing.T) {
	logger := newRecordingAPILogger("internal.access.api")
	srv := New(Options{
		BenchEnabled: true,
		Logger:       logger,
		BenchRuntime: &fakeChannelRuntimeBenchController{
			probeErr: errors.New("runtime probe failed"),
		},
	})
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)

	postJSON(t, httpSrv.URL+"/bench/v1/channel-runtime/probe", `{"run_id":"run-1","profile":"wide","channel_type":2,"range":{"start":0,"end":1}}`, http.StatusInternalServerError)
	requireAPILogEntry(t, logger, "ERROR", "internal.access.api.http", "internal.access.api.bench_runtime_failed")
}

func TestBenchChannelRuntimeExplicitProbeFailureDoesNotExposeControllerError(t *testing.T) {
	const channelID = "canonical-sensitive-person"
	const tokenLikeValue = "probe-secret-value"
	logger := newRecordingAPILogger("internal.access.api")
	srv := New(Options{
		BenchEnabled: true,
		Logger:       logger,
		BenchRuntime: &fakeChannelRuntimeBenchController{
			probeErr: &model.ChannelRuntimeProbeFailure{
				Reason: model.ChannelRuntimeProbeFailureInvalidEvidence,
				Cause:  fmt.Errorf("runtime probe failed for %s using %s", channelID, tokenLikeValue),
			},
		},
	})
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)

	resp, err := http.Post(
		httpSrv.URL+"/bench/v1/channel-runtime/probe",
		"application/json",
		strings.NewReader(`{"channels":[{"channel_id":"`+channelID+`","channel_type":1}]}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusInternalServerError)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "explicit channel runtime probe failed") {
		t.Fatalf("response = %s, want stable explicit probe failure", body)
	}
	for _, sensitive := range []string{channelID, tokenLikeValue} {
		if strings.Contains(string(body), sensitive) {
			t.Fatalf("response exposed sensitive controller error value %q", sensitive)
		}
	}
	entry := requireAPILogEntry(t, logger, "ERROR", "internal.access.api.http", "internal.access.api.bench_runtime_failed")
	foundReason := false
	for _, field := range entry.fields {
		if field.Key == "reason" && field.Value == string(model.ChannelRuntimeProbeFailureInvalidEvidence) {
			foundReason = true
		}
		for _, sensitive := range []string{channelID, tokenLikeValue} {
			if strings.Contains(fmt.Sprint(field.Value), sensitive) {
				t.Fatalf("log field %q exposed sensitive controller error value %q", field.Key, sensitive)
			}
		}
	}
	if !foundReason {
		t.Fatalf("log fields = %#v, want invalid_evidence reason", entry.fields)
	}
}

type recordedAPILogEntry struct {
	level  string
	module string
	fields []wklog.Field
}

type recordingAPILogger struct {
	module  string
	base    []wklog.Field
	entries *[]recordedAPILogEntry
}

func newRecordingAPILogger(module string) *recordingAPILogger {
	entries := make([]recordedAPILogEntry, 0)
	return &recordingAPILogger{module: module, entries: &entries}
}

func (r *recordingAPILogger) Debug(msg string, fields ...wklog.Field) { r.log("DEBUG", fields...) }
func (r *recordingAPILogger) Info(msg string, fields ...wklog.Field)  { r.log("INFO", fields...) }
func (r *recordingAPILogger) Warn(msg string, fields ...wklog.Field)  { r.log("WARN", fields...) }
func (r *recordingAPILogger) Error(msg string, fields ...wklog.Field) { r.log("ERROR", fields...) }
func (r *recordingAPILogger) Fatal(msg string, fields ...wklog.Field) { r.log("FATAL", fields...) }

func (r *recordingAPILogger) Named(name string) wklog.Logger {
	module := name
	if r.module != "" && name != "" {
		module = r.module + "." + name
	}
	return &recordingAPILogger{module: module, base: append([]wklog.Field(nil), r.base...), entries: r.entries}
}

func (r *recordingAPILogger) With(fields ...wklog.Field) wklog.Logger {
	base := append(append([]wklog.Field(nil), r.base...), fields...)
	return &recordingAPILogger{module: r.module, base: base, entries: r.entries}
}

func (r *recordingAPILogger) Sync() error { return nil }

func (r *recordingAPILogger) log(level string, fields ...wklog.Field) {
	all := append(append([]wklog.Field(nil), r.base...), fields...)
	*r.entries = append(*r.entries, recordedAPILogEntry{level: level, module: r.module, fields: all})
}

func requireAPILogEntry(t *testing.T, logger *recordingAPILogger, level, module, event string) recordedAPILogEntry {
	t.Helper()
	for _, entry := range *logger.entries {
		if entry.level != level || entry.module != module {
			continue
		}
		for _, field := range entry.fields {
			if field.Key == "event" && field.Value == event {
				return entry
			}
		}
	}
	t.Fatalf("missing api log level=%s module=%s event=%s entries=%#v", level, module, event, *logger.entries)
	return recordedAPILogEntry{}
}
