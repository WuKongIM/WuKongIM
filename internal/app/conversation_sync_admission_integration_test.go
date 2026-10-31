//go:build integration

package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	accessapi "github.com/WuKongIM/WuKongIM/internal/access/api"
	"github.com/WuKongIM/WuKongIM/internal/bench/chatlifecycle"
	"github.com/WuKongIM/WuKongIM/internal/bench/target"
	"github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// This composition test reproduces the bootstrap failure chain through the
// real product admission, HTTP adapter, ordered target fallback and login gate.
// A held directory read is the only injected delay; no RPC/storage cause is inferred.
func TestLoginSyncReportsConversationAdmissionExhaustion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	var work sync.WaitGroup
	defer func() {
		unblock()
		cancel()
		work.Wait()
	}()
	admitted := make(chan int, 48)
	var rejected [3]atomic.Int64
	var addrs []string
	for node := 0; node < 3; node++ {
		service := conversation.New(conversation.Options{
			Directory: heldLoginSyncDirectory{node: node, admitted: admitted, release: release},
			Hydrator:  unusedLoginSyncHydrator{},
		})
		handler := accessapi.New(accessapi.Options{Conversations: service}).Handler()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			counter := &loginSyncStatusWriter{ResponseWriter: w}
			handler.ServeHTTP(counter, r)
			if counter.status == http.StatusServiceUnavailable {
				rejected[node].Add(1)
			}
		}))
		// Cancel/unblock before httptest waits for any admitted handler.
		t.Cleanup(server.Close)
		addrs = append(addrs, server.URL)
	}
	client := target.NewClient(target.Config{APIAddrs: addrs, HTTPClient: &http.Client{Timeout: 3 * time.Second}})
	results := make(chan error, 48)
	for i := 0; i < 48; i++ {
		work.Add(1)
		go func() {
			defer work.Done()
			result, err := chatlifecycle.RunLoginSync(ctx, "admission-probe", immediateLoginConnector{}, client, time.Now)
			if err == nil && !result.TrafficReady {
				err = fmt.Errorf("successful full sync did not grant readiness")
			}
			results <- err
		}()
	}
	var counts [3]int
	for i := 0; i < 48; i++ {
		select {
		case node := <-admitted:
			counts[node]++
		case <-ctx.Done():
			t.Fatalf("did not fill actual API admission: %v counts=%v", ctx.Err(), counts)
		}
	}
	if counts != [3]int{16, 16, 16} {
		t.Fatalf("actual admitted requests=%v, want16 per product node", counts)
	}
	result, err := chatlifecycle.RunLoginSync(ctx, "overflow-probe", immediateLoginConnector{}, client, time.Now)
	failure, ok := chatlifecycle.LoginSyncFailureOf(err)
	if !ok || failure.Stage != chatlifecycle.LoginSyncStageSync || failure.Reason != chatlifecycle.LoginSyncReasonTransport || result.TrafficReady || !result.ConnectCompleted {
		t.Fatalf("overflow result=%+v failure=%+v recognized=%v error=%v", result, failure, ok, err)
	}
	unblock()
	for i := 0; i < 48; i++ {
		if err := <-results; err != nil {
			t.Fatalf("admitted full sync failed: %v", err)
		}
	}
	got := [3]int64{rejected[0].Load(), rejected[1].Load(), rejected[2].Load()}
	if got != [3]int64{33, 17, 1} {
		t.Fatalf("ordered fallback503 counts=%v, want33/17/1", got)
	}
	t.Logf("actual product admission16/node; held48; overflow sync rejected; ordered503=%v; admitted48 joined", got)
}

type heldLoginSyncDirectory struct {
	node     int
	admitted chan<- int
	release  <-chan struct{}
}

func (d heldLoginSyncDirectory) ListUserChannelMembershipPage(ctx context.Context, _ string, _ metadb.UserChannelMembershipCursor, _ int) ([]metadb.UserChannelMembership, metadb.UserChannelMembershipCursor, bool, error) {
	d.admitted <- d.node
	select {
	case <-d.release:
		return nil, metadb.UserChannelMembershipCursor{}, true, nil
	case <-ctx.Done():
		return nil, metadb.UserChannelMembershipCursor{}, false, ctx.Err()
	}
}

type unusedLoginSyncHydrator struct{}

func (unusedLoginSyncHydrator) HydratePersistedConversationHeads(context.Context, string, []metadb.UserChannelMembership) ([]conversation.HydrationResult, error) {
	return nil, fmt.Errorf("unexpected non-empty directory hydration")
}
func (unusedLoginSyncHydrator) HydrateConversationHeads(context.Context, string, []metadb.UserChannelMembership, ...uint64) ([]conversation.HydrationResult, error) {
	return nil, fmt.Errorf("unexpected mutation hydration")
}

type immediateLoginConnector struct{}

func (immediateLoginConnector) Connect(context.Context, string) error { return nil }

type loginSyncStatusWriter struct {
	http.ResponseWriter
	status int
}

func (w *loginSyncStatusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
