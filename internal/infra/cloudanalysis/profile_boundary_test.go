package cloudanalysis

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	analysis "github.com/WuKongIM/WuKongIM/internal/usecase/cloudanalysis"
	pprofprofile "github.com/google/pprof/profile"
)

func TestProfileCaptureUsesOnlyAllowlistedProfileEndpoints(t *testing.T) {
	baseURL, err := url.Parse("http://node-1.test/root/")
	if err != nil {
		t.Fatal(err)
	}
	data := encodedProfile(t)
	start := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	end := start.Add(7 * time.Second)
	tests := []struct {
		name      string
		request   analysis.ProfileCaptureRequest
		wantPath  string
		wantQuery string
	}{
		{name: "cpu", request: analysis.ProfileCaptureRequest{NodeID: 1, Kind: analysis.ProfileCPU, Seconds: 7}, wantPath: "/root/debug/pprof/profile", wantQuery: "seconds=7"},
		{name: "heap", request: analysis.ProfileCaptureRequest{NodeID: 1, Kind: analysis.ProfileHeap}, wantPath: "/root/debug/pprof/heap", wantQuery: "debug=0"},
		{name: "goroutine", request: analysis.ProfileCaptureRequest{NodeID: 1, Kind: analysis.ProfileGoroutine}, wantPath: "/root/debug/pprof/goroutine", wantQuery: "debug=0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			call := 0
			store := newProfileStore(profileStoreConfig{
				nodeURLs: map[uint64]*url.URL{1: baseURL},
				client: &http.Client{Transport: memoryRoundTripper(func(request *http.Request) (*http.Response, error) {
					if request.Method != http.MethodGet || request.URL.Path != test.wantPath || request.URL.RawQuery != test.wantQuery {
						t.Errorf("profile request = %s %s", request.Method, request.URL.String())
					}
					return jsonHTTPResponse(http.StatusOK, string(data)), nil
				})},
				now: func() time.Time {
					call++
					if call == 1 {
						return start
					}
					return end
				},
			})
			result, err := store.capture(context.Background(), test.request)
			if err != nil {
				t.Fatalf("capture() error = %v", err)
			}
			metadata, ok := result.Data.(ProfileMetadata)
			if !ok || metadata.NodeID != 1 || metadata.Kind != test.request.Kind || metadata.SizeBytes != len(data) ||
				!metadata.Window.Start.Equal(start) || !metadata.Window.End.Equal(end) || result.Node != "node-1" {
				t.Fatalf("capture() result = %#v", result)
			}
		})
	}
}

