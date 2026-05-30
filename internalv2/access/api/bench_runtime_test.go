package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/bench/model"
)

type fakeChannelRuntimeBenchController struct {
	snapshotQuery model.ChannelRuntimeQuery
	probeQuery    model.ChannelRuntimeQuery
	evictQuery    model.ChannelRuntimeQuery
}

func (f *fakeChannelRuntimeBenchController) Snapshot(_ context.Context, query model.ChannelRuntimeQuery) (model.ChannelRuntimeSnapshot, error) {
	f.snapshotQuery = query
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

func TestBenchChannelRuntimeProbeRejectsInvalidRange(t *testing.T) {
	srv := New(Options{BenchEnabled: true, BenchRuntime: &fakeChannelRuntimeBenchController{}})
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)

	postJSON(t, httpSrv.URL+"/bench/v1/channel-runtime/probe", `{"run_id":"run-1","profile":"wide","channel_type":2,"range":{"start":10,"end":10}}`, http.StatusBadRequest)
	postJSON(t, httpSrv.URL+"/bench/v1/channel-runtime/probe", `{"run_id":"run-1","profile":"wide","channel_type":2,"range":{"start":0,"end":100001}}`, http.StatusBadRequest)
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
