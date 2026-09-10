package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/stretchr/testify/require"
)

func conversationReplicaFixture(t *testing.T, states map[uint64]string) (Workspace, string) {
	t.Helper()
	w := dedupeTestWorkspace(t)
	key := IdentityKey("alice", "room", uint8(2))
	for node, state := range states {
		r := Row{Table: "Conversation", Kind: Primary, ID: node, Key: []byte(fmt.Sprint(node)), Fields: map[string][]byte{"original": []byte(state)}}
		raw, err := json.Marshal(r)
		require.NoError(t, err)
		c := sourceCandidate{NodeID: node, SourceKey: sourceRowKey(node, r), Table: r.Table, Kind: r.Kind, Identity: RecordIdentity{UID: "alice", Channel: ChannelIdentity{ID: "room", Type: 2}}, LogicalKey: key, Digest: diagnosticSHA([]byte(state)), Group: sourceGroup{Leader: 1, Replicas: []uint64{1, 2, 3}}}
		data, err := MarshalState(c)
		require.NoError(t, err)
		require.NoError(t, w.Put(context.Background(), []transfer.SpoolRow{{Key: c.SourceKey, Value: raw}, {Key: candidateKey("metadata", node, c.Table, key), Value: data}}))
	}
	return w, key
}

func TestConversationReplicaDifferenceBlocksStrictSelection(t *testing.T) {
	w, _ := conversationReplicaFixture(t, map[uint64]string{1: "read=3", 2: "read=3"})
	ctx := context.Background()
	require.ErrorContains(t, compareCandidates(ctx, w, "metadata", &captureBatch{ctx: ctx, workspace: w}), "source Conversation record is missing on configured replica node 3")
}

func replicaDecision(t *testing.T, w Workspace, key string, source uint64, archive bool) ConversationReplicaRecovery {
	t.Helper()
	row := sourceCandidate{LogicalKey: key, Group: sourceGroup{Leader: 1, Replicas: []uint64{1, 2, 3}}}
	digest, _, err := conversationReplicaCopies(context.Background(), w, row)
	require.NoError(t, err)
	return ConversationReplicaRecovery{LogicalKey: key, SourceNodeID: source, ArchiveOnly: archive, CopiesSHA256: digest}
}

func TestConversationReplicaDecisionPreservesExactChosenRecord(t *testing.T) {
	for _, tc := range []struct {
		name    string
		states  map[uint64]string
		source  uint64
		archive bool
	}{
		{"matching_follower", map[uint64]string{1: "read=3", 2: "read=3", 3: "read=0"}, 2, false},
		{"missing_leader", map[uint64]string{2: "read=3", 3: "read=3"}, 2, false},
		{"unique_nonleader_archive", map[uint64]string{3: "read=199"}, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			w, key := conversationReplicaFixture(t, tc.states)
			p := &MetadataPolicy{ConversationLookup: "v2_active_slot", ConversationReplicas: []ConversationReplicaRecovery{replicaDecision(t, w, key, tc.source, tc.archive)}}
			require.NoError(t, validateConversationReplicaPolicy(p))
			choices := newConversationReplicaChoices(ctx, w, p)
			b := &captureBatch{ctx: ctx, workspace: w}
			require.NoError(t, compareCandidatesWithChoices(ctx, w, "metadata", b, nil, choices))
			require.NoError(t, b.flush())
			r, err := choices.report()
			require.NoError(t, err)
			count := 0
			require.NoError(t, WalkSelectedSources(ctx, w, func(rec SelectedRecord) error {
				count++
				require.Equal(t, tc.source, rec.NodeID)
				require.Equal(t, tc.states[tc.source], string(rec.Row.Fields["original"]))
				return nil
			}))
			if tc.archive {
				require.Zero(t, count)
				require.EqualValues(t, 1, r.Archived)
			} else {
				require.Equal(t, 1, count)
				require.EqualValues(t, 1, r.Retained)
			}
		})
	}
}

func TestConversationReplicaDecisionRejectsChangedEvidenceAndAbsentChoice(t *testing.T) {
	for _, mode := range []string{"candidate", "original", "absent_choice", "unused", "healthy"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			states := map[uint64]string{1: "read=3", 2: "read=3"}
			if mode == "healthy" {
				states[3] = "read=3"
			}
			w, key := conversationReplicaFixture(t, states)
			decision := replicaDecision(t, w, key, 1, false)
			if mode == "absent_choice" {
				decision.SourceNodeID = 3
			}
			if mode == "unused" {
				decision.LogicalKey = "unrelated"
			}
			if mode == "candidate" || mode == "original" {
				k := candidateKey("metadata", 1, "Conversation", key)
				if mode == "original" {
					k = sourceRowKey(1, Row{Key: []byte("1")})
				}
				data, ok, err := w.Get(ctx, k)
				require.NoError(t, err)
				require.True(t, ok)
				w = prefixRowDriftWorkspace{Workspace: w, key: k, value: append(data, ' ')}
			}
			p := &MetadataPolicy{ConversationLookup: "v2_active_slot", ConversationReplicas: []ConversationReplicaRecovery{decision}}
			choices := newConversationReplicaChoices(ctx, w, p)
			err := compareCandidatesWithChoices(ctx, w, "metadata", &captureBatch{ctx: ctx, workspace: w}, nil, choices)
			if mode == "unused" {
				_, e := choices.report()
				require.ErrorContains(t, e, "not applied")
			} else {
				require.Error(t, err)
			}
			_, prepared, e := w.Get(ctx, []byte("workflow/PREPARED"))
			require.NoError(t, e)
			require.False(t, prepared)
		})
	}
}
