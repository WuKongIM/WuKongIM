package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
	wkrpcproto "github.com/WuKongIM/wkrpc/proto"
	"google.golang.org/protobuf/proto"
)

const readyFile = "http_forward.ready"
const resultsFile = "http_forward.jsonl"

// client owns the plugin-side WKRPC connection used by this compatibility helper.
type client struct {
	conn    net.Conn
	reader  *bufio.Reader
	sandbox string
	no      string

	writeMu sync.Mutex
	mu      sync.Mutex
	nextID  uint64
	// pending routes plugin-origin host RPC requests by request id.
	pending map[uint64]chan *wkrpcproto.Response
	// errs reports fatal read-loop or protocol errors back to main.
	errs chan error
	// stopped closes after the host sends /stop and the helper writes OK.
	stopped  chan struct{}
	stopOnce sync.Once
}

func main() {
	socketPath := flag.String("socket", "", "plugin host Unix socket")
	sandboxDir := flag.String("sandbox", "", "plugin sandbox directory")
	flag.Parse()

	if err := run(*socketPath, *sandboxDir); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "httpforward: %v\n", err)
		os.Exit(1)
	}
}

func run(socketPath, sandboxDir string) error {
	if socketPath == "" {
		return errors.New("--socket is required")
	}
	if sandboxDir == "" {
		return errors.New("--sandbox is required")
	}
	if err := os.MkdirAll(sandboxDir, 0o755); err != nil {
		return fmt.Errorf("create sandbox: %w", err)
	}
	conn, err := net.DialTimeout("unix", socketPath, 3*time.Second)
	if err != nil {
		return fmt.Errorf("connect host socket: %w", err)
	}
	defer conn.Close()

	c := &client{
		conn:    conn,
		reader:  bufio.NewReader(conn),
		sandbox: sandboxDir,
		no:      pluginNoFromExecutable(),
		pending: make(map[uint64]chan *wkrpcproto.Response),
		errs:    make(chan error, 1),
		stopped: make(chan struct{}),
	}
	if err := c.connect(); err != nil {
		return err
	}
	go c.readLoop()
	if err := c.startPlugin(context.Background()); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(sandboxDir, readyFile), []byte("ready\n"), 0o644); err != nil {
		return fmt.Errorf("write ready marker: %w", err)
	}
	if role, _ := os.ReadFile(filepath.Join(sandboxDir, "role")); strings.TrimSpace(string(role)) == "initiator" {
		go func() {
			if err := c.runInitiator(); err != nil {
				c.reportErr(err)
			}
		}()
	}

	select {
	case <-c.stopped:
		return nil
	case err := <-c.errs:
		return err
	}
}

func pluginNoFromExecutable() string {
	base := filepath.Base(os.Args[0])
	base = strings.TrimSuffix(base, filepath.Ext(base))
	if base == "" {
		return "httpforward"
	}
	return base
}

func (c *client) connect() error {
	connect := &wkrpcproto.Connect{Id: 1, Uid: c.no}
	payload, err := connect.Marshal()
	if err != nil {
		return err
	}
	if err := c.writeFrame(wkrpcproto.MsgTypeConnect, payload); err != nil {
		return err
	}
	msgType, body, err := c.readFrame()
	if err != nil {
		return err
	}
	if msgType != wkrpcproto.MsgTypeConnack {
		return fmt.Errorf("connect received %s, want connack", msgType.String())
	}
	var ack wkrpcproto.Connack
	if err := ack.Unmarshal(body); err != nil {
		return err
	}
	if ack.Status != wkrpcproto.StatusOK {
		return fmt.Errorf("connect rejected: status=%d body=%q", ack.Status, string(ack.Body))
	}
	return nil
}

func (c *client) startPlugin(ctx context.Context) error {
	body, err := proto.Marshal(&pluginproto.PluginInfo{
		No:      c.no,
		Name:    "HTTPForward Compatibility Plugin",
		Version: "0.0.1",
	})
	if err != nil {
		return err
	}
	resp, err := c.request(ctx, "/plugin/start", body)
	if err != nil {
		return err
	}
	var startup pluginproto.StartupResp
	if err := proto.Unmarshal(resp.Body, &startup); err != nil {
		return err
	}
	if !startup.GetSuccess() {
		return errors.New("host rejected plugin start")
	}
	return nil
}

