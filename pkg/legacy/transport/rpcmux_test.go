package transport

import (
	"context"
	"testing"
)

func TestRPCMuxRoutesByServiceID(t *testing.T) {
	mux := NewRPCMux()
	mux.Handle(1, func(ctx context.Context, body []byte) ([]byte, error) {
		return []byte("raft"), nil
	})
	mux.Handle(2, func(ctx context.Context, body []byte) ([]byte, error) {
		return []byte("isr"), nil
	})

	resp, err := mux.HandleRPC(context.Background(), append([]byte{2}, []byte("payload")...))
	if err != nil {
		t.Fatalf("HandleRPC() error = %v", err)
	}
	if string(resp) != "isr" {
		t.Fatalf("response = %q, want %q", resp, "isr")
	}
}

func TestRPCMuxRejectsUnknownService(t *testing.T) {
	mux := NewRPCMux()

	_, err := mux.HandleRPC(context.Background(), []byte{9})
	if err == nil {
		t.Fatal("expected unknown service error")
	}
}

func TestRPCMuxRejectsDuplicateServiceRegistration(t *testing.T) {
	mux := NewRPCMux()
	mux.Handle(1, func(ctx context.Context, body []byte) ([]byte, error) {
		return body, nil
	})

	defer func() {
		if recover() == nil {
			t.Fatal("expected duplicate service registration panic")
		}
	}()

	mux.Handle(1, func(ctx context.Context, body []byte) ([]byte, error) {
		return body, nil
	})
}
