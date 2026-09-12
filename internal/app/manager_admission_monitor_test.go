package app

import (
	"context"
	"encoding/json"
	accessmanager "github.com/WuKongIM/WuKongIM/internal/access/manager"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// Keep executable PromQL fixtures tied to the query actually sent by Manager.
func TestManagerRuntimeAdmissionPromQLFixtureMatchesProduction(t *testing.T) {
	data, err := os.ReadFile("testdata/runtime_pool_admission.promql.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Tests []struct {
			Queries []struct {
				Expr string `json:"expr"`
			} `json:"promql_expr_test"`
		} `json:"tests"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	def := requireMonitorDefinitionForTest(t, "runtimePoolAdmissionErrorRate")
	if len(fixture.Tests) < 7 {
		t.Fatal("missing semantic PromQL coverage")
	}
	for _, test := range fixture.Tests {
		for _, query := range test.Queries {
			if actual := def.query("2m"); actual != query.Expr {
				t.Fatalf("production query differs from executable fixture:\n%s", actual)
			}
		}
	}
}

func TestManagerRuntimeAdmissionCardStatus(t *testing.T) {
	for _, tc := range []struct {
		name, result, tone string
		available          bool
	}{
		{"observed zero", `[{"metric":{},"values":[[120,"0"]]}]`, accessmanager.RealtimeMonitorToneNormal, true},
		{"real error", `[{"metric":{"component":"slot","result":"full"},"values":[[120,"2"]]}]`, accessmanager.RealtimeMonitorToneCritical, true},
		{"no samples", `[]`, accessmanager.RealtimeMonitorToneCritical, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"status":"success","data":{"resultType":"matrix","result":` + tc.result + `}}`))}, nil
			})}
			provider := newManagerPrometheusMonitorProvider(managerPrometheusMonitorOptions{Enabled: true, BaseURL: "http://prometheus.invalid", Client: client})
			def := requireMonitorDefinitionForTest(t, "runtimePoolAdmissionErrorRate")
			result := provider.businessMonitorCard(context.Background(), def, "2m", time.Unix(0, 0), time.Unix(120, 0), 15*time.Second, 0)
			if result.card.Available != tc.available || result.card.Tone != tc.tone {
				t.Fatalf("card available=%v tone=%s; want %v %s", result.card.Available, result.card.Tone, tc.available, tc.tone)
			}
			if !tc.available && result.card.Error == "" {
				t.Fatal("missing source must remain explicit")
			}
		})
	}
}
