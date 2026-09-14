//go:build integration

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	accessapi "github.com/WuKongIM/WuKongIM/internal/access/api"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// Only the non-leader API node's worker runs. Completing the remote pending row
// therefore proves both the commit wake wiring and authoritative routed dispatch.
func TestMessageUpdateHintFromNonLeaderAPI(t *testing.T) {
	voters := make([]cluster.ControlVoter, 3)
	for i := range voters {
		voters[i] = cluster.ControlVoter{NodeID: uint64(i + 1), Addr: freeSendackSmokeTCPAddr(t)}
	}
	var apps []*App
	var nodes []*cluster.Node
	// Register last so all nodes stop before configuration-owned directories close.
	defer t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		done := make(chan error, len(apps))
		for _, a := range apps {
			go func() { done <- a.Stop(ctx) }()
		}
		for range apps {
			if err := <-done; err != nil {
				t.Error(err)
			}
		}
	})
	for _, voter := range voters {
		cfg := singleNodeClusterAppConfig(t)
		cfg.NodeID, cfg.Cluster.NodeID, cfg.Cluster.ListenAddr = voter.NodeID, voter.NodeID, voter.Addr
		cfg.Cluster.Control.Voters = voters
		cfg.Cluster.Slots.HashSlotCount, cfg.Cluster.Slots.InitialSlotCount = 256, 8
		cfg.Cluster.Slots.ReplicaCount, cfg.Cluster.Channel.ReplicaCount = 3, 3
		cfg.API.ListenAddr = "127.0.0.1:0"
		a, err := New(cfg, WithLogger(wklog.NewNop()))
		if err != nil {
			t.Fatal(err)
		}
		apps = append(apps, a)
		nodes = append(nodes, a.cluster.(*cluster.Node))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	done := make(chan error, len(apps))
	for _, a := range apps {
		go func() { done <- a.Start(ctx) }()
	}
	for range apps {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	waitAppClusterSnapshotsConverge(t, nodes)
	for i, n := range nodes {
		waitSingleNodeClusterNodeSchedulable(t, n, uint64(i+1))
	}
	channelID := channelid.EncodePersonChannel("hint-author", "hint-reader")
	send := postAppJSON(t, apps[0].api.(*accessapi.Server).Handler(), "/message/send",
		`{"from_uid":"hint-author","channel_id":"hint-reader","channel_type":1,"payload":"b2xk"}`, http.StatusOK)
	var sent struct {
		MessageID uint64 `json:"message_id"`
	}
	if err := json.Unmarshal(send, &sent); err != nil || sent.MessageID == 0 {
		t.Fatalf("send=%s err=%v", send, err)
	}
	route, err := nodes[0].RouteKey(channelID)
	if err != nil {
		t.Fatal(err)
	}
	var origin *App
	for _, a := range apps {
		if a.cfg.NodeID != route.Leader {
			origin = a
			break
		}
	}
	if origin == nil {
		t.Fatal("missing non-leader API node")
	}
	for _, a := range apps {
		if a.messageUpdateWorker == nil {
			t.Fatal("notification worker not wired")
		}
		if a != origin {
			if err := a.messageUpdateWorker.Stop(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	body := fmt.Sprintf(`{"login_uid":"hint-author","channel_id":"hint-reader","channel_type":1,"message_id":"%d","expected_content_epoch":"0","expected_version":"0","request_id":"remote-edit","payload":"bmV3"}`, sent.MessageID)
	postAppJSON(t, origin.api.(*accessapi.Server).Handler(), "/message/update", body, http.StatusOK)
	waitMessageUpdateHintCheckpoint(t, ctx, origin.cluster.(*cluster.Node), channelID, sent.MessageID)
	t.Logf("API node=%d, Slot leader=%d; remote notification checkpoint completed", origin.cfg.NodeID, route.Leader)
}
