package manager

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	management "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

// The HTTP contract must preserve lifecycle and independent observation,
// including an explicit unknown fallback for an unavailable observer.
func TestChannelRuntimeHTTPSeparatesLifecycleAndActivity(t *testing.T) {
	for _, state := range []string{"active", "inactive", "unknown", ""} {
		t.Run("runtime_"+state, func(t *testing.T) {
			server := New(Options{Management: managerNodesStub{channelRuntimeMeta: management.ListChannelRuntimeMetaResponse{Items: []management.ChannelRuntimeMeta{{ChannelID: "room", ChannelType: 2, Leader: 3, Status: "active", RuntimeStatus: state, RuntimeNodeID: 3}}}}})
			recorder := httptest.NewRecorder()
			server.Engine().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/manager/channel-runtime-meta", nil))
			if recorder.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			var body struct {
				Items []map[string]any `json:"items"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			want := state
			if want == "" {
				want = "unknown"
			}
			if len(body.Items) != 1 || body.Items[0]["status"] != "active" || body.Items[0]["runtime_status"] != want || body.Items[0]["runtime_node_id"] != float64(3) {
				t.Fatalf("body=%s", recorder.Body.String())
			}
		})
	}
}
