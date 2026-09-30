//go:build e2e

package send_ban

import (
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

type permissionTimelineFaultTransport struct {
	base  http.RoundTripper
	calls atomic.Int32
}

func (p *permissionTimelineFaultTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Path == "/debug/diagnostics/message" && p.calls.Add(1) == 2 {
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("{}")), Request: req}, nil
	}
	return p.base.RoundTrip(req)
}

// This intentionally failing opt-in observes retained partial receipts after a
// bounded post-window query fault. Its outer receipt verifier expects failure.
func TestPermissionTimelinePartialFailureReceipt(t *testing.T) {
	if os.Getenv("WK_E2E_PERMISSION_TIMELINE_FAILURE_PROBE") != "1" {
		t.Skip("opt-in negative receipt probe")
	}
	base := http.DefaultTransport
	http.DefaultTransport = &permissionTimelineFaultTransport{base: base}
	defer func() { http.DefaultTransport = base }()
	runPermissionCallerExperiment(t, true)
}
