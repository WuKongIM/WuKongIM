//go:build e2e

package suite

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"time"
)

// ForwardedHTTPResponse records the real upstream status without exposing its
// body to the faulted caller. Err means the proxy could not observe that status.
type ForwardedHTTPResponse struct {
	StatusCode int
	Err        error
}

// PostJSONWithResponseLoss forwards one real POST and withholds its response
// until the caller deadline expires. It models an ambiguous write result while
// leaving subsequent public reads to determine whether the write committed.
// The proxy and upstream request share cancellation and retain no response body.
func PostJSONWithResponseLoss(ctx context.Context, endpoint string, body any, timeout time.Duration) (ForwardedHTTPResponse, error) {
	if timeout <= 0 {
		return ForwardedHTTPResponse{}, fmt.Errorf("response-loss timeout must be positive")
	}
	observed := make(chan ForwardedHTTPResponse, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request, err := http.NewRequestWithContext(r.Context(), http.MethodPost, endpoint, r.Body)
		if err != nil {
			observed <- ForwardedHTTPResponse{Err: err}
			http.Error(w, "forward request failed", http.StatusBadGateway)
			return
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			observed <- ForwardedHTTPResponse{Err: err}
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		const maxResponseBytes = 64 << 10
		n, err := io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes+1))
		if n > maxResponseBytes {
			err = fmt.Errorf("upstream response exceeded %d bytes", maxResponseBytes)
		}
		observed <- ForwardedHTTPResponse{StatusCode: response.StatusCode, Err: err}
		// No response headers or bytes reach the caller, even after upstream
		// success. Cancellation releases the handler and the bounded proxy.
		<-r.Context().Done()
	}))
	defer proxy.Close()
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	_, callerErr := PostJSON(requestCtx, proxy.URL, body, nil)
	select {
	case upstream := <-observed:
		return upstream, callerErr
	default:
		return ForwardedHTTPResponse{Err: fmt.Errorf("upstream response not observed before caller completion")}, callerErr
	}
}
