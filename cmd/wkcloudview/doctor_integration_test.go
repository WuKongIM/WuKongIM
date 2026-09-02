//go:build integration

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"

	"github.com/WuKongIM/WuKongIM/internal/access/cloudview"
)

func TestDoctorProvesCompletePublicObservationSurfaceOverHTTPIntegration(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get(cloudview.GateProbeHeader) != "gate-secret" {
			http.Error(writer, "missing gate token", http.StatusUnauthorized)
			return
		}
		switch request.URL.Path {
		case "/", "/demo/":
			writer.WriteHeader(http.StatusOK)
		case "/manager/login":
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"access_token": "token", "permissions": []map[string]any{{"resource": "*", "actions": []string{"*"}}},
			})
		case "/manager/nodes":
			if request.Header.Get("Authorization") != "Bearer token" {
				http.Error(writer, "unauthorized", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(writer).Encode([]map[string]any{{"id": 1}, {"id": 2}, {"id": 3}})
		case "/route":
			_ = json.NewEncoder(writer).Encode(map[string]string{"ws_addr": "ws" + strings.TrimPrefix("http://"+request.Host, "http")})
		case "/prometheus/api/v1/targets":
			targets := make([]map[string]string, 7)
			for index := range targets {
				targets[index] = map[string]string{"health": "up"}
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"status": "success", "data": map[string]any{"activeTargets": targets}})
		case "/ws":
			connection, err := upgrader.Upgrade(writer, request, nil)
			if err == nil {
				_ = connection.Close()
			}
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)

	result, err := runDoctor(t.Context(), doctorOptions{
		BaseURL: server.URL, Username: "admin", Password: "a1234567", ExpectedTargets: 7,
		WebSocketPath: "/ws", GateToken: "gate-secret",
	})
	if err != nil {
		t.Fatalf("runDoctor() error = %v", err)
	}
	if !result.Manager || !result.Demo || !result.RouteRewrite || !result.WebSocket || result.PrometheusTargetsUp != 7 {
		t.Fatalf("doctor result = %#v", result)
	}
}
