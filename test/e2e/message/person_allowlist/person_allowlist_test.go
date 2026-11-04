//go:build e2e

package person_allowlist

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestPersonalAllowlistPreparesMembershipAndActivatesOwner(t *testing.T) {
	for _, count := range []int{1, 3} {
		name := "single-node-cluster"
		if count == 3 {
			name = "three-node-cluster"
		}
		t.Run(name, func(t *testing.T) {
			dir := os.Getenv("WK_E2E_PERSON_ALLOWLIST_REPORT_DIR")
			if dir == "" {
				var err error
				dir, err = os.MkdirTemp("", "wukongim-person-allowlist-reports-")
				require.NoError(t, err)
			}
			evidence := make([]map[string]any, 0, 24)
			t.Cleanup(func() {
				body, err := json.MarshalIndent(map[string]any{
					"scenario": "personal_allowlist_membership_activation", "topology": name,
					"hash_slots": 256, "passed": !t.Failed(), "observations": evidence,
				}, "", "  ")
				require.NoError(t, err)
				require.NoError(t, os.MkdirAll(dir, 0o755))
				path := filepath.Join(dir, name+".json")
				require.NoError(t, os.WriteFile(path, append(body, '\n'), 0o644))
				t.Logf("personal allowlist evidence: %s", path)
			})
			opts := []suite.Option{suite.WithManagerHTTP()}
			for id := 1; id <= count; id++ {
				opts = append(opts, suite.WithNodeConfigOverrides(uint64(id), map[string]string{
					"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_GATEWAY_TOKEN_AUTH_ON": "false",
					"WK_MANAGER_AUTH_ON": "false", "WK_MESSAGE_PERSON_WHITELIST_ENABLED": "true",
				}))
			}
			cluster := suite.New(t).StartStaticCluster(count, opts...)
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
			defer cancel()
			require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())
			_, err := cluster.WaitSlotLeadersStable(ctx, time.Second)
			require.NoError(t, err, cluster.DumpDiagnostics())
			slots := cluster.ManagerClient(t, 1).MustSlots(t)
			leader := func(key string) uint64 {
				hash := routing.HashSlotForKey(key, 256)
				for _, slot := range slots {
					if slot.HashSlots != nil {
						for _, item := range slot.HashSlots.Items {
							if item == hash {
								return slot.Runtime.LeaderID
							}
						}
					}
				}
				t.Fatalf("no authoritative Slot for %q", key)
				return 0
			}
			owner, peer := "allowlist-owner", "allowlist-peer"
			if count == 3 {
				for index := 0; index < 10000; index++ {
					candidate := fmt.Sprintf("allowlist-owner-%d", index)
					if leader(candidate) == 2 {
						owner = candidate
						break
					}
				}
				for index := 0; index < 10000; index++ {
					candidate := fmt.Sprintf("allowlist-peer-%d", index)
					if leader(candidate) == 3 && leader(channelid.EncodePersonChannel(owner, candidate)) != 1 {
						peer = candidate
						break
					}
				}
				require.Equal(t, uint64(2), leader(owner))
				require.Equal(t, uint64(3), leader(peer))
				require.NotEqual(t, uint64(1), leader(channelid.EncodePersonChannel(owner, peer)))
			}
			pair := channelid.EncodePersonChannel(owner, peer)
			evidence = append(evidence, map[string]any{"step": "placement", "owner": owner, "peer": peer,
				"owner_leader": leader(owner), "peer_leader": leader(peer), "source_leader": leader(pair), "ingress": 1,
				"owner_hash_slot": routing.HashSlotForKey(owner, 256), "peer_hash_slot": routing.HashSlotForKey(peer, 256),
				"source_hash_slot": routing.HashSlotForKey(pair, 256), "slot_inventory": slots})
			post := func(ingress uint64, path string, request any) {
				body, postErr := suite.PostJSON(ctx, "http://"+cluster.MustNode(ingress).APIAddr()+path, request, nil)
				evidence = append(evidence, map[string]any{"step": path, "ingress": ingress, "request": request, "response": json.RawMessage(body)})
				require.NoError(t, postErr, cluster.DumpDiagnostics())
			}
			add := func(ingress uint64, uid string, uids ...string) {
				post(ingress, "/channel/whitelist_add", map[string]any{"channel_id": uid, "channel_type": 1, "uids": uids})
			}
			// Pagination exposes a nonempty continuation for a scanned Membership,
			// even when its empty conversation is filtered. Never decode the cursor.
			scan := func(uid string) int {
				cursor, candidates := "", 0
				for pageIndex := 0; pageIndex < 20; pageIndex++ {
					page, readErr := suite.PostConversationListPage(ctx, cluster.MustNode(1).APIAddr(), suite.ConversationListRequest{UID: uid, Cursor: cursor, Limit: 1})
					require.NoError(t, readErr)
					evidence = append(evidence, map[string]any{"step": "directory-page", "uid": uid, "request_cursor": cursor, "response": page})
					if page.NextCursor != "" {
						candidates++
					}
					if page.Done {
						return candidates
					}
					require.NotEmpty(t, page.NextCursor)
					require.NotEqual(t, cursor, page.NextCursor)
					cursor = page.NextCursor
				}
				t.Fatal("directory exceeded the fixed 20-page bound")
				return 0
			}
			find := func(uid string) suite.ConversationListItem {
				page, readErr := suite.PostConversationListPage(ctx, cluster.MustNode(1).APIAddr(), suite.ConversationListRequest{UID: uid, Limit: 20})
				require.NoError(t, readErr)
				evidence = append(evidence, map[string]any{"step": "conversation-view", "uid": uid, "response": page})
				for _, row := range page.Conversations {
					if row.ChannelID == map[string]string{owner: peer, peer: owner}[uid] && row.ChannelType == 1 {
						return row
					}
				}
				t.Fatalf("missing person conversation for %s in %s", uid, pair)
				return suite.ConversationListItem{}
			}
			assertFloors := func(before, after suite.ConversationListItem) {
				before.ActiveAt = after.ActiveAt
				require.Equal(t, before, after, "activation must preserve badge, visibility and message state")
			}
			require.Zero(t, scan(owner))
			require.Zero(t, scan(peer))
			const other = "allowlist-other"
			add(1, owner, peer, peer, other, owner)
			require.Equal(t, 3, scan(owner), "duplicates and self-chat must not duplicate Memberships")
			require.Equal(t, 1, scan(peer), "peer Membership must exist before any SEND")
			require.Equal(t, 1, scan(other), "every added peer needs its own Membership")
			// Ordinary Membership establishment must not grant reciprocal permission.
			reverse, err := suite.PostMessageSend(ctx, cluster.MustNode(1).APIAddr(), map[string]any{
				"from_uid": owner, "channel_id": peer, "channel_type": 1,
				"client_msg_no": "allowlist-one-way-denied", "payload": "eA==",
			})
			require.NoError(t, err)
			require.Equal(t, uint8(frame.ReasonNotInWhitelist), reverse.Reason)
			require.Zero(t, reverse.MessageSeq)
			evidence = append(evidence, map[string]any{"step": "membership-does-not-grant-reciprocal-permission", "response": reverse})
			send := func(from, to, key string) suite.MessageSendResponse {
				response, sendErr := suite.PostMessageSend(ctx, cluster.MustNode(1).APIAddr(), map[string]any{
					"from_uid": from, "channel_id": to, "channel_type": 1, "client_msg_no": key,
					"payload": base64.StdEncoding.EncodeToString([]byte(key)),
				})
				require.NoError(t, sendErr, cluster.DumpDiagnostics())
				require.Equal(t, uint8(frame.ReasonSuccess), response.Reason)
				return response
			}
			send(peer, owner, "allowlist-history-1")
			ownerRow, peerRow := find(owner), find(peer)
			require.Positive(t, ownerRow.ActiveAt)
			require.Zero(t, peerRow.ActiveAt, "adding to A's whitelist must not activate B")
			require.Zero(t, ownerRow.ReadSeq)
			require.Zero(t, ownerRow.DeletedToSeq)
			require.Equal(t, uint64(1), ownerRow.Unread)
			add(uint64(count), owner, peer)
			repeated := find(owner)
			require.Greater(t, repeated.ActiveAt, ownerRow.ActiveAt)
			assertFloors(ownerRow, repeated)
			require.Equal(t, peerRow, find(peer))
			add(uint64(count), peer, owner)
			require.Positive(t, find(peer).ActiveAt)
			require.Equal(t, repeated, find(owner))
			send(peer, owner, "allowlist-history-2")
			tail := send(owner, peer, "allowlist-history-3")
			post(1, "/conversations/clearUnread", map[string]any{"uid": peer, "channel_id": owner, "channel_type": 1})
			post(1, "/conversations/delete", map[string]any{"uid": owner, "channel_id": peer, "channel_type": 1})
			last := send(peer, owner, "allowlist-visible-4")
			ownerRow, peerRow = find(owner), find(peer)
			require.Equal(t, tail.MessageSeq, ownerRow.DeletedToSeq)
			add(uint64(count), owner, peer)
			repeated = find(owner)
			require.Greater(t, repeated.ActiveAt, ownerRow.ActiveAt)
			assertFloors(ownerRow, repeated)
			require.Equal(t, peerRow, find(peer))
			require.Equal(t, uint64(1), repeated.Unread)
			require.Equal(t, tail.MessageSeq, repeated.DeletedToSeq)
			require.NotNil(t, repeated.LastMessage)
			require.Equal(t, last.MessageSeq, repeated.LastMessage.MessageSeq)
			var history struct {
				Messages []struct {
					MessageSeq uint64 `json:"message_seq"`
				} `json:"messages"`
			}
			_, err = suite.PostJSON(ctx, "http://"+cluster.MustNode(1).APIAddr()+"/channel/messagesync", map[string]any{
				"login_uid": owner, "channel_id": peer, "channel_type": 1, "limit": 20,
			}, &history)
			require.NoError(t, err)
			require.Len(t, history.Messages, 1, "repeated friend add must not reset join or hidden-history boundaries")
			require.Equal(t, last.MessageSeq, history.Messages[0].MessageSeq)
			evidence = append(evidence, map[string]any{"step": "preserved-history-floor", "response": history})
			// ACL changes for groups/denylists/removal must not project ordinary Memberships.
			post(1, "/channel", map[string]any{"channel_id": "allowlist-group", "channel_type": 2, "subscribers": []string{other}})
			post(1, "/channel/whitelist_add", map[string]any{"channel_id": "allowlist-group", "channel_type": 2, "uids": []string{owner}})
			post(1, "/channel/blacklist_add", map[string]any{"channel_id": owner, "channel_type": 1, "uids": []string{"allowlist-blocked"}})
			post(1, "/channel/whitelist_remove", map[string]any{"channel_id": owner, "channel_type": 1, "uids": []string{peer}})
			require.Equal(t, 3, scan(owner))
			require.Equal(t, repeated, find(owner))
			require.Equal(t, peerRow, find(peer))
			rejected, err := suite.PostMessageSend(ctx, cluster.MustNode(1).APIAddr(), map[string]any{
				"from_uid": peer, "channel_id": owner, "channel_type": 1, "client_msg_no": "allowlist-removed-denied", "payload": "eA==",
			})
			require.NoError(t, err)
			require.Equal(t, uint8(frame.ReasonNotInWhitelist), rejected.Reason)
			evidence = append(evidence, map[string]any{"step": "removed-direction-denied", "response": rejected})
			add(1, owner, peer)
			repeated, peerRow = find(owner), find(peer)
			for index := range cluster.Nodes {
				require.NoError(t, cluster.Nodes[index].Stop())
			}
			for index := range cluster.Nodes {
				require.NoError(t, cluster.StartStoppedNode(cluster.Nodes[index].Spec.ID))
			}
			require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())
			_, err = cluster.WaitSlotLeadersStable(ctx, time.Second)
			require.NoError(t, err)
			slots = cluster.ManagerClient(t, 1).MustSlots(t)
			require.Equal(t, repeated, find(owner))
			require.Equal(t, peerRow, find(peer))
			evidence = append(evidence, map[string]any{"step": "joined-restart-preserves-memberships", "passed": true})
		})
	}
}
