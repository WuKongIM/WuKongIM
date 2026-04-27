package core_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/gateway"
	"github.com/WuKongIM/WuKongIM/internal/gateway/testkit"
	"github.com/stretchr/testify/require"
)

func TestServerSessionSummaryCountsUnauthenticatedStatesByListener(t *testing.T) {
	handler := newTestHandler()
	proto := newScriptedProtocol("jsonrpc")
	transportFactory := testkit.NewFakeTransportFactory("fake-transport")
	srv := newServerWithListeners(t, handler, proto, transportFactory, gateway.SessionOptions{}, nil, nil, []gateway.ListenerOptions{
		{Name: "listener-a", Network: "tcp", Address: "127.0.0.1:9000", Transport: transportFactory.Name(), Protocol: proto.Name()},
		{Name: "listener-b", Network: "tcp", Address: "127.0.0.1:9001", Transport: transportFactory.Name(), Protocol: proto.Name()},
	})
	require.NoError(t, srv.Start())
	t.Cleanup(func() { _ = srv.Stop() })

	transportFactory.MustOpen("listener-a", 1)
	transportFactory.MustOpen("listener-b", 2)
	transportFactory.MustOpen("listener-a", 3)

	summary := srv.SessionSummary()
	require.True(t, summary.AcceptingNewSessions)
	require.Equal(t, 3, summary.GatewaySessions)
	require.Equal(t, map[string]int{"listener-a": 2, "listener-b": 1}, summary.SessionsByListener)
}

func TestServerRejectsOpenWhenNotAccepting(t *testing.T) {
	handler := newTestHandler()
	proto := newScriptedProtocol("jsonrpc")
	srv, transportFactory := newTestServer(t, handler, proto, gateway.SessionOptions{})
	require.NoError(t, srv.Start())
	t.Cleanup(func() { _ = srv.Stop() })

	srv.SetAcceptingNewSessions(false)
	conn := transportFactory.MustOpen("listener-a", 1)

	waitFor(t, func() bool { return connClosed(conn) })
	summary := srv.SessionSummary()
	require.False(t, summary.AcceptingNewSessions)
	require.Equal(t, 0, summary.GatewaySessions)
	require.Empty(t, handler.callOrder())
}
