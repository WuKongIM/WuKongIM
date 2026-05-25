package propose

import "context"

// ForwardHandler handles remote Slot proposal requests on the target node.
type ForwardHandler struct {
	slots SlotRuntime
}

// NewForwardHandler creates a ForwardHandler for slots.
func NewForwardHandler(slots SlotRuntime) *ForwardHandler { return &ForwardHandler{slots: slots} }

// HandleRPC decodes and applies one forwarded Slot proposal.
func (h *ForwardHandler) HandleRPC(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := DecodeForwardRequest(payload)
	if err != nil {
		return nil, err
	}
	if h == nil || h.slots == nil || !h.slots.IsLocalLeader(req.SlotID) {
		return nil, ErrNotLeader
	}
	if err := h.slots.Propose(ctx, req.SlotID, req.Payload); err != nil {
		return nil, err
	}
	return nil, nil
}
