package cluster

import (
	"context"
	"testing"
)

func TestSlotHandlerRejectsUnknownKind(t *testing.T) {
	handler := &slotHandler{cluster: &Cluster{}}
	body := make([]byte, managedSlotRequestHeaderSize+1)
	body[0] = managedSlotCodecVersion
	body[managedSlotRequestHeaderSize] = 0

	_, err := handler.Handle(context.Background(), body)
	if err != ErrInvalidConfig {
		t.Fatalf("slotHandler.Handle() error = %v, want %v", err, ErrInvalidConfig)
	}
}
