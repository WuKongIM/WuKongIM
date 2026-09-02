package top

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	accessapi "github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/top/topapi"
)

type topRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn topRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestNormalizeConfigAppliesOperatorDefaultsAndCleansServers(t *testing.T) {
	originalServers := []string{" http://node-1:5001 ", "", " https://node-2/base "}

	got, err := normalizeConfig(config{
		Servers:     originalServers,
		View:        "  all  ",
		AlertFilter: " channel/pressure_high ",
	})
	if err != nil {
		t.Fatalf("normalize config: %v", err)
	}
	if got.Window != defaultWindow || got.Interval != defaultInterval || got.Limit != defaultLimit {
		t.Fatalf("defaults = window %s interval %s limit %d", got.Window, got.Interval, got.Limit)
	}
	if got.View != "all" || got.AlertFilter != "channel/pressure_high" {
		t.Fatalf("trimmed selectors = view %q alert %q", got.View, got.AlertFilter)
	}
	if len(got.Servers) != 2 || got.Servers[0] != "http://node-1:5001" || got.Servers[1] != "https://node-2/base" {
		t.Fatalf("cleaned servers = %#v", got.Servers)
	}
	if originalServers[0] != " http://node-1:5001 " {
		t.Fatalf("normalization mutated caller-owned server list: %#v", originalServers)
	}

	defaults, err := normalizeConfig(config{View: " \t "})
	if err != nil {
		t.Fatalf("normalize defaults: %v", err)
	}
	if defaults.View != defaultView {
		t.Fatalf("default view = %q, want %q", defaults.View, defaultView)
	}
}

func TestNormalizeConfigRejectsUnsafeRefreshBounds(t *testing.T) {
	tests := []struct {
		name string
		cfg  config
		want string
	}{
		{name: "window", cfg: config{Window: minWindow - time.Nanosecond}, want: "window"},
		{name: "interval", cfg: config{Interval: -time.Nanosecond}, want: "interval"},
		{name: "limit", cfg: config{Limit: -1}, want: "limit"},
		{name: "max refresh", cfg: config{MaxRefresh: -1}, want: "max-refresh"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := normalizeConfig(tt.cfg)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("normalize error = %v, want field %q", err, tt.want)
			}
		})
	}
}

func TestResolveServersPrefersExplicitStableOrder(t *testing.T) {
	got, err := resolveServers(config{Servers: []string{
		" http://node-2:5001, http://node-1:5001 ",
		"http://node-2:5001",
	}})
	if err != nil {
		t.Fatalf("resolve explicit servers: %v", err)
	}
	want := []string{"http://node-2:5001", "http://node-1:5001"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("resolved servers = %#v, want %#v", got, want)
	}
}

func TestSnapshotURLValidatesSchemeAndPreservesBaseQuery(t *testing.T) {
	for _, server := range []string{"node-1:5001", "ftp://node-1/top", "://bad"} {
		if _, err := snapshotURL(server, config{}); err == nil {
			t.Fatalf("snapshotURL(%q) unexpectedly succeeded", server)
		}
	}

	got, err := snapshotURL("https://node-1:5001/admin/?token=a%2Bb&limit=old", config{
		Window: 7 * time.Second,
		View:   "pressure",
		Limit:  13,
	})
	if err != nil {
		t.Fatalf("snapshot URL: %v", err)
	}
	for _, want := range []string{
		"https://node-1:5001/admin/top/v1/snapshot?",
		"limit=13",
		"token=a%2Bb",
		"view=pressure",
		"window=7s",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("snapshot URL %q does not contain %q", got, want)
		}
	}
}

func TestClientSnapshotDecodesResponseWithoutRealNetwork(t *testing.T) {
	var requested *http.Request
	c := &client{http: &http.Client{Transport: topRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		requested = req
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"version":"top/v1","node":{"id":9,"ready":true},"verdict":{"level":"ok"},"sources":{}}`)),
			Request:    req,
		}, nil
	})}}

	got, err := c.snapshot(context.Background(), "http://node-9:5001/base", config{
		Window: 5 * time.Second,
		View:   "all",
		Limit:  8,
	})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if got.Version != "top/v1" || got.Node.ID != 9 || !got.Node.Ready || got.Verdict.Level != "ok" {
		t.Fatalf("decoded snapshot = %#v", got)
	}
	if requested == nil || requested.Method != http.MethodGet || requested.URL.Path != "/base/top/v1/snapshot" {
		t.Fatalf("request = %#v", requested)
	}
	if requested.URL.Query().Get("window") != "5s" || requested.URL.Query().Get("view") != "all" || requested.URL.Query().Get("limit") != "8" {
		t.Fatalf("request query = %q", requested.URL.RawQuery)
	}
}

func TestClientSnapshotReportsTransportStatusAndDecodeFailures(t *testing.T) {
	tests := []struct {
		name      string
		transport topRoundTripFunc
		want      string
	}{
		{
			name: "transport",
			transport: func(*http.Request) (*http.Response, error) {
				return nil, errors.New("dial disabled")
			},
			want: "fetch top snapshot",
		},
		{
			name: "status",
			transport: func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader("busy")), Request: req}, nil
			},
			want: "status 503",
		},
		{
			name: "decode",
			transport: func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{")), Request: req}, nil
			},
			want: "decode top snapshot",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &client{http: &http.Client{Transport: tt.transport}}
			_, err := c.snapshot(context.Background(), "http://node-1:5001", config{Window: defaultWindow, View: defaultView, Limit: defaultLimit})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("snapshot error = %v, want %q", err, tt.want)
			}
		})
	}

	c := &client{http: &http.Client{Transport: topRoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("transport called for invalid URL")
		return nil, nil
	})}}
	if _, err := c.snapshot(context.Background(), "relative", config{}); err == nil {
		t.Fatal("invalid server URL unexpectedly succeeded")
	}
}

func TestRenderAlertsJSONPreservesFilteredAlertContract(t *testing.T) {
	var out strings.Builder
	snapshot := aggregate([]accessapi.TopSnapshot{sampleSnapshot()})
	if err := renderAlertsJSON(&out, snapshot, "alert-1"); err != nil {
		t.Fatalf("render alerts JSON: %v", err)
	}
	if !strings.Contains(out.String(), `"id": "alert-1"`) || strings.Contains(out.String(), `"nodes"`) {
		t.Fatalf("filtered alert JSON = %q", out.String())
	}
	if err := renderAlertsJSON(io.Discard, snapshot, "missing"); err == nil {
		t.Fatal("missing alert filter unexpectedly rendered")
	}
}
