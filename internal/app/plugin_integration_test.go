package app

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	pluginusecase "github.com/WuKongIM/WuKongIM/internal/usecase/plugin"
	"github.com/WuKongIM/WuKongIM/internal/usecase/plugin/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	wkrpcproto "github.com/WuKongIM/wkrpc/proto"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestPluginSingleNodeClusterSmoke(t *testing.T) {
	cfg := testConfig(t)
	cfg.Plugin.Enable = true
	cfg.Plugin.HotReload = false
	cfg.Plugin.SocketPath = shortPluginSmokeSocketPath(t)
	cfg.Plugin.SetExplicitFlags(false, true, false)
	cfg.API.ListenAddr = "127.0.0.1:0"
	cfg.Manager = validManagerConfigForTest()
	cfg.Manager.ListenAddr = "127.0.0.1:0"
	cfg.Manager.AuthOn = false

	app, err := New(cfg)
	require.NoError(t, err)
	require.NotNil(t, app.pluginRuntime)
	require.NotNil(t, app.pluginRuntime.Socket())

	channelID := runtimechannelid.EncodePersonChannel("plugin-sender", "plugin-recipient")
	require.NoError(t, app.DB().ForSlot(1).UpsertChannelRuntimeMeta(context.Background(), metadb.ChannelRuntimeMeta{
		ChannelID:    channelID,
		ChannelType:  int64(frame.ChannelTypePerson),
		ChannelEpoch: 1,
		LeaderEpoch:  1,
		Replicas:     []uint64{cfg.Node.ID},
		ISR:          []uint64{cfg.Node.ID},
		Leader:       cfg.Node.ID,
		MinISR:       1,
		Status:       uint8(channel.StatusActive),
		Features:     uint64(channel.MessageSeqFormatLegacyU32),
		LeaseUntilMS: time.Now().Add(time.Minute).UnixMilli(),
	}))

	require.NoError(t, app.Start())
	t.Cleanup(func() { require.NoError(t, app.Stop()) })
	require.Eventually(t, func() bool {
		_, err := app.Cluster().LeaderOf(1)
		return err == nil
	}, 3*time.Second, 50*time.Millisecond)

	fake := newFakePluginClient(t, cfg.Plugin.SocketPath, "wk.echo")
	fake.route(pluginusecase.PathRoute, func(body []byte) ([]byte, error) {
		var req pluginproto.HttpRequest
		if err := proto.Unmarshal(body, &req); err != nil {
			return nil, err
		}
		return proto.Marshal(&pluginproto.HttpResponse{
			Status: http.StatusAccepted,
			Headers: map[string]string{
				"Content-Type": "application/json",
				"X-Plugin-No":  "wk.echo",
			},
			Body: []byte(fmt.Sprintf(`{"method":%q,"path":%q,"query":%q,"body":%q}`, req.GetMethod(), req.GetPath(), req.GetQuery()["q"], string(req.GetBody()))),
		})
	})
	fake.start()
	defer fake.stop()

	startupResp := fake.requestProto(t, "/plugin/start", &pluginproto.PluginInfo{
		No:      "wk.echo",
		Name:    "Echo Plugin",
		Version: "0.0.1",
		Methods: []string{string(pluginusecase.MethodRoute)},
	})
	var startup pluginproto.StartupResp
	require.NoError(t, proto.Unmarshal(startupResp.Body, &startup))
	require.Equal(t, cfg.Node.ID, startup.GetNodeId())
	require.True(t, startup.GetSuccess())
	require.NotEmpty(t, startup.GetSandboxDir())

	require.Eventually(t, func() bool {
		observed, ok := app.pluginRuntime.Registry().Get("wk.echo")
		return ok && string(observed.Status) == string(pluginusecase.StatusRunning) && observed.Enabled
	}, time.Second, 20*time.Millisecond)

	managerBody := getHTTPBody(t, "http://"+app.Manager().Addr()+"/manager/nodes/1/plugins")
	require.JSONEq(t, `{"node_id":1,"total":1,"items":[{"node_id":1,"plugin_no":"wk.echo","name":"Echo Plugin","version":"0.0.1","status":"running","enabled":true,"methods":["Route"],"priority":0,"persist_after_sync":false,"reply_sync":false,"is_ai":0,"pid":0,"last_error":""}]}`, stripVolatileManagerPluginFields(t, managerBody))

	req, err := http.NewRequest(http.MethodPost, "http://"+app.API().Addr()+"/plugins/wk.echo/hello?q=codex", strings.NewReader("ping"))
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	publicBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	require.Equal(t, "wk.echo", resp.Header.Get("X-Plugin-No"))
	require.JSONEq(t, `{"method":"POST","path":"/hello","query":"codex","body":"ping"}`, string(publicBody))

	sendResp := fake.requestProto(t, "/message/send", &pluginproto.SendReq{
		Header:      &pluginproto.Header{NoPersist: false},
		ClientMsgNo: "plugin-origin-1",
		FromUid:     "plugin-sender",
		ChannelId:   "plugin-recipient",
		ChannelType: uint32(frame.ChannelTypePerson),
		Payload:     []byte("from plugin"),
	})
	var send pluginproto.SendResp
	require.NoError(t, proto.Unmarshal(sendResp.Body, &send))
	require.NotZero(t, send.GetMessageId())

	fetch, err := app.ChannelLog().Fetch(context.Background(), channel.FetchRequest{
		ChannelID: channel.ChannelID{ID: channelID, Type: frame.ChannelTypePerson},
		FromSeq:   1,
		Limit:     10,
		MaxBytes:  1024,
	})
	require.NoError(t, err)
	require.Len(t, fetch.Messages, 1)
	require.Equal(t, "plugin-origin-1", fetch.Messages[0].ClientMsgNo)
	require.Equal(t, "plugin-sender", fetch.Messages[0].FromUID)
	require.Equal(t, []byte("from plugin"), fetch.Messages[0].Payload)
}

