//go:build e2e

package medium_recipient_hotpath

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	benchmetrics "github.com/WuKongIM/WuKongIM/internal/bench/metrics"
)

// Preserve bounded raw histogram boundaries for independent per-node analysis.
// These are sampled completed stages, not an in-flight request trace. Retaining
// existing scrape results adds no HTTP work to the measured SEND window.
func logPermissionSoakReplicationStages(t *testing.T, before, after benchmetrics.PrometheusSnapshot, captureErr error) {
	if os.Getenv("WK_E2E_MEDIUM_RECIPIENT_REPLICATION_DIAGNOSTICS") != "1" {
		return
	}
	selectRows := func(snapshot benchmetrics.PrometheusSnapshot) ([]benchmetrics.PrometheusSample, bool) {
		var rows []benchmetrics.PrometheusSample
		for _, sample := range snapshot.Samples {
			if !strings.HasPrefix(sample.Name, "wukongim_channelv2_replication_stage_duration_seconds_") {
				continue
			}
			if len(rows) >= 4096 {
				return nil, true
			}
			rows = append(rows, sample)
		}
		return rows, false
	}
	start, startOverflow := selectRows(before)
	end, endOverflow := selectRows(after)
	row := struct {
		Schema        string                          `json:"schema"`
		CaptureFailed bool                            `json:"capture_failed"`
		RowsOverflow  bool                            `json:"rows_overflow"`
		Before        []benchmetrics.PrometheusSample `json:"before"`
		After         []benchmetrics.PrometheusSample `json:"after"`
	}{"wukongim/permission-soak-replication/v1", captureErr != nil, startOverflow || endOverflow, start, end}
	data, err := json.Marshal(row)
	if err != nil {
		t.Errorf("marshal replication-stage diagnostic: %v", err)
		return
	}
	t.Logf("WKRC-PERMISSION-SOAK-REPLICATION %s", data)
}
