package metrics

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMQTTOwnerMetricsUseOnlyFixedAggregateLabels(t *testing.T) {
	r := New(1, "node")
	r.MQTT.ObserveOwnerSweep(3, true)
	r.MQTT.SetOwnerWork("held", 4)
	r.MQTT.SetOwnerWork("uncertain", 1)
	r.MQTT.SetOwnerWork("private-client", 9)
	r.MQTT.SetOwnerWork("pending", -1)
	f, err := r.Gather()
	require.NoError(t, err)
	events := requireMetricFamily(t, f, "wukongim_mqtt_owner_sweep_total")
	require.Len(t, events.Metric, 3)
	for event, value := range map[string]float64{"turns": 1, "visited": 3, "failures": 1} {
		require.Equal(t, value, findMetricByLabels(t, events, map[string]string{"event": event}).GetCounter().GetValue())
	}
	work := requireMetricFamily(t, f, "wukongim_mqtt_owner_work")
	require.Len(t, work.Metric, 7)
	for state, value := range map[string]float64{"held": 4, "pending": 0, "active": 0, "closing": 0, "operations": 0, "deadlines": 0, "uncertain": 1} {
		require.Equal(t, value, findMetricByLabels(t, work, map[string]string{"state": state}).GetGauge().GetValue())
	}
	var disabled *MQTTMetrics
	disabled.ObserveOwnerSweep(1, false)
	disabled.SetOwnerWork("held", 1)
}

func TestMQTTConsumerMetricsBoundLabelsAndMaterializeZero(t *testing.T) {
	r := New(1, "node")
	r.MQTT.ObserveConsumer("quota_end_confirmed", 2)
	r.MQTT.ObserveConsumer("client-secret", 9)
	r.MQTT.SetConsumerWork(3, 16)
	f, e := r.Gather()
	require.NoError(t, e)
	events := requireMetricFamily(t, f, "wukongim_mqtt_consumer_events_total")
	require.Len(t, events.Metric, 11)
	require.Zero(t, findMetricByLabels(t, events, map[string]string{"event": "qualification_removed"}).GetCounter().GetValue())
	require.Equal(t, float64(2), findMetricByLabels(t, events, map[string]string{"event": "quota_end_confirmed"}).GetCounter().GetValue())
	require.Zero(t, findMetricByLabels(t, events, map[string]string{"event": "revocation_end_confirmed"}).GetCounter().GetValue())
	for _, m := range events.Metric {
		for _, l := range m.Label {
			require.NotContains(t, l.GetValue(), "secret")
		}
	}
	work := requireMetricFamily(t, f, "wukongim_mqtt_consumer_work")
	require.Len(t, work.Metric, 2)
	require.Equal(t, float64(3), findMetricByLabels(t, work, map[string]string{"state": "admitted"}).GetGauge().GetValue())
	var disabled *MQTTMetrics
	disabled.ObserveConsumer("completed", 1)
	disabled.SetConsumerWork(0, 16)
}
