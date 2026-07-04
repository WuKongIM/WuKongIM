package node

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/transport"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func TestPluginHTTPForwardCodecRoundTripAndDropsOversizedBody(t *testing.T) {
	req := pluginHTTPForwardRequest{
		PluginNo: "wk.plugin.echo",
		Request: &pluginproto.HttpRequest{
			Method:  http.MethodPost,
			Path:    "/hello",
			Headers: map[string]string{"X-Trace": "trace-1"},
			Query:   map[string]string{"name": "alice"},
			Body:    []byte("payload"),
		},
	}

	raw, err := encodePluginHTTPForwardRequest(req)
	require.NoError(t, err)
	got, err := decodePluginHTTPForwardRequest(raw)
	require.NoError(t, err)
	require.Equal(t, req.PluginNo, got.PluginNo)
	require.Equal(t, req.Request.GetMethod(), got.Request.GetMethod())
	require.Equal(t, req.Request.GetPath(), got.Request.GetPath())
	require.Equal(t, "trace-1", got.Request.GetHeaders()["X-Trace"])
	require.Equal(t, "alice", got.Request.GetQuery()["name"])
	require.Equal(t, []byte("payload"), got.Request.GetBody())

	_, err = encodePluginHTTPForwardRequest(pluginHTTPForwardRequest{PluginNo: "wk.plugin.echo", Request: &pluginproto.HttpRequest{Body: make([]byte, maxPluginHTTPForwardBodyBytes+1)}})
	require.Error(t, err)
}

func TestPluginHTTPForwardCodecRejectsTooManyHeaders(t *testing.T) {
	headers := make(map[string]string, maxPluginHTTPForwardMapEntries+1)
	for i := 0; i <= maxPluginHTTPForwardMapEntries; i++ {
		headers[fmt.Sprintf("X-%d", i)] = "v"
	}

	_, err := encodePluginHTTPForwardRequest(pluginHTTPForwardRequest{PluginNo: "wk.plugin.echo", Request: &pluginproto.HttpRequest{Headers: headers}})

	require.Error(t, err)
}

func TestPluginHTTPForwardResponseCodecRoundTrip(t *testing.T) {
	resp := pluginHTTPForwardResponse{Status: rpcStatusOK, Response: &pluginproto.HttpResponse{Status: http.StatusAccepted, Headers: map[string]string{"X-Plugin": "ok"}, Body: []byte("accepted")}}

	raw, err := encodePluginHTTPForwardResponse(resp)
	require.NoError(t, err)
	got, err := decodePluginHTTPForwardResponse(raw)
	require.NoError(t, err)

	require.Equal(t, rpcStatusOK, got.Status)
	require.NotNil(t, got.Response)
	require.Equal(t, int32(http.StatusAccepted), got.Response.GetStatus())
	require.Equal(t, "ok", got.Response.GetHeaders()["X-Plugin"])
	require.Equal(t, []byte("accepted"), got.Response.GetBody())
}

func TestPluginHTTPForwardClientUsesNodeRPC(t *testing.T) {
	cluster := &capturingPluginHTTPForwardCluster{response: mustEncodePluginHTTPForwardResponse(t, pluginHTTPForwardResponse{Status: rpcStatusOK, Response: &pluginproto.HttpResponse{Status: http.StatusNoContent}})}
	client := NewClient(cluster)

	resp, err := client.ForwardPluginHTTP(context.Background(), 7, &pluginproto.ForwardHttpReq{PluginNo: "wk.plugin.echo", ToNodeId: 7, Request: &pluginproto.HttpRequest{Path: "/remote"}})

	require.NoError(t, err)
	require.Equal(t, int32(http.StatusNoContent), resp.GetStatus())
	require.Equal(t, multiraft.NodeID(7), cluster.nodeID)
	require.Equal(t, pluginHTTPForwardRPCServiceID, cluster.serviceID)
	decoded, err := decodePluginHTTPForwardRequest(cluster.payload)
	require.NoError(t, err)
	require.Equal(t, "wk.plugin.echo", decoded.PluginNo)
	require.Equal(t, "/remote", decoded.Request.GetPath())
}

func TestPluginHTTPForwardHandlerRoutesToLocalProvider(t *testing.T) {
	provider := &recordingPluginHTTPRouteProvider{resp: &pluginproto.HttpResponse{Status: http.StatusCreated, Body: []byte("created")}}
	adapter := New(Options{Cluster: &singleMuxCluster{mux: transport.NewRPCMux()}, PluginHTTPRoutes: provider})

	body, err := encodePluginHTTPForwardRequest(pluginHTTPForwardRequest{PluginNo: "wk.plugin.echo", Request: &pluginproto.HttpRequest{Method: http.MethodGet, Path: "/hello"}})
	require.NoError(t, err)
	respBody, err := adapter.handlePluginHTTPForwardRPC(context.Background(), body)

	require.NoError(t, err)
	resp, err := decodePluginHTTPForwardResponse(respBody)
	require.NoError(t, err)
	require.Equal(t, rpcStatusOK, resp.Status)
	require.Equal(t, int32(http.StatusCreated), resp.Response.GetStatus())
	require.Equal(t, 1, provider.calls)
	require.Equal(t, "wk.plugin.echo", provider.pluginNo)
	require.Equal(t, "/hello", provider.req.GetPath())
}

