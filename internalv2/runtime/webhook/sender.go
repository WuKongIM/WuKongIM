package webhook

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// HTTPSenderOptions configures outbound HTTP webhook delivery.
type HTTPSenderOptions struct {
	// Addr is the base webhook URL. The event query parameter is added per request.
	Addr string
	// Timeout bounds one outbound HTTP request attempt.
	Timeout time.Duration
	// Client optionally supplies a shared HTTP client for tests or custom transports.
	Client *http.Client
}

// HTTPSender posts JSON webhook requests to one configured endpoint.
type HTTPSender struct {
	addr    string
	timeout time.Duration
	client  *http.Client
}

// NewHTTPSender creates an HTTP webhook sender.
func NewHTTPSender(opts HTTPSenderOptions) *HTTPSender {
	client := opts.Client
	if client == nil {
		client = &http.Client{}
	}
	return &HTTPSender{addr: opts.Addr, timeout: opts.Timeout, client: client}
}

// Send posts the encoded webhook body as JSON. Only HTTP 200 is classified as success.
func (s *HTTPSender) Send(ctx context.Context, req SendRequest) error {
	if s == nil || s.addr == "" {
		return fmt.Errorf("webhook: http addr is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if s.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.timeout)
		defer cancel()
	}
	target, err := url.Parse(s.addr)
	if err != nil {
		return err
	}
	query := target.Query()
	query.Set("event", req.Event)
	target.RawQuery = query.Encode()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(req.Body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("webhook: http status %s", strconv.Itoa(resp.StatusCode))
	}
	return nil
}
