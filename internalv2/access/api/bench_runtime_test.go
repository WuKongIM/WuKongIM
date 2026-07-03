package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type fakeChannelRuntimeBenchController struct {
	snapshotQuery model.ChannelRuntimeQuery
	probeQuery    model.ChannelRuntimeQuery
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

func (f *fakeChannelRuntimeBenchController) Probe(_ context.Context, query model.ChannelRuntimeQuery) (model.ChannelRuntimeProbeResult, error) {
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
	logger := newRecordingAPILogger("internalv2.access.api")
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
	requireAPILogEntry(t, logger, "ERROR", "internalv2.access.api.http", "internalv2.access.api.bench_runtime_failed")
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
