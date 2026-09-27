package metrics

import "github.com/prometheus/client_golang/prometheus"

// MQTTMetrics keeps fixed aggregate consumer-work series. Events count turn
// observations, including repeated confirmations, not unique Sessions/messages.
type MQTTMetrics struct {
	events             map[string]prometheus.Counter
	admitted, capacity prometheus.Gauge
}

func newMQTTMetrics(reg prometheus.Registerer, labels prometheus.Labels) *MQTTMetrics {
	v := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "wukongim_mqtt_consumer_events_total", Help: "Bounded consumer maintenance turn observations; end confirmations may repeat and are not unique Session counts.", ConstLabels: labels}, []string{"event"})
	g := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "wukongim_mqtt_consumer_work", Help: "Admitted consumer keys across queued and executing work, and the configured cohort bound.", ConstLabels: labels}, []string{"state"})
	m := &MQTTMetrics{events: make(map[string]prometheus.Counter), admitted: g.WithLabelValues("admitted"), capacity: g.WithLabelValues("capacity")}
	for _, event := range []string{"pages", "visited", "scheduled", "completed", "failures", "accounted", "projected", "removed", "quota_end_confirmed", "revocation_end_confirmed"} {
		m.events[event] = v.WithLabelValues(event)
	}
	reg.MustRegister(v, g)
	return m
}

// ObserveConsumer drops unknown event names; caller-provided identities never
// become Prometheus labels. Zero series exist before the first work observation.
func (m *MQTTMetrics) ObserveConsumer(event string, count uint64) {
	if m == nil {
		return
	}
	if c := m.events[event]; c != nil {
		c.Add(float64(count))
	}
}
func (m *MQTTMetrics) SetConsumerWork(admitted, capacity int) {
	if m == nil {
		return
	}
	m.admitted.Set(float64(max(0, admitted)))
	m.capacity.Set(float64(max(0, capacity)))
}
