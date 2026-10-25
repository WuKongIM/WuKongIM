package rpc

import (
	"testing"
	"unsafe"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

// TestDispatchObservationFootprint reports resident request metadata separately
// from object counts; selected-only observation still changes struct sizes.
func TestDispatchObservationFootprint(t *testing.T) {
	t.Logf("dispatch_footprint request=%d entry=%d trace=%d sample=%d", unsafe.Sizeof(Request{}), unsafe.Sizeof(requestEntry{}), unsafe.Sizeof(rpctrace.Trace{}), unsafe.Sizeof(rpctrace.Sample{}))
	t.Logf("selected_dispatch_footprint recorder=%d published=%d", unsafe.Sizeof(rpctrace.DispatchRecorder{}), unsafe.Sizeof(rpctrace.DispatchSample{}))
}
