package migration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestApprovedDuplicateChainKeepsTerminalAndOriginalBoundaries(t *testing.T) {
	ctx := context.Background()
	facts := []BusinessFacts{transformMessage(1, 10, "a", false), transformMessage(2, 10, "b", false), transformMessage(3, 20, "b", false), {Tail: &SourceMessageTail{Channel: ChannelIdentity{ID: "group", Type: 2}, LastSeq: 3}}}
	w, s := transformFixture(t, facts)
	// Decode the operator policy through the real JSON boundary so this test
	// reproduces the old semantic rejection before the field exists.
	require.NoError(t, json.Unmarshal([]byte(`{"resolve_duplicate_chains":true}`), s.Messages))
	tr, err := buildMessageTransform(ctx, s, w, transformFixtureDecoder{}, "message-transform/convert/")
	require.NoError(t, err)
	require.EqualValues(t, 1, tr.report.Retained)
	require.EqualValues(t, 2, tr.report.DuplicateDrops)
	for old, want := range map[uint64]uint64{1: 0, 2: 0, 3: 1} {
		got, e := tr.boundary(ctx, ChannelIdentity{ID: "group", Type: 2}, old)
		require.NoError(t, e)
		require.Equal(t, want, got)
	}
	var proof MessageSequenceMapping
	b, ok, err := tr.w.Get(ctx, []byte("chains/"+channelTuple(ChannelIdentity{ID: "group", Type: 2})+"/00000000000000000001"))
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, UnmarshalState(b, &proof))
	require.Len(t, proof.Winners, 1)
	require.EqualValues(t, 2, proof.Winners[0].Sequence, "direct provenance must not be overwritten")
	require.NotNil(t, proof.TerminalWinner)
	require.EqualValues(t, 3, proof.TerminalWinner.Sequence)
	require.EqualValues(t, 1, tr.report.ChainRoots)
	require.EqualValues(t, 1, tr.report.ChainTerminals)
	exported := 0
	require.NoError(t, WalkDuplicateChainMappings(ctx, w, tr.report, func(m MessageSequenceMapping) error { exported++; require.Equal(t, proof, m); return nil }))
	require.Equal(t, 1, exported)
	bad := *tr.report
	bad.ChainSHA256 = "changed"
	require.ErrorContains(t, WalkDuplicateChainMappings(ctx, w, &bad, func(MessageSequenceMapping) error { return nil }), "differs from prepared proof")
	independent, e := buildMessageTransform(ctx, s, w, transformFixtureDecoder{}, "independent-approved/")
	require.NoError(t, e)
	require.Equal(t, tr.report, independent.report)
}

func TestApprovedUnreadProjectionPreservesOriginalAndMappedPositions(t *testing.T) {
	ctx := context.Background()
	c := &SourceConversation{UID: "reader", Channel: ChannelIdentity{ID: "group", Type: 2}, ReadSeq: 2, DeletedToSeq: 1, UnreadCount: 17}
	facts := []BusinessFacts{transformMessage(1, 10, "a", false), transformMessage(2, 10, "a", false), transformMessage(3, 20, "b", false), {Tail: &SourceMessageTail{Channel: c.Channel, LastSeq: 3}}, {Conversation: c}}
	for _, f := range facts {
		if f.Message != nil {
			f.Message.Timestamp = 1700000000
			f.Message.ServerTimestampMS = 1700000000000
		}
	}
	w, s := transformFixture(t, facts)
	s.Metadata = &MetadataSelection{}
	require.NoError(t, json.Unmarshal([]byte(`{"derive_unread_from_boundaries":true}`), &s.Metadata.Policy))
	report, err := BuildTargetRecords(ctx, s, w, transformFixtureDecoder{})
	require.NoError(t, err)
	require.EqualValues(t, 2, report.Messages)
	require.NotNil(t, report.ArchivedUnread)
	require.EqualValues(t, 1, report.ArchivedUnread.Rows)
	require.EqualValues(t, 1, report.ArchivedUnread.NonzeroRows)
	require.Len(t, report.ArchivedUnread.SHA256, 64)
	var membership meta.UserChannelMembership
	require.NoError(t, WalkTargetMetadata(ctx, w, func(r TargetRecord) error {
		if r.Table == "membership" {
			return UnmarshalState(r.Value, &membership)
		}
		return nil
	}))
	require.EqualValues(t, 1, membership.ReadSeq)
	require.Zero(t, membership.DeletedToSeq)
	require.NoError(t, WalkSelectedSources(ctx, w, func(r SelectedRecord) error {
		f, e := (transformFixtureDecoder{}).DecodeBusiness(r.Row, r.Identity)
		if e != nil {
			return e
		}
		if f.Conversation != nil {
			require.EqualValues(t, 17, f.Conversation.UnreadCount)
			require.EqualValues(t, 2, f.Conversation.ReadSeq)
			require.EqualValues(t, 1, f.Conversation.DeletedToSeq)
		}
		return nil
	}))
}

func TestDuplicateChainRejectsDistinctSurvivingTerminals(t *testing.T) {
	facts := []BusinessFacts{transformMessage(1, 10, "a", false), transformMessage(2, 10, "b", false), transformMessage(3, 20, "b", false), transformMessage(4, 30, "a", false), {Tail: &SourceMessageTail{Channel: ChannelIdentity{ID: "group", Type: 2}, LastSeq: 4}}}
	w, s := transformFixture(t, facts)
	s.Messages.ResolveDuplicateChains = true
	_, err := buildMessageTransform(context.Background(), s, w, transformFixtureDecoder{}, "ambiguous/")
	require.ErrorContains(t, err, "multiple surviving terminals")
}

// Shared replacement suffixes must not be traversed again for every old row.
func TestDuplicateChainResolutionUsesLinearWorkspaceReads(t *testing.T) {
	const count = 160
	facts := make([]BusinessFacts, 0, count+1)
	for i := 1; i <= count; i++ {
		facts = append(facts, transformMessage(uint64(i), uint64((i+1)/2), fmt.Sprintf("client-%d", i/2), false))
	}
	facts = append(facts, BusinessFacts{Tail: &SourceMessageTail{Channel: ChannelIdentity{ID: "group", Type: 2}, LastSeq: count}})
	w, s := transformFixture(t, facts)
	s.Messages.ResolveDuplicateChains = true
	counted := &chainReadWorkspace{Workspace: w}
	tr, err := buildMessageTransform(context.Background(), s, counted, transformFixtureDecoder{}, "linear-chain/")
	require.NoError(t, err)
	require.EqualValues(t, 1, tr.report.Retained)
	require.LessOrEqual(t, counted.mappingReads, count*10, "shared suffix lookups must grow linearly")
	t.Logf("%d chain messages: %d mapping reads", count, counted.mappingReads)
}

type chainReadWorkspace struct {
	Workspace
	mappingReads int
}

func (w *chainReadWorkspace) Get(ctx context.Context, key []byte) ([]byte, bool, error) {
	if bytes.Contains(key, []byte("/mapping/")) {
		w.mappingReads++
	}
	return w.Workspace.Get(ctx, key)
}
