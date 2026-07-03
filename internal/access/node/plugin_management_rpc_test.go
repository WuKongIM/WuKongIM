package node

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	pluginusecase "github.com/WuKongIM/WuKongIM/internal/usecase/plugin"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/stretchr/testify/require"
)

func TestPluginManagementCodecRoundTripAndBoundsConfig(t *testing.T) {
	now := time.Date(2026, 5, 16, 10, 30, 0, 0, time.UTC)
	detail := pluginusecase.LocalPluginDetail{
		NodeID:         2,
		No:             "wk.echo",
		Name:           "Echo",
		Version:        "1.0.0",
		ConfigTemplate: &pluginproto.ConfigTemplate{Fields: []*pluginproto.Field{{Name: "api_key", Type: pluginproto.FieldTypeSecret.String(), Label: "API Key"}}},
		Config:         map[string]any{"api_key": pluginusecase.SecretHidden},
		CreatedAt:      &now,
		UpdatedAt:      &now,
		Status:         pluginusecase.StatusRunning,
		Enabled:        true,
		Methods:        []pluginusecase.Method{pluginusecase.MethodRoute, pluginusecase.MethodSend},
		Priority:       9,
		ReplySync:      true,
		IsAI:           1,
		PID:            123,
		LastSeenAt:     now,
		LastError:      "",
	}
	resp := pluginManagementResponse{Status: rpcStatusOK, List: &pluginusecase.LocalPluginList{NodeID: 2, Plugins: []pluginusecase.LocalPlugin{detail}}, Detail: &detail}

	raw, err := encodePluginManagementResponse(resp)
	require.NoError(t, err)
	got, err := decodePluginManagementResponse(raw)
	require.NoError(t, err)

	require.Equal(t, rpcStatusOK, got.Status)
	require.NotNil(t, got.List)
	require.Equal(t, uint64(2), got.List.NodeID)
	require.Equal(t, "wk.echo", got.List.Plugins[0].No)
	require.Equal(t, pluginusecase.SecretHidden, got.List.Plugins[0].Config["api_key"])
	require.NotNil(t, got.Detail)
	require.Equal(t, pluginusecase.MethodRoute, got.Detail.Methods[0])
	require.Equal(t, "api_key", got.Detail.ConfigTemplate.GetFields()[0].GetName())

	tooLarge := detail
	tooLarge.Config = map[string]any{"blob": string(make([]byte, maxPluginManagementConfigBytes+1))}
	_, err = encodePluginManagementResponse(pluginManagementResponse{Status: rpcStatusOK, Detail: &tooLarge})
	require.Error(t, err)
}

func TestPluginManagementRequestCodecRoundTrip(t *testing.T) {
	req := pluginManagementRequest{Op: pluginManagementOpUpdateConfig, NodeID: 2, PluginNo: "wk.echo", Config: json.RawMessage(`{"enabled":true}`)}

	raw, err := encodePluginManagementRequest(req)
	require.NoError(t, err)
	got, err := decodePluginManagementRequest(raw)
	require.NoError(t, err)

	require.Equal(t, pluginManagementOpUpdateConfig, got.Op)
	require.Equal(t, uint64(2), got.NodeID)
	require.Equal(t, "wk.echo", got.PluginNo)
	require.JSONEq(t, `{"enabled":true}`, string(got.Config))
}

func TestPluginManagementHandlerDelegatesProvider(t *testing.T) {
	provider := &recordingPluginManagementProvider{listResp: pluginusecase.LocalPluginList{NodeID: 1, Plugins: []pluginusecase.LocalPlugin{{NodeID: 1, No: "wk.echo"}}}}
	adapter := New(Options{PluginManagement: provider})
	body, err := encodePluginManagementRequest(pluginManagementRequest{Op: pluginManagementOpList, NodeID: 1})
	require.NoError(t, err)

	respBody, err := adapter.handlePluginManagementRPC(context.Background(), body)

	require.NoError(t, err)
	resp, err := decodePluginManagementResponse(respBody)
	require.NoError(t, err)
	require.Equal(t, rpcStatusOK, resp.Status)
	require.Equal(t, 1, provider.listCalls)
	require.Equal(t, "wk.echo", resp.List.Plugins[0].No)
}

func TestPluginManagementHandlerUnavailableWithoutProvider(t *testing.T) {
	adapter := New(Options{})
	body, err := encodePluginManagementRequest(pluginManagementRequest{Op: pluginManagementOpList, NodeID: 1})
	require.NoError(t, err)

	respBody, err := adapter.handlePluginManagementRPC(context.Background(), body)

	require.NoError(t, err)
	resp, err := decodePluginManagementResponse(respBody)
	require.NoError(t, err)
	require.Equal(t, rpcStatusUnavailable, resp.Status)
}

