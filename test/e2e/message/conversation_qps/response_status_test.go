package conversation_qps

import "testing"

// The legacy sync route exposes this exact serving-node backpressure envelope.
// It rejects the offered rate; unrelated failures must not become capacity evidence.
func TestCapacityClassifiesObservedLegacyBackpressure(t *testing.T) {
	for _, tc := range []struct {
		code    int
		body    string
		refusal bool
	}{
		{503, `{"error":"retry required"}`, true},
		{400, `{"msg":"internal/message: backpressured: channel: backpressured","status":400}`, true},
		{400, `{"msg":"internal/usecase/conversation: list capacity exceeded","status":400}`, true},
		{400, `{"msg":"internal/usecase/conversation: list capacity exceeded: disk error","status":400}`, false},
		{400, `{"msg":"disk read failed","status":400}`, false},
		{400, `{"msg":"internal/message: backpressured: channel: backpressured","status":500}`, false},
		{400, `{"msg":"internal/usecase/conversation: route not ready\nchannel: backpressured","status":400}`, true},
		{400, `{"msg":"internal/usecase/conversation: route not ready","status":400}`, false},
		{400, `backpressured`, false},
		{500, `{"msg":"internal/message: backpressured: channel: backpressured","status":400}`, false},
	} {
		if got := (&responseStatusError{code: tc.code, body: tc.body}).capacityRefusal(); got != tc.refusal {
			t.Errorf("HTTP %d %s: refusal=%v, want %v", tc.code, tc.body, got, tc.refusal)
		}
	}
}
