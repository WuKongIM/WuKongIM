package metrics

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMQTTConsumerMetricsBoundLabelsAndMaterializeZero(t *testing.T) {
	r := New(1, "node")
	r.MQTT.ObserveConsumer("quota_end_confirmed", 2)
	r.MQTT.ObserveConsumer("client-secret", 9)
	r.MQTT.SetConsumerWork(3, 16)
	f, e := r.Gather()
	require.NoError(t, e)
	events := requireMetricFamily(t, f, "wukongim_mqtt_consumer_events_total")
	require.Len(t, events.Metric, 10)
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
