package mqttsession

import (
	"context"
	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

type noConnectionControl struct{}

func (noConnectionControl) Renew(context.Context, contract.Owner) error        { return nil }
func (noConnectionControl) Disconnect(context.Context, DisconnectIntent) error { return nil }
func TestConnectionConfigurationIsBounded(t *testing.T) {
	r, _ := ownerFixture(t, 2, 2)
	base := ConnectionOptions{Owners: r, Control: noConnectionControl{}}
	for _, change := range []func(*ConnectionOptions){func(o *ConnectionOptions) { o.Owners = nil }, func(o *ConnectionOptions) { o.Control = nil }, func(o *ConnectionOptions) { o.Workers = 129 }, func(o *ConnectionOptions) { o.Capacity = 1_000_001 }, func(o *ConnectionOptions) { o.CallTimeout = -1 }, func(o *ConnectionOptions) { o.Retry = time.Minute + 1 }} {
		bad := base
		change(&bad)
		_, err := NewConnections(bad)
		require.Error(t, err)
	}
	s, err := NewConnections(base)
	require.NoError(t, err)
	require.ErrorIs(t, s.Register(contract.Owner{}), ErrConnectionsStopped)
}
