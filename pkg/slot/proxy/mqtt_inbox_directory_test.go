package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestMQTTInboxDirectoryReadCurrentUIDAuthority(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeHashSlotStores(t, 256)
	store := nodes[0].store
	var uid string
	for i := range 10000 {
		candidate := fmt.Sprintf("inbox-%d", i)
		if store.cluster.SlotForKey(candidate) == 2 && store.cluster.HashSlotForKey(candidate) != 2 {
			uid = candidate
			break
		}
	}
	require.NotEmpty(t, uid)
	hs := store.cluster.HashSlotForKey(uid)
	want := []meta.ChannelKey{{ChannelID: "z", ChannelType: 1}, {ChannelID: "aa", ChannelType: 1}}
	for _, key := range want {
		require.NoError(t, nodes[1].db.ForHashSlot(hs).UpsertUserChannelMembership(ctx, meta.UserChannelMembership{UID: uid, ChannelID: key.ChannelID, ChannelType: key.ChannelType}))
	}
	_, err := nodes[0].db.ForHashSlot(hs).GetUserChannelMembership(ctx, uid, "z", 1)
	require.ErrorIs(t, err, meta.ErrNotFound, "origin intentionally has no directory")
	q := meta.MQTTRead{Kind: meta.MQTTReadInboxDirectory, Owner: meta.MQTTBindingOwner{Kind: meta.MQTTBindingUID, ID: uid}, Limit: 1}
	for _, reader := range []*Store{store, nodes[1].store} {
		query := q
		for i, key := range want {
			before := nodes[1].cluster.nextIndex[2]
			r, err := reader.ReadMQTT(ctx, query)
			require.NoError(t, err)
			require.Equal(t, []meta.ChannelKey{key}, r.Directory)
			require.Equal(t, key, r.After.Directory)
			require.Equal(t, i == len(want)-1, r.Done)
			require.Equal(t, before+1, nodes[1].cluster.nextIndex[2], "fresh local apply barrier for every page")
			query.After = r.After
		}
	}
	nodes[1].store.cluster = &changingReadAuthority{proxyTestCluster: nodes[1].cluster}
	_, err = nodes[1].store.ReadMQTT(ctx, q)
	require.ErrorIs(t, err, ErrReadStaleRoute)
}

func TestMQTTInboxDirectoryReplyRejectsAmbiguousPages(t *testing.T) {
	q := meta.MQTTRead{Kind: meta.MQTTReadInboxDirectory, Owner: meta.MQTTBindingOwner{Kind: meta.MQTTBindingUID, ID: "alice"}, Limit: 2}
	q.After.Directory = meta.ChannelKey{ChannelID: "z", ChannelType: 1}
	req := mqttReadRPC{Format: 1, SlotID: 2, HashSlot: 17, Query: q}
	for _, mode := range []string{"valid", "duplicate", "regression", "input_cursor", "wrong_cursor", "wrong_type", "oversize", "empty_nonfinal", "short_nonfinal", "foreign_cursor", "foreign_result", "membership", "accounting"} {
		t.Run(mode, func(t *testing.T) {
			r := meta.MQTTReadResult{Done: false, Directory: []meta.ChannelKey{{ChannelID: "z", ChannelType: 2}, {ChannelID: "aa", ChannelType: 1}}}
			r.After.Directory = r.Directory[1]
			switch mode {
			case "duplicate":
				r.Directory[1] = r.Directory[0]
				r.After.Directory = r.Directory[1]
			case "regression":
				r.Directory[0], r.Directory[1] = r.Directory[1], r.Directory[0]
				r.After.Directory = r.Directory[1]
			case "input_cursor":
				r.Directory[0] = q.After.Directory
			case "wrong_cursor":
				r.After.Directory = r.Directory[0]
			case "wrong_type":
				r.Directory[0].ChannelType = 256
			case "oversize":
				r.Directory = append(r.Directory, meta.ChannelKey{ChannelID: "bb", ChannelType: 1})
				r.After.Directory = r.Directory[2]
			case "empty_nonfinal":
				r.Directory = nil
				r.After = q.After
			case "short_nonfinal":
				r.Directory = r.Directory[:1]
				r.After.Directory = r.Directory[0]
			case "foreign_cursor":
				r.After.Topic = "x"
			case "foreign_result":
				r.Bindings = []meta.MQTTSourceBinding{{}}
			case "membership":
				r.Membership = &meta.MQTTMembershipView{}
			case "accounting":
				r.Accounting = &meta.MQTTAccountingRange{}
			}
			raw, err := json.Marshal(mqttReadReply{Format: 1, SlotID: 2, HashSlot: 17, Query: q, Status: rpcStatusOK, Result: &r})
			require.NoError(t, err)
			_, err = decodeMQTTReadReply(raw, req)
			if mode == "valid" {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, meta.ErrCorruptValue)
			}
		})
	}
	require.NoError(t, validateMQTTReadShape(q, meta.MQTTReadResult{After: q.After, Done: true}))
	// An older read must never accept an unrelated new result field.
	require.ErrorIs(t, validateMQTTReadShape(meta.MQTTRead{Kind: meta.MQTTReadSession, Namespace: "main", ClientID: "c"}, meta.MQTTReadResult{Done: true, Directory: []meta.ChannelKey{{ChannelID: "z", ChannelType: 1}}}), meta.ErrCorruptValue)
}
