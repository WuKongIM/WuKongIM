package prometheus

import (
	"os"
	"strings"
	"testing"
)

func TestPrometheusConfigLoadsChannelRuntimeCapacityAlerts(t *testing.T) {
	rawConfig, err := os.ReadFile("prometheus.yml")
	if err != nil {
		t.Fatalf("read prometheus config: %v", err)
	}
	if !strings.Contains(string(rawConfig), "alerts/*.yml") {
		t.Fatal("prometheus.yml must load alerts/*.yml rule files")
	}

	rawRules, err := os.ReadFile("alerts/wukongim-channel-runtime.yml")
	if err != nil {
		t.Fatalf("read channel runtime alerts: %v", err)
	}
	rules := string(rawRules)
	for _, want := range []string{
		"WKChannelRuntimeCapacityHigh",
		"WKChannelRuntimeCapacityCritical",
		"WKChannelRuntimeActivationRejected",
		"wukongim_channel_active_channels",
		"wukongim_channel_max_channels",
		"wukongim_channel_activation_rejected_total",
	} {
		if !strings.Contains(rules, want) {
			t.Fatalf("channel runtime alerts missing %q", want)
		}
	}
}
