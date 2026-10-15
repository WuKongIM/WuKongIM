package metrics

import (
	"sync"

	"github.com/golang/protobuf/proto"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// storageCommitBatchSnapshot keeps the batch-size and stage histograms on one
// observation cut. Its private registry contains only these four families;
// the lock never spans the main registry, downstream consumers or HTTP encoding.
type storageCommitBatchSnapshot struct {
	mu          sync.Mutex
	registry    *prometheus.Registry
	descriptors map[string]*prometheus.Desc
}

func newStorageCommitBatchSnapshot(histograms map[string]*prometheus.HistogramVec) *storageCommitBatchSnapshot {
	s := &storageCommitBatchSnapshot{
		registry:    prometheus.NewRegistry(),
		descriptors: make(map[string]*prometheus.Desc, len(histograms)),
	}
	for name, histogram := range histograms {
		// Each HistogramVec describes one family without materializing a series.
		descriptor := make(chan *prometheus.Desc, 1)
		histogram.Describe(descriptor)
		s.descriptors[name] = <-descriptor
		s.registry.MustRegister(histogram)
	}
	return s
}

func (s *storageCommitBatchSnapshot) Describe(ch chan<- *prometheus.Desc) {
	for _, descriptor := range s.descriptors {
		ch <- descriptor
	}
}

func (s *storageCommitBatchSnapshot) Collect(ch chan<- prometheus.Metric) {
	s.mu.Lock()
	// Gather calls each histogram's Write while observations are excluded.
	// Sending live HistogramVec metrics under this lock would not suffice:
	// the outer registry calls Write after Collect returns and the lock is gone.
	families, err := s.registry.Gather()
	s.mu.Unlock()
	if err != nil {
		for _, descriptor := range s.descriptors {
			ch <- prometheus.NewInvalidMetric(descriptor, err)
			return
		}
	}
	for _, family := range families {
		for _, metric := range family.Metric {
			ch <- storageCommitBatchMetric{descriptor: s.descriptors[family.GetName()], snapshot: metric}
		}
	}
}

// storageCommitBatchMetric owns an immutable DTO and returns detached copies so
// a downstream Gather caller cannot mutate another snapshot consumer's data.
type storageCommitBatchMetric struct {
	descriptor *prometheus.Desc
	snapshot   *dto.Metric
}

func (m storageCommitBatchMetric) Desc() *prometheus.Desc { return m.descriptor }

func (m storageCommitBatchMetric) Write(out *dto.Metric) error {
	*out = *proto.Clone(m.snapshot).(*dto.Metric)
	return nil
}
