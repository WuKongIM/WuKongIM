//go:build integration

package target

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// cancelReadBody cancels only after the transport returned successful headers.
type cancelReadBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelReadBody) Read(p []byte) (int, error) {
	b.cancel()
	return b.ReadCloser.Read(p)
}

func TestObservationHTTPBodyCancellationRemainsCausal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	client := NewClient(Config{APIAddrs: []string{server.URL}, HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		response, err := transport.RoundTrip(req)
		if err == nil {
			response.Body = &cancelReadBody{ReadCloser: response.Body, cancel: cancel}
		}
		return response, err
	})}})
	_, err := client.Metrics(ctx)
	require.ErrorIs(t, err, context.Canceled)
}
