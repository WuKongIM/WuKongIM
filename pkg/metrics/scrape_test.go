package metrics

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Failure contracts precede the implementation: scrapes must retain complete
// data, negotiation, error responses and independent concurrent gzip streams.
func scrapeFixture() *Registry {
	r := &Registry{registry: prometheus.NewRegistry()}
	g := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "wukongim_channelv2_scrape_fixture", Help: "Complete scrape fixture.",
	}, []string{"slot_id", "node_id"})
	for i := 0; i < 256; i++ {
		g.WithLabelValues(strconv.Itoa(i+1), "3").Set(float64(i))
	}
	r.registry.MustRegister(g)
	return r
}

func scrapeResponse(handler http.Handler, encoding, accept string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Accept-Encoding", encoding)
	req.Header.Set("Accept", accept)
	rsp := httptest.NewRecorder()
	handler.ServeHTTP(rsp, req)
	return rsp
}

func decodeScrape(rsp *httptest.ResponseRecorder) ([]byte, error) {
	if rsp.Header().Get("Content-Encoding") == "" {
		return rsp.Body.Bytes(), nil
	}
	reader, err := gzip.NewReader(bytes.NewReader(rsp.Body.Bytes()))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func TestScrapeCompressionPreservesCompleteExposition(t *testing.T) {
	r := scrapeFixture()
	handler := r.Handler()
	upstream := promhttp.HandlerFor(channelRuntimeAliasGatherer{base: r.registry}, promhttp.HandlerOpts{})
	for _, accept := range []string{"", "application/vnd.google.protobuf; proto=io.prometheus.client.MetricFamily; encoding=delimited"} {
		plain := scrapeResponse(upstream, "", accept)
		for _, encoding := range []string{"", "br", "xgzip", "gzip", "br, gzip", "gzip; q=0.8", "gzip;q=0"} {
			got := scrapeResponse(handler, encoding, accept)
			want := scrapeResponse(upstream, encoding, accept)
			if got.Code != want.Code || got.Header().Get("Content-Encoding") != want.Header().Get("Content-Encoding") || got.Header().Get("Content-Type") != want.Header().Get("Content-Type") {
				t.Fatalf("negotiation changed for %q/%q: %v/%v", encoding, accept, got.Header(), want.Header())
			}
			body, err := decodeScrape(got)
			if err != nil || !bytes.Equal(body, plain.Body.Bytes()) {
				t.Fatalf("incomplete or corrupt scrape for %q/%q: %v", encoding, accept, err)
			}
			if got.Header().Get("Content-Encoding") == "gzip" && (got.Body.Len() < 10 || got.Body.Bytes()[8] != 4) {
				t.Errorf("gzip scrape must advertise fastest compression, got header %x", got.Body.Bytes()[:10])
			}
		}
	}
	plain := scrapeResponse(handler, "", "").Body.Bytes()
	if bytes.Count(plain, []byte("wukongim_channelv2_scrape_fixture{")) != 256 || bytes.Count(plain, []byte("wukongim_channel_scrape_fixture{")) != 256 {
		t.Fatal("lost original or promoted Slot metric series")
	}
}

type failedScrapeCollector struct{ desc *prometheus.Desc }

func (c failedScrapeCollector) Describe(ch chan<- *prometheus.Desc) { ch <- c.desc }
func (c failedScrapeCollector) Collect(ch chan<- prometheus.Metric) {
	ch <- prometheus.NewInvalidMetric(c.desc, errors.New("bounded fixture gather failure"))
}

func TestScrapeGatherFailureAndPoolReuse(t *testing.T) {
	r := scrapeFixture()
	c := failedScrapeCollector{prometheus.NewDesc("scrape_failure", "Fixture failure.", nil, nil)}
	r.registry.MustRegister(c)
	handler := r.Handler()
	failed := scrapeResponse(handler, "gzip", "")
	if failed.Code != http.StatusInternalServerError || failed.Header().Get("Content-Encoding") != "" || !bytes.Contains(failed.Body.Bytes(), []byte("bounded fixture gather failure")) {
		t.Fatalf("gather failure changed: %d/%v/%s", failed.Code, failed.Header(), failed.Body.String())
	}
	if !r.registry.Unregister(c) {
		t.Fatal("fixture did not unregister")
	}
	plain := scrapeResponse(handler, "", "").Body.Bytes()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got := scrapeResponse(handler, "gzip", "")
			body, err := decodeScrape(got)
			if got.Code != http.StatusOK || err != nil || !bytes.Equal(body, plain) {
				t.Errorf("concurrent gzip state corrupted: status=%d err=%v", got.Code, err)
			}
		}()
	}
	wg.Wait()
}

type failedScrapeWriter struct{ header http.Header }

func (w *failedScrapeWriter) Header() http.Header     { return w.header }
func (*failedScrapeWriter) WriteHeader(int)           {}
func (*failedScrapeWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestScrapeWriteFailureDoesNotPoisonPool(t *testing.T) {
	handler := scrapeFixture().Handler()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	handler.ServeHTTP(&failedScrapeWriter{header: make(http.Header)}, req)
	got := scrapeResponse(handler, "gzip", "")
	body, err := decodeScrape(got)
	plain := scrapeResponse(handler, "", "")
	if err != nil || !bytes.Equal(body, plain.Body.Bytes()) {
		t.Fatalf("writer failure contaminated following request: %v", err)
	}
}

func TestScrapeNilRegistryRetainsDefaultInstrumentation(t *testing.T) {
	var r *Registry
	got := scrapeResponse(r.Handler(), "gzip", "")
	body, err := decodeScrape(got)
	if got.Code != http.StatusOK || err != nil || !bytes.Contains(body, []byte("promhttp_metric_handler_requests_total")) || !bytes.Contains(body, []byte("go_goroutines")) {
		t.Fatalf("default instrumentation missing: status=%d err=%v", got.Code, err)
	}
}

// BenchmarkMetricsScrapeCompression compares complete 256-Slot production
// scrapes with upstream compression. Native whole-node E2E CPU is the gate;
// this benchmark only measures the compression tradeoff and wire bytes.
func BenchmarkMetricsScrapeCompression(b *testing.B) {
	r := NewWithLogicalSlots(3, "node-3", 256)
	for _, tc := range []struct {
		name string
		h    http.Handler
	}{
		{"upstream-default", promhttp.HandlerFor(channelRuntimeAliasGatherer{base: r.registry}, promhttp.HandlerOpts{})},
		{"product", r.Handler()},
	} {
		b.Run(tc.name, func(b *testing.B) {
			req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
			req.Header.Set("Accept-Encoding", "gzip")
			var total int64
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				rsp := httptest.NewRecorder()
				tc.h.ServeHTTP(rsp, req)
				if rsp.Code != http.StatusOK {
					b.Fatal(rsp.Code)
				}
				total += int64(rsp.Body.Len())
			}
			b.ReportMetric(float64(total)/float64(b.N), "wire-B/op")
		})
	}
}
