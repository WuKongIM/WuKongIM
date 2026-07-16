package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestPrepareChurnGroupSubscribersSwapsMappedIdentity(t *testing.T) {
	type request struct {
		path string
		body model.BatchSubscribersRequest
	}
	requests := make([]request, 0, 2)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body model.BatchSubscribersRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		requests = append(requests, request{path: r.URL.Path, body: body})
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(target.Close)

	assignment := Assignment{
		RunID:    "run-churn-group",
		WorkerID: "worker-a",
		Target: model.Target{
			BenchAPI: model.BenchAPIConfig{Addrs: []string{target.URL}},
		},
		Scenario: model.Scenario{
			Identity: model.IdentityConfig{TotalUsers: 4, UIDPrefix: "bench-u"},
			Online:   model.OnlineConfig{TotalUsers: 2},
			Channels: model.ChannelsConfig{Profiles: []model.ChannelProfile{{
				Name: "group-a", ChannelType: model.ChannelTypeGroup, Count: 1,
				Members: model.MembersConfig{Count: 2},
			}}},
		},
		Plan: model.WorkerPlan{
			WorkerID:              "worker-a",
			IdentityRange:         model.Range{Start: 0, End: 2},
			OnlineIdentityIndexes: []int{0, 3},
			Profiles: map[string]model.ProfileShard{"group-a": {
				Name: "group-a", ChannelType: model.ChannelTypeGroup,
				ChannelRange: model.Range{Start: 0, End: 1}, MemberRange: model.Range{Start: 0, End: 2},
			}},
		},
	}
	replacements := []churnReplacement{{oldUID: "bench-u-1", user: connectionUserForIdentityIndex(assignment.Scenario.Identity, 3)}}

	require.NoError(t, prepareChurnGroupSubscriberSwaps(context.Background(), assignment, 1, replacements))
	require.Len(t, requests, 2)
	require.Equal(t, "/bench/v1/channels/subscribers", requests[0].path)
	require.Equal(t, "/bench/v1/channels/subscribers/remove", requests[1].path)
	require.Equal(t, []model.SubscriberItem{{
		ChannelID: "run-churn-group-group-a-0", ChannelType: frame.ChannelTypeGroup, Subscribers: []string{"bench-u-3"},
	}}, requests[0].body.Items)
	require.Equal(t, []model.SubscriberItem{{
		ChannelID: "run-churn-group-group-a-0", ChannelType: frame.ChannelTypeGroup, Subscribers: []string{"bench-u-1"},
	}}, requests[1].body.Items)
	require.Contains(t, requests[0].body.BatchID, "churn-subs-add-worker-a-1")
	require.Contains(t, requests[1].body.BatchID, "churn-subs-remove-worker-a-1")
}
