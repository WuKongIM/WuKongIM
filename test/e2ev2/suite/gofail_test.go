//go:build e2e

package suite

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGofailEndpointEnvUsesLoopbackHTTP(t *testing.T) {
	endpoint := ReserveGofailEndpoint(t)
	require.True(t, strings.HasPrefix(endpoint.Env(), "GOFAIL_HTTP=127.0.0.1:"))
	require.Equal(t, "http://"+endpoint.Addr, endpoint.BaseURL())
}

func TestGofailEndpointRejectsEmptyFailpointName(t *testing.T) {
	endpoint := GofailEndpoint{Addr: "127.0.0.1:1"}
	err := endpoint.Enable(context.Background(), "", `return("boom")`)
	require.Error(t, err)
}

func TestReserveGofailEndpointReturnsFreePort(t *testing.T) {
	endpoint := ReserveGofailEndpoint(t)
	ln, err := net.Listen("tcp", endpoint.Addr)
	require.NoError(t, err)
	require.NoError(t, ln.Close())
}
