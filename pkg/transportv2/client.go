package transportv2

// Client owns outbound peer connection state.
type Client struct {
	cfg ClientConfig
}

// NewClient validates config and builds a minimal client shell.
func NewClient(cfg ClientConfig) (*Client, error) {
	normalized, err := normalizeClientConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &Client{cfg: normalized}, nil
}

// Stop releases client resources.
func (c *Client) Stop() {}

// Stats returns a point-in-time client stats snapshot.
func (c *Client) Stats() Stats { return Stats{} }
