package rpctrace_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

// Failure matrix: competing immutable candidates and Finish cannot combine
// one group's phase with another group's owner data or publish a partial group.
func TestBarrierOwnerCoverageConcurrentPublicationStaysOneGroup(t *testing.T) {
	r, _ := rpctrace.NewRecorder(1)
	tr := r.Start(context.Background(), rpctrace.SideServer, 91)
	base := time.Now()
	type candidate struct {
		width time.Duration
		spans [rpctrace.MaxBarrierSpans]rpctrace.HistoricalSpan
		data  rpctrace.BarrierCoverage
	}
	candidates := make([]candidate, 9)
	for i := range candidates {
		width := time.Duration(10 + 2*i)
		spans, data := coverageCandidate(base, width)
		owner := ownerCoverageDocument()
		ownerPart(owner, 0, "budget")["duration"] = int64(width / 2)
		ownerPart(owner, 0, "budget")["sync_work"] = int64(width/2 - 3)
		installOwnerDocument(t, &data, owner)
		candidates[i] = candidate{width: width, spans: spans, data: data}
	}
	first := candidates[0]
	tr.RetainLongestBarrierWithCoverage(base, base.Add(5*first.width), first.spans, 5, 0, first.data)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for _, current := range candidates[1:] {
		workers.Add(1)
		go func(c candidate) {
			defer workers.Done()
			<-start
			tr.RetainLongestBarrierWithCoverage(base, base.Add(5*c.width), c.spans, 5, 0, c.data)
		}(current)
	}
	finished := make(chan *rpctrace.Sample, 1)
	go func() {
		<-start
		finished <- tr.Finish(rpctrace.ResultCanceled)
	}()
	close(start)
	sample := <-finished
	workers.Wait()
	owner := readOwnerDocument(t, sample.BarrierCoverage)
	if owner == nil || len(sample.Spans) != 5 || sample.DroppedSpans != 0 || r.Active() != 0 {
		t.Fatal("concurrent publication lost or split the selected group")
	}
	width := sample.Spans[0].Duration
	if ownerPart(owner, 0, "budget")["duration"] != float64(width/2) {
		t.Fatal("owner data and phase duration came from different candidates")
	}
	for _, span := range sample.Spans {
		if span.Duration != width {
			t.Fatal("Finish published phases from multiple candidates")
		}
	}
}
