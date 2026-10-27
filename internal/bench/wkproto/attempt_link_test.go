package wkproto

import "testing"

// F06: the private adapter option is independent of queue geometry and passes
// to the real SDK without expanding current pending/publication bounds.
func TestAttemptLinkAdapterOptInPreservesExactSDKCapacities(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		c, err := NewClient(ClientConfig{Addr: "127.0.0.1:5100", SendQueueCapacity: 32, MaxInflight: 32, FrameBufferSize: 32, EnableAttemptLinkDiagnostics: enabled})
		if err != nil {
			t.Fatal(err)
		}
		cfg := c.innerConfig()
		if cfg.EnableAttemptLinkDiagnostics != enabled || cfg.SendQueueCapacity != 32 || cfg.MaxInflight != 32 || cfg.InboundFrameBufferSize != 32 {
			t.Fatalf("adapter altered SDK config: %+v", cfg)
		}
		if got := c.ConnectFingerprint(); got != [16]byte{} {
			t.Fatal("adapter without active session minted scope")
		}
	}
}