func (c *client) request(ctx context.Context, path string, body []byte) (*wkrpcproto.Response, error) {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	respC := make(chan *wkrpcproto.Response, 1)
	c.pending[id] = respC
	c.mu.Unlock()

	req := &wkrpcproto.Request{Id: id, Path: path, Body: body}
	payload, err := req.Marshal()
	if err != nil {
		c.removePending(id)
		return nil, err
	}
	if err := c.writeFrame(wkrpcproto.MsgTypeRequest, payload); err != nil {
		c.removePending(id)
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	select {
	case resp := <-respC:
		if resp.Status != wkrpcproto.StatusOK {
			return nil, fmt.Errorf("host rpc %s status=%d body=%q", path, resp.Status, string(resp.Body))
		}
		return resp, nil
	case err := <-c.errs:
		return nil, err
	case <-ctx.Done():
		c.removePending(id)
		return nil, ctx.Err()
	}
}

func (c *client) removePending(id uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.pending, id)
}

func (c *client) readLoop() {
	for {
		msgType, body, err := c.readFrame()
		if err != nil {
			c.reportErr(err)
			return
		}
		switch msgType {
		case wkrpcproto.MsgTypeResp:
			c.handleResponse(body)
		case wkrpcproto.MsgTypeRequest:
			c.handleRequest(body)
		case wkrpcproto.MsgTypeHeartbeat:
			if err := c.writeFrame(wkrpcproto.MsgTypeHeartbeat, nil); err != nil {
				c.reportErr(err)
				return
			}
		default:
			c.reportErr(fmt.Errorf("unexpected wkrpc frame type %s", msgType.String()))
			return
		}
	}
}

func (c *client) handleResponse(body []byte) {
	var resp wkrpcproto.Response
	if err := resp.Unmarshal(body); err != nil {
		c.reportErr(err)
		return
	}
	c.mu.Lock()
	respC := c.pending[resp.Id]
	delete(c.pending, resp.Id)
	c.mu.Unlock()
	if respC != nil {
		respC <- &resp
	}
}

func (c *client) handleRequest(body []byte) {
	var req wkrpcproto.Request
	resp := &wkrpcproto.Response{Status: wkrpcproto.StatusOK, Timestamp: time.Now().UnixMilli()}
	if err := req.Unmarshal(body); err != nil {
		resp.Status = wkrpcproto.StatusError
		resp.Body = []byte(err.Error())
	} else {
		resp.Id = req.Id
		switch req.Path {
		case "/plugin/route":
			routeResp, err := c.handlePluginRoute(req.Body)
			if err != nil {
				resp.Status = wkrpcproto.StatusError
				resp.Body = []byte(err.Error())
			} else {
				resp.Body = routeResp
			}
		case "/stop":
		default:
			resp.Status = wkrpcproto.StatusError
			resp.Body = []byte("route not found")
		}
	}
	payload, err := resp.Marshal()
	if err != nil {
		c.reportErr(err)
		return
	}
	if err := c.writeFrame(wkrpcproto.MsgTypeResp, payload); err != nil {
		c.reportErr(err)
		return
	}
	if req.Path == "/stop" && resp.Status == wkrpcproto.StatusOK {
		c.stopOnce.Do(func() { close(c.stopped) })
	}
}

func (c *client) handlePluginRoute(body []byte) ([]byte, error) {
	var req pluginproto.HttpRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	return proto.Marshal(&pluginproto.HttpResponse{
		Status: 200,
		Headers: map[string]string{
			"X-Plugin-No": c.no,
		},
		Body: []byte(fmt.Sprintf("route:%s:%s:%s", c.no, req.GetPath(), string(req.GetBody()))),
	})
}

func (c *client) runInitiator() error {
	local, err := c.forwardWithRetry("local", 0, "/local", []byte("local-payload"))
	if err != nil {
		return err
	}
	if err := c.appendRecord("local", local); err != nil {
		return err
	}
	remote, err := c.forwardWithRetry("remote", 2, "/remote", []byte("remote-payload"))
	if err != nil {
		return err
	}
	if err := c.appendRecord("remote", remote); err != nil {
		return err
	}
	fanoutErr := c.forwardExpectError(context.Background(), -1, "/fanout", []byte("fanout-payload"))
	if fanoutErr == nil {
		return errors.New("fanout httpForward unexpectedly succeeded")
	}
	return c.appendErrorRecord("fanout", fanoutErr)
}

