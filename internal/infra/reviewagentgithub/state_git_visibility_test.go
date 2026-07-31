package reviewagentgithub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestWaitForStateRefHeadRetriesExactPreviousParent(t *testing.T) {
	parent := strings.Repeat("1", 40)
	committed := strings.Repeat("2", 40)
	var reads atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.Path !=
			"/repos/WuKongIM/WuKongIM/git/ref/heads/review-state/pr-718" {
			t.Fatalf("path = %q", request.URL.Path)
		}
		head := parent
		if reads.Add(1) > 1 {
			head = committed
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"ref": "refs/heads/review-state/pr-718",
			"object": map[string]string{
				"type": "commit",
				"sha":  head,
			},
		})
	}))
	defer server.Close()

	client := newStateVisibilityTestClient(t, server)
	err := client.waitForStateRefHead(
		context.Background(),
		"review-state/pr-718",
		parent,
		committed,
		2,
		0,
	)
	if err != nil {
		t.Fatalf("waitForStateRefHead() error = %v", err)
	}
	if got := reads.Load(); got != 2 {
		t.Fatalf("ref reads = %d, want 2", got)
	}
}

func TestWaitForStateRefHeadRejectsThirdHeadWithoutRetry(t *testing.T) {
	parent := strings.Repeat("1", 40)
	committed := strings.Repeat("2", 40)
	contended := strings.Repeat("3", 40)
	var reads atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		reads.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"ref": "refs/heads/review-state/scheduler",
			"object": map[string]string{
				"type": "commit",
				"sha":  contended,
			},
		})
	}))
	defer server.Close()

	client := newStateVisibilityTestClient(t, server)
	err := client.waitForStateRefHead(
		context.Background(),
		"review-state/scheduler",
		parent,
		committed,
		3,
		0,
	)
	if err == nil || err.Error() != "Review state ref re-read is inconsistent" {
		t.Fatalf("waitForStateRefHead() error = %v", err)
	}
	if got := reads.Load(); got != 1 {
		t.Fatalf("ref reads = %d, want 1", got)
	}
}

func newStateVisibilityTestClient(
	t *testing.T,
	server *httptest.Server,
) *Client {
	t.Helper()
	client, err := NewClient(ClientConfig{
		BaseURL:      server.URL,
		GraphQLURL:   server.URL + "/graphql",
		Repository:   "WuKongIM/WuKongIM",
		Token:        "test-token",
		MaxPages:     1,
		MaxBodyBytes: 1 << 20,
	}, server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client
}