type fakePluginClient struct {
	t       *testing.T
	conn    net.Conn
	reader  *bufio.Reader
	routes  map[string]func([]byte) ([]byte, error)
	writeMu sync.Mutex
	mu      sync.Mutex
	nextID  uint64
	pending map[uint64]chan *wkrpcproto.Response
	errs    chan error
}

func newFakePluginClient(t *testing.T, socketPath, pluginNo string) *fakePluginClient {
	t.Helper()
	conn, err := net.DialTimeout("unix", socketPath, time.Second)
	require.NoError(t, err)
	fake := &fakePluginClient{
		t:       t,
		conn:    conn,
		reader:  bufio.NewReader(conn),
		routes:  make(map[string]func([]byte) ([]byte, error)),
		pending: make(map[uint64]chan *wkrpcproto.Response),
		errs:    make(chan error, 1),
	}
	fake.writeConnect(pluginNo)
	go fake.readLoop()
	return fake
}

func shortPluginSmokeSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "wk-plugin-app-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "plugin.sock")
}

func (f *fakePluginClient) route(path string, handler func([]byte) ([]byte, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[path] = handler
}

func (f *fakePluginClient) start() {}

func (f *fakePluginClient) stop() {
	_ = f.conn.Close()
}

func (f *fakePluginClient) requestProto(t *testing.T, path string, msg proto.Message) *wkrpcproto.Response {
	t.Helper()
	data, err := proto.Marshal(msg)
	require.NoError(t, err)

	f.mu.Lock()
	f.nextID++
	id := f.nextID
	respC := make(chan *wkrpcproto.Response, 1)
	f.pending[id] = respC
	f.mu.Unlock()

	req := &wkrpcproto.Request{Id: id, Path: path, Body: data}
	payload, err := req.Marshal()
	require.NoError(t, err)
	require.NoError(t, f.writeFrame(wkrpcproto.MsgTypeRequest, payload))

	select {
	case resp := <-respC:
		require.Equalf(t, wkrpcproto.StatusOK, resp.Status, "host rpc %s body=%q", path, string(resp.Body))
		return resp
	case err := <-f.errs:
		t.Fatalf("fake plugin read loop: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatalf("host rpc %s timed out", path)
	}
	return nil
}

func (f *fakePluginClient) writeConnect(pluginNo string) {
	connect := &wkrpcproto.Connect{Id: 1, Uid: pluginNo}
	payload, err := connect.Marshal()
	require.NoError(f.t, err)
	require.NoError(f.t, f.writeFrame(wkrpcproto.MsgTypeConnect, payload))
	msgType, body, err := f.readFrame()
	require.NoError(f.t, err)
	require.Equal(f.t, wkrpcproto.MsgTypeConnack, msgType)
	var ack wkrpcproto.Connack
	require.NoError(f.t, ack.Unmarshal(body))
	require.Equal(f.t, wkrpcproto.StatusOK, ack.Status, string(ack.Body))
}

func (f *fakePluginClient) readLoop() {
	for {
		msgType, body, err := f.readFrame()
		if err != nil {
			select {
			case f.errs <- err:
			default:
			}
			return
		}
		switch msgType {
		case wkrpcproto.MsgTypeResp:
			var resp wkrpcproto.Response
			if err := resp.Unmarshal(body); err != nil {
				f.reportReadErr(err)
				return
			}
			f.mu.Lock()
			respC := f.pending[resp.Id]
			delete(f.pending, resp.Id)
			f.mu.Unlock()
			if respC != nil {
				respC <- &resp
			}
		case wkrpcproto.MsgTypeRequest:
			f.handleHostRequest(body)
		case wkrpcproto.MsgTypeHeartbeat:
			if err := f.writeFrame(wkrpcproto.MsgTypeHeartbeat, []byte{wkrpcproto.MsgTypeHeartbeat.Uint8()}); err != nil {
				f.reportReadErr(err)
				return
			}
		default:
			f.reportReadErr(fmt.Errorf("unexpected wkrpc frame type %s", msgType.String()))
			return
		}
	}
}

func (f *fakePluginClient) reportReadErr(err error) {
	select {
	case f.errs <- err:
	default:
	}
}

func (f *fakePluginClient) handleHostRequest(body []byte) {
	var req wkrpcproto.Request
	if err := req.Unmarshal(body); err != nil {
		f.reportReadErr(err)
		return
	}
	resp := &wkrpcproto.Response{Id: req.Id, Status: wkrpcproto.StatusOK, Timestamp: time.Now().UnixMilli()}
	f.mu.Lock()
	handler := f.routes[req.Path]
	f.mu.Unlock()
	if handler == nil {
		resp.Status = wkrpcproto.StatusError
		resp.Body = []byte("route not found")
	} else if data, err := handler(req.Body); err != nil {
		resp.Status = wkrpcproto.StatusError
		resp.Body = []byte(err.Error())
	} else {
		resp.Body = data
	}
	payload, err := resp.Marshal()
	if err != nil {
		f.reportReadErr(err)
		return
	}
	if err := f.writeFrame(wkrpcproto.MsgTypeResp, payload); err != nil {
		f.reportReadErr(err)
	}
}

func (f *fakePluginClient) writeFrame(msgType wkrpcproto.MsgType, payload []byte) error {
	frameData := make([]byte, len(wkrpcproto.MagicNumberStart)+1+4+len(payload))
	copy(frameData, wkrpcproto.MagicNumberStart)
	frameData[len(wkrpcproto.MagicNumberStart)] = msgType.Uint8()
	binary.BigEndian.PutUint32(frameData[len(wkrpcproto.MagicNumberStart)+1:], uint32(len(payload)))
	copy(frameData[len(wkrpcproto.MagicNumberStart)+1+4:], payload)
	f.writeMu.Lock()
	defer f.writeMu.Unlock()
	n, err := f.conn.Write(frameData)
	if err != nil {
		return err
	}
	if n != len(frameData) {
		return io.ErrShortWrite
	}
	return nil
}

func (f *fakePluginClient) readFrame() (wkrpcproto.MsgType, []byte, error) {
	if err := f.conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		return 0, nil, err
	}
	header := make([]byte, len(wkrpcproto.MagicNumberStart)+1+4)
	if _, err := io.ReadFull(f.reader, header); err != nil {
		return 0, nil, err
	}
	if string(wkrpcproto.MagicNumberStart) != string(header[:len(wkrpcproto.MagicNumberStart)]) {
		return 0, nil, fmt.Errorf("invalid wkrpc magic: %q", header[:len(wkrpcproto.MagicNumberStart)])
	}
	msgType := wkrpcproto.MsgType(header[len(wkrpcproto.MagicNumberStart)])
	if msgType == wkrpcproto.MsgTypeHeartbeat {
		return msgType, nil, nil
	}
	length := binary.BigEndian.Uint32(header[len(wkrpcproto.MagicNumberStart)+1:])
	body := make([]byte, length)
	if _, err := io.ReadFull(f.reader, body); err != nil {
		return 0, nil, err
	}
	return msgType, body, nil
}

func getHTTPBody(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
	return string(body)
}

func stripVolatileManagerPluginFields(t *testing.T, body string) string {
	t.Helper()
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &payload))
	items, ok := payload["items"].([]any)
	require.True(t, ok)
	for _, item := range items {
		obj, ok := item.(map[string]any)
		require.True(t, ok)
		delete(obj, "last_seen_at")
		delete(obj, "config_template")
	}
	data, err := json.Marshal(payload)
	require.NoError(t, err)
	return string(data)
}
