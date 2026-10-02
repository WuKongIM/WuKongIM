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
	require.Len(t, events.Metric, 16)
	require.Zero(t, findMetricByLabels(t, events, map[string]string{"event": "subscription_removal_confirmed"}).GetCounter().GetValue())
	require.Zero(t, findMetricByLabels(t, events, map[string]string{"event": "subscription_establishment_confirmed"}).GetCounter().GetValue())
	r.MQTT.ObserveConsumer("subscription_establishment_confirmed", 4)
	r.MQTT.ObserveConsumer("subscription_removal_confirmed", 3)
	updated, err := r.Gather()
	require.NoError(t, err)
	require.Equal(t, float64(4), findMetricByLabels(t, requireMetricFamily(t, updated, "wukongim_mqtt_consumer_events_total"), map[string]string{"event": "subscription_establishment_confirmed"}).GetCounter().GetValue())
	require.Equal(t, float64(3), findMetricByLabels(t, requireMetricFamily(t, updated, "wukongim_mqtt_consumer_events_total"), map[string]string{"event": "subscription_removal_confirmed"}).GetCounter().GetValue())
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

func TestMQTTSubscriptionClosuresHaveFixedZeroSeries(t *testing.T) {
	r := New(1, "node")
	r.MQTT.ObserveSubscriptionClose("subscribe", "conflict")
	r.MQTT.ObserveSubscriptionClose("unsubscribe", "deadline")
	r.MQTT.ObserveSubscriptionClose("secret-client", "conflict")
	r.MQTT.ObserveSubscriptionClose("subscribe", "secret-topic")
	f, err := r.Gather()
	require.NoError(t, err)
	family := requireMetricFamily(t, f, "wukongim_mqtt_subscription_closures_total")
	require.Len(t, family.Metric, 34)
	for _, operation := range []string{"subscribe", "unsubscribe"} {
		for _, reason := range []string{"disabled", "malformed", "owner_limit", "fenced", "deadline", "canceled", "clock", "conflict", "evidence", "pending", "unconfirmed", "denied", "quota", "callback", "reply_evidence", "reply_write", "unknown"} {
			want := float64(0)
			if operation == "subscribe" && reason == "conflict" || operation == "unsubscribe" && reason == "deadline" {
				want = 1
			}
			require.Equal(t, want, findMetricByLabels(t, family, map[string]string{"operation": operation, "reason": reason}).GetCounter().GetValue())
		}
	}
	var disabled *MQTTMetrics
	disabled.ObserveSubscriptionClose("subscribe", "unknown")
}

func TestMQTTReclamationMetricsUseFixedAggregateEvents(t *testing.T) {
	r := New(1, "node")
	r.MQTT.ObserveConsumer("reclamation_confirmed", 2)
	r.MQTT.ObserveConsumer("reclamation_index_rows", 64)
	r.MQTT.ObserveConsumer("client-secret", 7)
	families, err := r.Gather()
	require.NoError(t, err)
	events := requireMetricFamily(t, families, "wukongim_mqtt_consumer_events_total")
	require.Len(t, events.Metric, 16)
	require.Equal(t, float64(2), findMetricByLabels(t, events, map[string]string{"event": "reclamation_confirmed"}).GetCounter().GetValue())
	require.Equal(t, float64(64), findMetricByLabels(t, events, map[string]string{"event": "reclamation_index_rows"}).GetCounter().GetValue())
}

func TestMQTTTombstoneRetirementMetricUsesFixedEvent(t *testing.T) {
	r := New(1, "node")
	r.MQTT.ObserveConsumer("retired", 3)
	families, err := r.Gather()
	require.NoError(t, err)
	events := requireMetricFamily(t, families, "wukongim_mqtt_consumer_events_total")
	require.Equal(t, float64(3), findMetricByLabels(t, events, map[string]string{"event": "retired"}).GetCounter().GetValue())
}
