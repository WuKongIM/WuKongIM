package metrics

import (
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

var queueSnapshotNames = [4]string{
	"wukongim_runtime_pool_queue_depth", "wukongim_runtime_pool_queue_capacity",
	"wukongim_runtime_pool_queue_bytes", "wukongim_runtime_pool_queue_bytes_capacity",
}

// queueSnapshotCapture delays serialization of real collected Metrics. The
// registry's ordinary Collect/Write split permits this ordering without sleeps.
type queueSnapshotCapture struct {
	collectors           []prometheus.Collector
	afterDepthCollection func()
	afterDepthWrite      func()
}

func (c *queueSnapshotCapture) Register(collector prometheus.Collector) error {
	c.collectors = append(c.collectors, collector)
	return nil
}
func (c *queueSnapshotCapture) MustRegister(collectors ...prometheus.Collector) {
	for _, collector := range collectors {
		_ = c.Register(collector)
	}
}
func (*queueSnapshotCapture) Unregister(prometheus.Collector) bool { return false }
func (c *queueSnapshotCapture) Describe(out chan<- *prometheus.Desc) {
	for _, collector := range c.collectors {
		collector.Describe(out)
	}
}
func queueSnapshotHasDepth(collector prometheus.Collector) bool {
	descriptions := make(chan *prometheus.Desc, 16)
	collector.Describe(descriptions)
	close(descriptions)
	for description := range descriptions {
		if strings.Contains(description.String(), `fqName: "`+queueSnapshotNames[0]+`"`) {
			return true
		}
	}
	return false
}
func (c *queueSnapshotCapture) Collect(out chan<- prometheus.Metric) {
	collectors := append([]prometheus.Collector(nil), c.collectors...)
	sort.SliceStable(collectors, func(i, j int) bool {
		return queueSnapshotHasDepth(collectors[i]) && !queueSnapshotHasDepth(collectors[j])
	})
	var metrics []prometheus.Metric
	for _, collector := range collectors {
		// Fixtures create at most two queues (eight metrics). Fully collecting
		// before callbacks avoids holding a MetricVec lock during publication.
		collected := make(chan prometheus.Metric, 16)
		collector.Collect(collected)
		close(collected)
		for metric := range collected {
			metrics = append(metrics, metric)
		}
		if queueSnapshotHasDepth(collector) && c.afterDepthCollection != nil {
			c.afterDepthCollection()
		}
	}
	sort.SliceStable(metrics, func(i, j int) bool {
		return strings.Contains(metrics[i].Desc().String(), `fqName: "`+queueSnapshotNames[0]+`"`) &&
			!strings.Contains(metrics[j].Desc().String(), `fqName: "`+queueSnapshotNames[0]+`"`)
	})
	for _, metric := range metrics {
		if c.afterDepthWrite != nil && strings.Contains(metric.Desc().String(), `fqName: "`+queueSnapshotNames[0]+`"`) {
			metric = queueSnapshotWrite{Metric: metric, after: c.afterDepthWrite}
		}
		out <- metric
	}
}

type queueSnapshotWrite struct {
	prometheus.Metric
	after func()
}

func (m queueSnapshotWrite) Write(out *dto.Metric) error {
	if err := m.Metric.Write(out); err != nil {
		return err
	}
	m.after()
	return nil
}

func queueSnapshotValues(t *testing.T, families []*dto.MetricFamily) [4]float64 {
	t.Helper()
	var values [4]float64
	for i, name := range queueSnapshotNames {
		family := requireMetricFamily(t, families, name)
		if len(family.Metric) != 1 {
			t.Fatalf("%s series=%d, want one complete queue", name, len(family.Metric))
		}
		metric := family.Metric[0]
		if len(metric.Label) != 4 {
			t.Fatalf("%s labels=%v, want original four variable labels", name, metric.Label)
		}
		values[i] = metric.GetGauge().GetValue()
	}
	return values
}

func TestRuntimePressureQueueSnapshotAcrossDeferredWrite(t *testing.T) {
	for _, revisioned := range []bool{false, true} {
		for _, http := range []bool{false, true} {
			name := "gather"
			if http {
				name = "http"
			}
			if revisioned {
				name += "_revisioned"
			}
			t.Run(name, func(t *testing.T) {
				capture := &queueSnapshotCapture{}
				m := newRuntimePressureMetrics(capture, nil)
				set := func(revision uint64, observation RuntimePressureQueueObservation) {
					if revisioned {
						m.SetQueueRevisioned("transport", "scheduler", "scheduler", "rpc", revision, observation)
					} else {
						m.SetQueue("transport", "scheduler", "scheduler", "rpc", observation)
					}
				}
				set(1, RuntimePressureQueueObservation{Depth: 8, Capacity: 10, Bytes: 80, BytesCapacity: 100})
				writes := 0
				capture.afterDepthWrite = func() {
					writes++
					set(2, RuntimePressureQueueObservation{Depth: 1, Capacity: 1, Bytes: 10, BytesCapacity: 10})
				}
				registry := prometheus.NewRegistry()
				registry.MustRegister(capture)
				var families []*dto.MetricFamily
				if http {
					recorder := httptest.NewRecorder()
					promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
					if recorder.Code != 200 {
						t.Fatalf("HTTP status=%d", recorder.Code)
					}
					var parser expfmt.TextParser
					parsed, err := parser.TextToMetricFamilies(strings.NewReader(recorder.Body.String()))
					if err != nil {
						t.Fatal(err)
					}
					for _, family := range parsed {
						families = append(families, family)
					}
				} else {
					var err error
					families, err = registry.Gather()
					if err != nil {
						t.Fatal(err)
					}
				}
				if writes != 1 {
					t.Fatalf("publication trigger count=%d", writes)
				}
				if got := queueSnapshotValues(t, families); got != [4]float64{8, 10, 80, 100} {
					t.Errorf("mixed observation=%v, want frozen [8 10 80 100]", got)
				}
				capture.afterDepthWrite = nil
				families, err := registry.Gather()
				if err != nil {
					t.Fatal(err)
				}
				if got := queueSnapshotValues(t, families); got != [4]float64{1, 1, 10, 10} {
					t.Errorf("current observation=%v", got)
				}
			})
		}
	}
}

func TestRuntimePressureQueueSnapshotMembershipIsComplete(t *testing.T) {
	capture := &queueSnapshotCapture{}
	m := newRuntimePressureMetrics(capture, nil)
	m.SetQueue("transport", "scheduler", "scheduler", "rpc", RuntimePressureQueueObservation{Depth: 1, Capacity: 2})
	capture.afterDepthCollection = func() {
		m.SetQueue("transport", "scheduler", "scheduler", "raft", RuntimePressureQueueObservation{Depth: 2, Capacity: 3})
	}
	registry := prometheus.NewRegistry()
	registry.MustRegister(capture)
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range queueSnapshotNames {
		family := requireMetricFamily(t, families, name)
		if len(family.Metric) != 1 {
			t.Errorf("%s exposed partial next membership: series=%d", name, len(family.Metric))
		}
	}
}

func TestRuntimePressureQueueSnapshotPreservesSetterSemantics(t *testing.T) {
	registry := prometheus.NewRegistry()
	m := newRuntimePressureMetrics(registry, nil)
	set := func(v RuntimePressureQueueObservation) { m.SetQueue("transport", "scheduler", "scheduler", "rpc", v) }
	revision := func(n uint64, v RuntimePressureQueueObservation) {
		m.SetQueueRevisioned("transport", "scheduler", "scheduler", "rpc", n, v)
	}
	check := func(want [4]float64) {
		t.Helper()
		families, err := registry.Gather()
		if err != nil {
			t.Fatal(err)
		}
		if got := queueSnapshotValues(t, families); got != want {
			t.Fatalf("observation=%v want=%v", got, want)
		}
	}
	set(RuntimePressureQueueObservation{Depth: 8, Capacity: 10, Bytes: 80, BytesCapacity: 100})
	m.SetQueueDepth("transport", "scheduler", "scheduler", "rpc", 3)
	check([4]float64{3, 10, 80, 100})
	m.SetQueueCapacity("transport", "scheduler", "scheduler", "rpc", 0)
	check([4]float64{3, 0, 80, 100})
	m.SetQueueBytes("transport", "scheduler", "scheduler", "rpc", -1)
	check([4]float64{3, 0, 0, 100})
	m.SetQueueBytesCapacity("transport", "scheduler", "scheduler", "rpc", 42)
	check([4]float64{3, 0, 0, 42})
	revision(2, RuntimePressureQueueObservation{Depth: 2, Capacity: 4})
	check([4]float64{2, 4, 0, 0})
	set(RuntimePressureQueueObservation{Depth: 1, Capacity: 3})
	revision(1, RuntimePressureQueueObservation{Depth: 9, Capacity: 10})
	check([4]float64{1, 3, 0, 0})
	revision(0, RuntimePressureQueueObservation{Depth: -1, Capacity: -1, Bytes: -1, BytesCapacity: -1})
	check([4]float64{})
	var absent *RuntimePressureMetrics
	absent.SetQueue("", "", "", "", RuntimePressureQueueObservation{})
	absent.SetQueueRevisioned("", "", "", "", 1, RuntimePressureQueueObservation{})
	absent.SetQueueDepth("", "", "", "", 1)
	absent.SetQueueCapacity("", "", "", "", 1)
	absent.SetQueueBytes("", "", "", "", 1)
	absent.SetQueueBytesCapacity("", "", "", "", 1)
}

func TestRuntimePressureQueueSnapshotWarmedPublicationHasNoAllocations(t *testing.T) {
	m := newRuntimePressureMetrics(prometheus.NewRegistry(), nil)
	var revision uint64
	update := func() {
		revision++
		m.SetQueue("transport", "scheduler", "scheduler", "rpc", RuntimePressureQueueObservation{Depth: 1, Capacity: 4})
		m.SetQueueRevisioned("transport", "scheduler", "scheduler", "rpc", revision, RuntimePressureQueueObservation{Depth: 2, Capacity: 4})
		m.SetQueueDepth("transport", "scheduler", "scheduler", "rpc", 1)
		m.SetQueueCapacity("transport", "scheduler", "scheduler", "rpc", 4)
		m.SetQueueBytes("transport", "scheduler", "scheduler", "rpc", 8)
		m.SetQueueBytesCapacity("transport", "scheduler", "scheduler", "rpc", 16)
	}
	update()
	if got := testing.AllocsPerRun(1000, update); got != 0 {
		t.Fatalf("warmed publication allocations=%v", got)
	}
}
