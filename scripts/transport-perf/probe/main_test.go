//go:build linux || darwin

package main

import (
	"io"
	"testing"
)

func TestQuantilesPreserveHistoricalRank(t *testing.T) {
	q := summarize([]int64{4e6, 1e6, 3e6, 2e6})
	if q.Calls != 4 || q.MeanMS != 2.5 || q.P50MS != 2 || q.P95MS != 3 || q.P99MS != 3 {
		t.Fatalf("unexpected exact quantiles: %+v", q)
	}
	if q := summarize(nil); q.Calls != 0 || q.P99MS != 0 {
		t.Fatal(q)
	}
}

func TestRejectUnboundedOptions(t *testing.T) {
	for _, args := range [][]string{{"-duration=0"}, {"-duration=61s"}, {"-workers=17"}, {"-shards=0"}, {"-warmup=-1s"}, {"-warmup=31s"}, {"-sample-cap=1000001"}, {"-gc=-1"}, {"-lifetime=3h"}, {"-mode=unknown"}, {"unexpected"}} {
		if _, err := parse(args, io.Discard); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
