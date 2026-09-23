package node

import (
	"context"
	"encoding/hex"
	"testing"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	"github.com/stretchr/testify/require"
)

func mqttRPCOwner() contract.Owner {
	return contract.Owner{Key: contract.Key{Namespace: "n", ClientID: "c"}, SessionGeneration: 1, OwnerGeneration: 2, NodeID: 3, BootID: "b", ConnectionID: 4}
}

type mqttClosePortFunc func(context.Context, contract.Owner) error

func (f mqttClosePortFunc) Quiesce(ctx context.Context, o contract.Owner) error { return f(ctx, o) }

type mqttRPCNodeFunc func(context.Context, uint64, uint8, []byte) ([]byte, error)

func (f mqttRPCNodeFunc) CallRPC(ctx context.Context, node uint64, service uint8, body []byte) ([]byte, error) {
	return f(ctx, node, service, body)
}

func TestMQTTOwnerWireIsBoundedAndIdentityComplete(t *testing.T) {
	o := mqttRPCOwner()
	body, err := encodeMQTTOwnerRequest(o)
	require.NoError(t, err)
	require.Equal(t, "574b4d51010100016e0001630000000000000001000000000000000200000000000000030001620000000000000004", hex.EncodeToString(body))
	calls := 0
	a := MQTTOwnerRPC{Owners: mqttClosePortFunc(func(context.Context, contract.Owner) error { calls++; return nil })}
	for i := 0; i < len(body); i++ {
		_, err := a.HandleRPC(context.Background(), body[:i])
		require.Error(t, err)
	}
	for _, bad := range [][]byte{append(append([]byte{}, body...), 0), make([]byte, 4096), []byte("{}"), append([]byte("WKMQ\x02\x01"), body[6:]...), append([]byte("WKMQ\x01\x02"), body[6:]...)} {
		_, err := a.HandleRPC(context.Background(), bad)
		require.Error(t, err)
	}
	require.Zero(t, calls)
	response, err := a.HandleRPC(context.Background(), body)
	require.NoError(t, err)
	got, status, err := decodeMQTTOwnerResponse(response)
	require.NoError(t, err)
	require.Equal(t, o, got)
	require.Equal(t, byte(1), status)
}

func TestMQTTOwnerRPCRequiresExactSuccessfulReceipt(t *testing.T) {
	o := mqttRPCOwner()
	for _, closeErr := range []error{nil, runtime.ErrOwnerUnknown, runtime.ErrOwnerClose, context.Canceled, context.DeadlineExceeded} {
		a := MQTTOwnerRPC{Owners: mqttClosePortFunc(func(_ context.Context, got contract.Owner) error { require.Equal(t, o, got); return closeErr })}
		client := NewMQTTOwnerClient(mqttRPCNodeFunc(func(ctx context.Context, id uint64, service uint8, body []byte) ([]byte, error) {
			require.Equal(t, o.NodeID, id)
			require.Equal(t, uint8(92), service)
			return a.HandleRPC(ctx, body)
		}))
		err := client.Quiesce(context.Background(), o)
		if closeErr == nil {
			require.NoError(t, err)
		} else {
			require.ErrorIs(t, err, closeErr)
		}
	}
	for _, mutate := range []func(contract.Owner) ([]byte, error){
		func(o contract.Owner) ([]byte, error) { o.ConnectionID++; return encodeMQTTOwnerResponse(o, 1) },
		func(o contract.Owner) ([]byte, error) { o.BootID = "other"; return encodeMQTTOwnerResponse(o, 1) },
		func(contract.Owner) ([]byte, error) { return nil, nil },
		func(contract.Owner) ([]byte, error) { return []byte{1}, nil },
	} {
		client := NewMQTTOwnerClient(mqttRPCNodeFunc(func(context.Context, uint64, uint8, []byte) ([]byte, error) { return mutate(o) }))
		require.Error(t, client.Quiesce(context.Background(), o))
	}
	body, err := encodeMQTTOwnerRequest(o)
	require.NoError(t, err)
	response, err := (MQTTOwnerRPC{}).HandleRPC(context.Background(), body)
	require.NoError(t, err)
	_, status, err := decodeMQTTOwnerResponse(response)
	require.NoError(t, err)
	require.NotEqual(t, byte(1), status)
}

func TestMQTTOwnerRPCWaitsForRuntimeQuiescence(t *testing.T) {
	now := time.Now()
	owners, err := runtime.NewOwners(runtime.OwnerOptions{NodeID: 3, BootID: "b", Capacity: 1, MaxOperations: 1, PendingTimeout: time.Second, MaxLease: time.Second, CloseRetry: time.Second, Now: func() time.Time { return now }})
	require.NoError(t, err)
	closed := make(chan struct{})
	o, err := owners.Reserve(runtime.Claim{Key: contract.Key{Namespace: "n", ClientID: "c"}, UID: "alice", SessionGeneration: 1, OwnerGeneration: 2}, func(context.Context) error { close(closed); return nil })
	require.NoError(t, err)
	require.NoError(t, owners.Activate(o, 1, now.Add(time.Second)))
	op, err := owners.Begin(context.Background(), o)
	require.NoError(t, err)
	a := MQTTOwnerRPC{Owners: owners}
	client := NewMQTTOwnerClient(mqttRPCNodeFunc(func(ctx context.Context, _ uint64, _ uint8, b []byte) ([]byte, error) { return a.HandleRPC(ctx, b) }))
	done := make(chan error, 1)
	go func() { done <- client.Quiesce(context.Background(), o) }()
	<-closed
	select {
	case err := <-done:
		t.Fatalf("receipt preceded scope drain: %v", err)
	default:
	}
	op.Done()
	require.NoError(t, <-done)
	require.NoError(t, client.Quiesce(context.Background(), o))
	o.BootID = "previous"
	require.ErrorIs(t, client.Quiesce(context.Background(), o), runtime.ErrOwnerUnknown)
}