func (c *client) forwardWithRetry(mode string, toNodeID int64, path string, body []byte) (*pluginproto.HttpResponse, error) {
	deadline := time.Now().Add(10 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		resp, err := c.forwardHTTP(ctx, toNodeID, path, body)
		cancel()
		if err == nil && resp.GetStatus() == 200 {
			return resp, nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("status=%d body=%q", resp.GetStatus(), string(resp.GetBody()))
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, fmt.Errorf("%s httpForward to node %d failed: %w", mode, toNodeID, lastErr)
}

func (c *client) forwardExpectError(ctx context.Context, toNodeID int64, path string, body []byte) error {
	_, err := c.forwardHTTP(ctx, toNodeID, path, body)
	return err
}

func (c *client) forwardHTTP(ctx context.Context, toNodeID int64, path string, body []byte) (*pluginproto.HttpResponse, error) {
	data, err := proto.Marshal(&pluginproto.ForwardHttpReq{
		PluginNo: c.no,
		ToNodeId: toNodeID,
		Request: &pluginproto.HttpRequest{
			Method: "POST",
			Path:   path,
			Body:   append([]byte(nil), body...),
		},
	})
	if err != nil {
		return nil, err
	}
	resp, err := c.request(ctx, "/plugin/httpForward", data)
	if err != nil {
		return nil, err
	}
	var httpResp pluginproto.HttpResponse
	if err := proto.Unmarshal(resp.Body, &httpResp); err != nil {
		return nil, err
	}
	return &httpResp, nil
}

func (c *client) appendRecord(mode string, resp *pluginproto.HttpResponse) error {
	path := filepath.Join(c.sandbox, resultsFile)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	return json.NewEncoder(file).Encode(struct {
		Mode   string `json:"mode"`
		Status int32  `json:"status"`
		Body   string `json:"body"`
	}{
		Mode:   mode,
		Status: resp.GetStatus(),
		Body:   string(resp.GetBody()),
	})
}

func (c *client) appendErrorRecord(mode string, err error) error {
	path := filepath.Join(c.sandbox, resultsFile)
	file, openErr := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if openErr != nil {
		return openErr
	}
	defer file.Close()
	return json.NewEncoder(file).Encode(struct {
		Mode  string `json:"mode"`
		Error string `json:"error"`
	}{
		Mode:  mode,
		Error: err.Error(),
	})
}

func (c *client) reportErr(err error) {
	select {
	case c.errs <- err:
	default:
	}
}

func (c *client) writeFrame(msgType wkrpcproto.MsgType, payload []byte) error {
	if msgType == wkrpcproto.MsgTypeHeartbeat {
		payload = nil
	}
	frameData := make([]byte, len(wkrpcproto.MagicNumberStart)+1+4+len(payload))
	copy(frameData, wkrpcproto.MagicNumberStart)
	frameData[len(wkrpcproto.MagicNumberStart)] = msgType.Uint8()
	binary.BigEndian.PutUint32(frameData[len(wkrpcproto.MagicNumberStart)+1:], uint32(len(payload)))
	copy(frameData[len(wkrpcproto.MagicNumberStart)+1+4:], payload)
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	n, err := c.conn.Write(frameData)
	if err != nil {
		return err
	}
	if n != len(frameData) {
		return io.ErrShortWrite
	}
	return nil
}

func (c *client) readFrame() (wkrpcproto.MsgType, []byte, error) {
	header := make([]byte, len(wkrpcproto.MagicNumberStart)+1+4)
	if _, err := io.ReadFull(c.reader, header); err != nil {
		return 0, nil, err
	}
	if string(header[:len(wkrpcproto.MagicNumberStart)]) != string(wkrpcproto.MagicNumberStart) {
		return 0, nil, fmt.Errorf("invalid wkrpc magic: %q", header[:len(wkrpcproto.MagicNumberStart)])
	}
	msgType := wkrpcproto.MsgType(header[len(wkrpcproto.MagicNumberStart)])
	length := binary.BigEndian.Uint32(header[len(wkrpcproto.MagicNumberStart)+1:])
	if msgType == wkrpcproto.MsgTypeHeartbeat {
		if length > 0 {
			if _, err := io.CopyN(io.Discard, c.reader, int64(length)); err != nil {
				return 0, nil, err
			}
		}
		return msgType, nil, nil
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(c.reader, body); err != nil {
		return 0, nil, err
	}
	return msgType, body, nil
}