func TestProfileCaptureFailsClosedOnNodeAndResponseErrors(t *testing.T) {
	baseURL, err := url.Parse("http://node-1.test")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		nodeURLs  map[uint64]*url.URL
		kind      analysis.ProfileKind
		roundTrip func(*http.Request) (*http.Response, error)
		want      string
		wantInput bool
	}{
		{name: "unknown node", nodeURLs: map[uint64]*url.URL{}, kind: analysis.ProfileHeap, wantInput: true},
		{name: "unknown kind", nodeURLs: map[uint64]*url.URL{1: baseURL}, kind: analysis.ProfileKind("mutex"), wantInput: true},
		{
			name: "transport failure", nodeURLs: map[uint64]*url.URL{1: baseURL}, kind: analysis.ProfileHeap,
			roundTrip: func(*http.Request) (*http.Response, error) { return nil, errors.New("node unavailable") },
			want:      "node unavailable",
		},
		{
			name: "HTTP failure", nodeURLs: map[uint64]*url.URL{1: baseURL}, kind: analysis.ProfileHeap,
			roundTrip: func(*http.Request) (*http.Response, error) {
				return jsonHTTPResponse(http.StatusForbidden, "  profile denied  "), nil
			},
			want: "status 403: profile denied",
		},
		{
			name: "body read failure", nodeURLs: map[uint64]*url.URL{1: baseURL}, kind: analysis.ProfileHeap,
			roundTrip: func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: failingReadCloser{err: errors.New("profile read failed")}}, nil
			},
			want: "profile read failed",
		},
		{
			name: "malformed profile", nodeURLs: map[uint64]*url.URL{1: baseURL}, kind: analysis.ProfileHeap,
			roundTrip: func(*http.Request) (*http.Response, error) {
				return jsonHTTPResponse(http.StatusOK, "not a profile"), nil
			},
			want: "invalid profile",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport := test.roundTrip
			if transport == nil {
				transport = func(*http.Request) (*http.Response, error) {
					t.Fatal("unexpected HTTP request")
					return nil, nil
				}
			}
			store := newProfileStore(profileStoreConfig{
				nodeURLs: test.nodeURLs,
				client:   &http.Client{Transport: memoryRoundTripper(transport)},
				now:      time.Now,
			})
			_, err := store.capture(context.Background(), analysis.ProfileCaptureRequest{NodeID: 1, Kind: test.kind})
			if test.wantInput {
				if !errors.Is(err, analysis.ErrInvalidToolInput) {
					t.Fatalf("capture() error = %v, want ErrInvalidToolInput", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("capture() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestProfileStoreEvictsOldestCapturesWithinCountAndByteBudgets(t *testing.T) {
	store := newProfileStore(profileStoreConfig{
		nodeURLs: map[uint64]*url.URL{}, client: &http.Client{}, now: time.Now, maxProfiles: 3, maxBytes: 5,
	})
	profile := func(id string, nodeID uint64, data string) storedProfile {
		return storedProfile{metadata: ProfileMetadata{ProfileID: id, NodeID: nodeID}, data: []byte(data)}
	}
	if !store.store(profile("a", 1, "aaa")) || !store.store(profile("b", 2, "bb")) || !store.store(profile("c", 1, "cc")) {
		t.Fatal("store rejected captures within the configured budget")
	}
	if _, exists := store.profiles["a"]; exists || store.totalBytes != 4 || strings.Join(store.order, ",") != "b,c" {
		t.Fatalf("byte eviction state order=%v total=%d profiles=%v", store.order, store.totalBytes, store.profiles)
	}
	if !store.store(profile("d", 2, "d")) || !store.store(profile("e", 1, "e")) {
		t.Fatal("store rejected count-bounded captures")
	}
	if _, exists := store.profiles["b"]; exists || store.totalBytes != 4 || strings.Join(store.order, ",") != "c,d,e" {
		t.Fatalf("count eviction state order=%v total=%d profiles=%v", store.order, store.totalBytes, store.profiles)
	}
	if store.store(profile("oversized", 1, "123456")) {
		t.Fatal("store accepted one capture larger than its total byte budget")
	}

	cluster := store.list(analysis.ProfileListRequest{Limit: 2})
	clusterItems := cluster.Data.([]ProfileMetadata)
	if cluster.Node != "cluster" || len(clusterItems) != 2 || clusterItems[0].ProfileID != "e" || clusterItems[1].ProfileID != "d" {
		t.Fatalf("cluster list = %#v", cluster)
	}
	nodeTwo := store.list(analysis.ProfileListRequest{NodeID: 2, Limit: 2})
	nodeItems := nodeTwo.Data.([]ProfileMetadata)
	if nodeTwo.Node != "node-2" || len(nodeItems) != 1 || nodeItems[0].ProfileID != "d" {
		t.Fatalf("node list = %#v", nodeTwo)
	}
}

func TestProfileTopRejectsUnknownCorruptAndUnsupportedCaptures(t *testing.T) {
	store := newProfileStore(profileStoreConfig{
		nodeURLs: map[uint64]*url.URL{}, client: &http.Client{}, now: time.Now,
	})
	if _, err := store.top(analysis.ProfileTopRequest{ProfileID: "missing", Limit: 10}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing top error = %v", err)
	}
	store.store(storedProfile{metadata: ProfileMetadata{ProfileID: "corrupt", NodeID: 1}, data: []byte("not a profile")})
	if _, err := store.top(analysis.ProfileTopRequest{ProfileID: "corrupt", Limit: 10}); err == nil {
		t.Fatal("corrupt top error = nil")
	}
	store.store(storedProfile{metadata: ProfileMetadata{ProfileID: "heap", NodeID: 1}, data: encodedProfile(t)})
	if _, err := store.top(analysis.ProfileTopRequest{
		ProfileID: "heap", Limit: 10, SampleType: analysis.ProfileSampleType("objects"),
	}); !errors.Is(err, analysis.ErrInvalidToolInput) {
		t.Fatalf("unsupported sample type error = %v", err)
	}
}

func TestSummarizeProfileDeduplicatesStacksAndOrdersTies(t *testing.T) {
	if rows, err := summarizeProfile(nil, 10, ""); err != nil || len(rows) != 0 {
		t.Fatalf("summarize nil = %#v, %v", rows, err)
	}
	functionA := &pprofprofile.Function{ID: 1, Name: "alpha"}
	functionB := &pprofprofile.Function{ID: 2, Name: "beta"}
	functionZ := &pprofprofile.Function{ID: 3, Name: "zeta"}
	location := func(id uint64, function *pprofprofile.Function) *pprofprofile.Location {
		return &pprofprofile.Location{ID: id, Line: []pprofprofile.Line{{Function: function}}}
	}
	profile := &pprofprofile.Profile{
		SampleType: []*pprofprofile.ValueType{{Type: "samples", Unit: "count"}},
		Sample: []*pprofprofile.Sample{
			{Value: []int64{10}, Location: []*pprofprofile.Location{location(1, functionZ), location(2, functionA), location(3, functionA)}},
			{Value: []int64{10}, Location: []*pprofprofile.Location{location(4, functionB)}},
			{Value: []int64{}, Location: []*pprofprofile.Location{location(5, functionA)}},
			{Value: []int64{99}, Location: []*pprofprofile.Location{{ID: 6, Line: []pprofprofile.Line{{Function: nil}, {Function: &pprofprofile.Function{}}}}}},
		},
	}
	rows, err := summarizeProfile(profile, 10, "samples")
	if err != nil {
		t.Fatalf("summarizeProfile() error = %v", err)
	}
	if len(rows) != 3 || rows[0].Function != "alpha" || rows[0].Flat != 0 || rows[0].Cumulative != 10 ||
		rows[1].Function != "beta" || rows[1].Flat != 10 || rows[2].Function != "zeta" || rows[2].Flat != 10 {
		t.Fatalf("rows = %#v", rows)
	}
	limited, err := summarizeProfile(profile, 2, "samples")
	if err != nil || len(limited) != 2 || limited[0].Function != "alpha" || limited[1].Function != "beta" {
		t.Fatalf("limited rows = %#v, %v", limited, err)
	}
}
