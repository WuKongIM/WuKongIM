//go:build integration

package gateway

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	applog "github.com/WuKongIM/WuKongIM/internal/log"
	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/binding"
	"github.com/gorilla/websocket"
)

func TestWebSocketPathRejectionLogIncludesDiagnostics(t *testing.T) {
	for _, format := range []string{"console", "json"} {
		t.Run(format, func(t *testing.T) {
			dir := t.TempDir()
			logger, err := applog.NewLogger(applog.Config{Dir: dir, Level: "info", Format: format})
			if err != nil {
				t.Fatal(err)
			}
			gw, err := coregateway.New(coregateway.Options{
				Handler:   New(Options{Logger: logger.Named("access.gateway")}),
				Logger:    logger,
				Listeners: []coregateway.ListenerOptions{binding.WSMux("ws-gateway", "127.0.0.1:0")},
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = gw.Stop() })
			if err := gw.Start(); err != nil {
				t.Fatal(err)
			}
			addr := gw.ListenerAddr("ws-gateway")
			conn, err := net.DialTimeout("tcp", addr, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
				t.Fatal(err)
			}
			peer := conn.LocalAddr().String()
			_, err = fmt.Fprintf(conn, "GET /ws?token=private-query HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n", addr)
			if err != nil {
				t.Fatal(err)
			}
			reader := bufio.NewReader(conn)
			resp, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != http.StatusNotFound || string(body) != `websocket path "/" not found` {
				t.Fatalf("rejection response changed: status=%d body=%q", resp.StatusCode, body)
			}
			if _, err := reader.ReadByte(); err != io.EOF {
				t.Fatalf("rejected connection read = %v, want EOF", err)
			}
			if err := logger.Sync(); err != nil {
				t.Fatal(err)
			}
			logs, err := applog.NewAppLogReader(applog.AppLogReaderOptions{Dir: dir}).Entries(
				context.Background(), applog.AppLogEntriesRequest{Limit: 10, Levels: []string{"INFO", "ERROR"}},
			)
			if err != nil {
				t.Fatal(err)
			}
			if len(logs.Items) != 1 {
				t.Fatalf("error log count = %d, want 1", len(logs.Items))
			}
			entry := logs.Items[0]
			if entry.Level != "INFO" {
				t.Fatalf("handshake rejection level = %s, want INFO", entry.Level)
			}
			if strings.Contains(entry.Raw, "github.com/WuKongIM") {
				t.Fatal("rejection includes stack trace")
			}
			if entry.Fields["listener"] != "ws-gateway" {
				t.Fatalf("listener field = %v, want ws-gateway", entry.Fields["listener"])
			}
			detail, ok := entry.Fields["error"].(string)
			if !ok {
				t.Fatalf("error field = %#v, want diagnostic text", entry.Fields["error"])
			}
			for _, want := range []string{
				"websocket handshake rejected", "conn_id=", "http_status=404",
				"remote_addr=" + strconv.Quote(peer), "local_addr=" + strconv.Quote(addr),
				`requested_path="/ws"`, `expected_path="/"`,
			} {
				if !strings.Contains(detail, want) {
					t.Errorf("error %q missing %q", detail, want)
				}
			}
			if strings.Contains(entry.Raw, "private-query") || strings.Contains(detail, "token=") {
				t.Fatal("query credentials appeared in rejection log")
			}

			// A rejected request must not stop the listener from upgrading a valid one.
			dialer := websocket.Dialer{HandshakeTimeout: 3 * time.Second}
			valid, _, err := dialer.Dial("ws://"+addr+"/", nil)
			if err != nil {
				t.Fatalf("valid root-path upgrade after rejection: %v", err)
			}
			_ = valid.Close()
		})
	}
}
