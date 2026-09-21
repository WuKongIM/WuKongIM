//go:build e2e

package suite

import (
	"context"
	"testing"
	"time"

	wkclient "github.com/WuKongIM/WuKongIM/pkg/client"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestManagedWKProtoReadinessKeepsTokenAuthenticationEnabled(t *testing.T) {
	node := New(t).StartSingleNodeCluster(WithNodeConfigOverrides(1, map[string]string{
		"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_GATEWAY_TOKEN_AUTH_ON": "true",
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := wkclient.New(wkclient.Config{Addr: node.GatewayAddr(), OperationTimeout: time.Second})
	require.NoError(t, err)
	defer client.Close()
	_, err = client.Connect(ctx, wkclient.ConnectOptions{UID: "e2e-ready-1", DeviceID: "unauthorized-probe", DeviceFlag: frame.APP, Token: "wrong-token"})
	require.Error(t, err, "readiness must not turn off gateway token authentication")
	require.ErrorContains(t, err, "ReasonAuthFail")
}