func TestPluginHTTPForwardHandlerUnavailableWithoutProvider(t *testing.T) {
	adapter := New(Options{})
	body, err := encodePluginHTTPForwardRequest(pluginHTTPForwardRequest{PluginNo: "wk.plugin.echo", Request: &pluginproto.HttpRequest{Path: "/hello"}})
	require.NoError(t, err)

	respBody, err := adapter.handlePluginHTTPForwardRPC(context.Background(), body)

	require.NoError(t, err)
	resp, err := decodePluginHTTPForwardResponse(respBody)
	require.NoError(t, err)
	require.Equal(t, rpcStatusUnavailable, resp.Status)
}

func TestPluginHTTPForwardClientReturnsRemoteError(t *testing.T) {
	cluster := &capturingPluginHTTPForwardCluster{response: mustEncodePluginHTTPForwardResponse(t, pluginHTTPForwardResponse{Status: rpcStatusUnavailable, Error: "plugin route failed"})}
	client := NewClient(cluster)

	_, err := client.ForwardPluginHTTP(context.Background(), 7, &pluginproto.ForwardHttpReq{PluginNo: "wk.plugin.echo"})

	require.Error(t, err)
	require.Contains(t, err.Error(), "plugin route failed")
}

type recordingPluginHTTPRouteProvider struct {
	calls    int
	pluginNo string
	req      *pluginproto.HttpRequest
	resp     *pluginproto.HttpResponse
	err      error
}

func (r *recordingPluginHTTPRouteProvider) Route(_ context.Context, pluginNo string, req *pluginproto.HttpRequest) (*pluginproto.HttpResponse, error) {
	r.calls++
	r.pluginNo = pluginNo
	r.req = req
	if r.err != nil {
		return nil, r.err
	}
	if r.resp != nil {
		return r.resp, nil
	}
	return &pluginproto.HttpResponse{Status: http.StatusOK}, nil
}

type capturingPluginHTTPForwardCluster struct {
	nodeID    multiraft.NodeID
	serviceID uint8
	payload   []byte
	response  []byte
	err       error
}

func (c *capturingPluginHTTPForwardCluster) RPCMux() *transport.RPCMux { return nil }
func (c *capturingPluginHTTPForwardCluster) LeaderOf(multiraft.SlotID) (multiraft.NodeID, error) {
	return 0, nil
}
func (c *capturingPluginHTTPForwardCluster) IsLocal(multiraft.NodeID) bool      { return false }
func (c *capturingPluginHTTPForwardCluster) SlotForKey(string) multiraft.SlotID { return 0 }
func (c *capturingPluginHTTPForwardCluster) RPCService(_ context.Context, nodeID multiraft.NodeID, _ multiraft.SlotID, serviceID uint8, payload []byte) ([]byte, error) {
	c.nodeID = nodeID
	c.serviceID = serviceID
	c.payload = append([]byte(nil), payload...)
	if c.err != nil {
		return nil, c.err
	}
	return c.response, nil
}
func (c *capturingPluginHTTPForwardCluster) PeersForSlot(multiraft.SlotID) []multiraft.NodeID {
	return nil
}

type singleMuxCluster struct {
	mux *transport.RPCMux
}

func (c *singleMuxCluster) RPCMux() *transport.RPCMux                           { return c.mux }
func (c *singleMuxCluster) LeaderOf(multiraft.SlotID) (multiraft.NodeID, error) { return 0, nil }
func (c *singleMuxCluster) IsLocal(multiraft.NodeID) bool                       { return true }
func (c *singleMuxCluster) SlotForKey(string) multiraft.SlotID                  { return 0 }
func (c *singleMuxCluster) RPCService(context.Context, multiraft.NodeID, multiraft.SlotID, uint8, []byte) ([]byte, error) {
	return nil, errors.New("unexpected remote rpc")
}
func (c *singleMuxCluster) PeersForSlot(multiraft.SlotID) []multiraft.NodeID { return nil }

func mustEncodePluginHTTPForwardResponse(t *testing.T, resp pluginHTTPForwardResponse) []byte {
	t.Helper()
	body, err := encodePluginHTTPForwardResponse(resp)
	require.NoError(t, err)
	return body
}
