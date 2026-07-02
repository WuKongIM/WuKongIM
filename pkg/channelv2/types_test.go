package channelv2

import (
	"context"
	"errors"
	"testing"
)

func TestAppendAdmissionGuardFuncNilFailsClosed(t *testing.T) {
	var guard AppendAdmissionGuardFunc

	if err := guard.AllowChannelAppend(context.Background(), AppendAdmissionRequest{}); !errors.Is(err, ErrNotReady) {
		t.Fatalf("AllowChannelAppend() error = %v, want ErrNotReady", err)
	}
}