func TestPluginManagementClientUsesNodeRPCAndMapsStatuses(t *testing.T) {
	detail := pluginusecase.LocalPluginDetail{NodeID: 7, No: "wk.echo"}
	cluster := &capturingPluginManagementCluster{response: mustEncodePluginManagementResponse(t, pluginManagementResponse{Status: rpcStatusOK, Detail: &detail})}
	client := NewClient(cluster)

	got, err := client.GetNodePlugin(context.Background(), 7, "wk.echo")

	require.NoError(t, err)
	require.Equal(t, "wk.echo", got.No)
	require.Equal(t, multiraft.NodeID(7), cluster.nodeID)
	require.Equal(t, pluginManagementRPCServiceID, cluster.serviceID)
	decoded, err := decodePluginManagementRequest(cluster.payload)
	require.NoError(t, err)
	require.Equal(t, pluginManagementOpGet, decoded.Op)
	require.Equal(t, "wk.echo", decoded.PluginNo)

	cluster.response = mustEncodePluginManagementResponse(t, pluginManagementResponse{Status: rpcStatusUnsupported})
	_, err = client.GetNodePlugin(context.Background(), 7, "wk.echo")
	require.ErrorIs(t, err, ErrPluginManagementUnsupported)

	cluster.response = mustEncodePluginManagementResponse(t, pluginManagementResponse{Status: rpcStatusUnavailable, Error: "plugin disabled"})
	_, err = client.GetNodePlugin(context.Background(), 7, "wk.echo")
	require.ErrorIs(t, err, ErrPluginManagementUnavailable)
	require.Contains(t, err.Error(), "plugin disabled")
}

type recordingPluginManagementProvider struct {
	listCalls int
	listResp  pluginusecase.LocalPluginList
	listErr   error
	getErr    error
}

func (r *recordingPluginManagementProvider) ListLocalPlugins(context.Context) (pluginusecase.LocalPluginList, error) {
	r.listCalls++
	return r.listResp, r.listErr
}

func (r *recordingPluginManagementProvider) GetLocalPlugin(context.Context, string) (pluginusecase.LocalPluginDetail, error) {
	if r.getErr != nil {
		return pluginusecase.LocalPluginDetail{}, r.getErr
	}
	return pluginusecase.LocalPluginDetail{}, errors.New("unexpected get")
}

func (r *recordingPluginManagementProvider) UpdateLocalConfig(context.Context, string, json.RawMessage) (pluginusecase.LocalPluginDetail, error) {
	return pluginusecase.LocalPluginDetail{}, errors.New("unexpected update")
}

func (r *recordingPluginManagementProvider) RestartLocalPlugin(context.Context, string) (pluginusecase.LocalPluginDetail, error) {
	return pluginusecase.LocalPluginDetail{}, errors.New("unexpected restart")
}

func (r *recordingPluginManagementProvider) UninstallLocalPlugin(context.Context, string) error {
	return errors.New("unexpected uninstall")
}

type capturingPluginManagementCluster struct {
	nodeID    multiraft.NodeID
	serviceID uint8
	payload   []byte
	response  []byte
	err       error
}

func (c *capturingPluginManagementCluster) RPCMux() *transport.RPCMux { return nil }
func (c *capturingPluginManagementCluster) LeaderOf(multiraft.SlotID) (multiraft.NodeID, error) {
	return 0, nil
}
func (c *capturingPluginManagementCluster) IsLocal(multiraft.NodeID) bool      { return false }
func (c *capturingPluginManagementCluster) SlotForKey(string) multiraft.SlotID { return 0 }
func (c *capturingPluginManagementCluster) RPCService(_ context.Context, nodeID multiraft.NodeID, _ multiraft.SlotID, serviceID uint8, payload []byte) ([]byte, error) {
	c.nodeID = nodeID
	c.serviceID = serviceID
	c.payload = append([]byte(nil), payload...)
	if c.err != nil {
		return nil, c.err
	}
	return c.response, nil
}
func (c *capturingPluginManagementCluster) PeersForSlot(multiraft.SlotID) []multiraft.NodeID {
	return nil
}

func mustEncodePluginManagementResponse(t *testing.T, resp pluginManagementResponse) []byte {
	t.Helper()
	body, err := encodePluginManagementResponse(resp)
	require.NoError(t, err)
	return body
}

func TestPluginManagementHandlerPreservesDomainErrors(t *testing.T) {
	provider := &recordingPluginManagementProvider{getErr: pluginusecase.ErrPluginNotFound}
	adapter := New(Options{PluginManagement: provider})
	body, err := encodePluginManagementRequest(pluginManagementRequest{Op: pluginManagementOpGet, NodeID: 1, PluginNo: "missing"})
	require.NoError(t, err)

	respBody, err := adapter.handlePluginManagementRPC(context.Background(), body)

	require.NoError(t, err)
	resp, err := decodePluginManagementResponse(respBody)
	require.NoError(t, err)
	require.Equal(t, pluginManagementStatusNotFound, resp.Status)

	cluster := &capturingPluginManagementCluster{response: respBody}
	client := NewClient(cluster)
	_, err = client.GetNodePlugin(context.Background(), 1, "missing")
	require.ErrorIs(t, err, pluginusecase.ErrPluginNotFound)
}

func TestPluginManagementClientMapsUnknownServiceToUnsupported(t *testing.T) {
	client := NewClient(&capturingPluginManagementCluster{err: errors.New("unknown rpc service 55")})

	_, err := client.ListNodePlugins(context.Background(), 7)

	require.ErrorIs(t, err, ErrPluginManagementUnsupported)
}

func TestPluginManagementCodecRejectsAsymmetricBounds(t *testing.T) {
	plugins := make([]pluginusecase.LocalPlugin, maxPluginManagementPlugins+1)
	_, err := encodePluginManagementResponse(pluginManagementResponse{Status: rpcStatusOK, List: &pluginusecase.LocalPluginList{NodeID: 1, Plugins: plugins}})
	require.Error(t, err)

	methods := make([]pluginusecase.Method, maxPluginManagementMethods+1)
	_, err = encodePluginManagementResponse(pluginManagementResponse{Status: rpcStatusOK, Detail: &pluginusecase.LocalPluginDetail{NodeID: 1, No: "wk.echo", Methods: methods}})
	require.Error(t, err)
}

func TestPluginManagementCodecRejectsNarrowingOverflow(t *testing.T) {
	raw := malformedPluginManagementResponseWithPriority(^uint64(0))

	_, err := decodePluginManagementResponse(raw)

	require.Error(t, err)
}

func malformedPluginManagementResponseWithPriority(priority uint64) []byte {
	dst := make([]byte, 0, 128)
	dst = append(dst, pluginManagementResponseMagic[:]...)
	dst = appendString(dst, rpcStatusOK)
	dst = appendString(dst, "") // error code
	dst = appendString(dst, "")
	dst = appendPluginManagementBool(dst, false) // list absent
	dst = appendPluginManagementBool(dst, true)  // detail present
	dst = appendUvarint(dst, 1)                  // node_id
	dst = appendString(dst, "wk.echo")
	dst = appendString(dst, "Echo")
	dst = appendString(dst, "1.0.0")
	dst = appendBytes(dst, nil) // template
	dst = appendBytes(dst, nil) // config
	dst = appendPluginManagementBool(dst, false)
	dst = appendPluginManagementBool(dst, false)
	dst = appendString(dst, string(pluginusecase.StatusRunning))
	dst = appendPluginManagementBool(dst, true)
	dst = appendUvarint(dst, 0) // methods
	dst = appendUvarint(dst, priority)
	dst = appendPluginManagementBool(dst, false)
	dst = appendPluginManagementBool(dst, false)
	dst = appendUvarint(dst, 0) // is_ai
	dst = appendUvarint(dst, 0) // pid
	dst = appendUvarint(dst, 0) // last_seen_at
	dst = appendString(dst, "")
	return dst
}

func TestPluginManagementClientMapsTransportFailureToUnavailable(t *testing.T) {
	client := NewClient(&capturingPluginManagementCluster{err: errors.New("dial target: connection refused")})

	_, err := client.ListNodePlugins(context.Background(), 7)

	require.ErrorIs(t, err, ErrPluginManagementUnavailable)
}

func TestPluginManagementRequestCodecRejectsOversizedRequestAndStrings(t *testing.T) {
	tooLarge := append([]byte{}, pluginManagementRequestMagic[:]...)
	tooLarge = appendString(tooLarge, pluginManagementOpList)
	tooLarge = appendUvarint(tooLarge, 1)
	tooLarge = appendString(tooLarge, "wk.echo")
	tooLarge = appendBytes(tooLarge, make([]byte, maxPluginManagementRequestBytes-len(tooLarge)+1))

	_, err := decodePluginManagementRequest(tooLarge)
	require.Error(t, err)

	oversizedPluginNo := append([]byte{}, pluginManagementRequestMagic[:]...)
	oversizedPluginNo = appendString(oversizedPluginNo, pluginManagementOpGet)
	oversizedPluginNo = appendUvarint(oversizedPluginNo, 1)
	oversizedPluginNo = appendString(oversizedPluginNo, string(make([]byte, maxPluginManagementStringBytes+1)))
	oversizedPluginNo = appendBytes(oversizedPluginNo, nil)

	_, err = decodePluginManagementRequest(oversizedPluginNo)
	require.Error(t, err)
}
