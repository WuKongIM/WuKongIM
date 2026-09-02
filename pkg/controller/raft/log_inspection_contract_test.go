package raft

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controller/command"
	"github.com/WuKongIM/WuKongIM/pkg/controller/state"
	etcdraft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

func TestLogInspectionDistinguishesNoopCorruptionAndCommands(t *testing.T) {
	issuedAt := time.Date(2026, 9, 2, 8, 9, 10, 123000000, time.FixedZone("operator", 8*60*60))
	encoded, err := command.Encode(command.Command{
		Kind:     command.KindUpsertNode,
		IssuedAt: issuedAt,
		Node:     &state.Node{NodeID: 2, Name: "node-2"},
	})
	if err != nil {
		t.Fatalf("encode command: %v", err)
	}
	emptyKind, err := command.Encode(command.Command{IssuedAt: issuedAt})
	if err != nil {
		t.Fatalf("encode empty-kind command: %v", err)
	}

	entries := []raftpb.Entry{
		{Index: 1, Type: raftpb.EntryConfChange, Data: []byte("membership")},
		{Index: 2, Type: raftpb.EntryNormal},
		{Index: 3, Type: raftpb.EntryNormal, Data: []byte("not-json")},
		{Index: 4, Type: raftpb.EntryNormal, Data: emptyKind},
		{Index: 5, Type: raftpb.EntryNormal, Data: encoded},
	}
	got := logEntriesFromRaft(entries)
	if len(got) != len(entries) {
		t.Fatalf("logEntriesFromRaft() length = %d, want %d", len(got), len(entries))
	}
	if got[0].Index != 5 || got[0].DecodeStatus != "ok" || got[0].DecodedType != string(command.KindUpsertNode) ||
		got[0].CreatedAtMS != issuedAt.UTC().UnixMilli() || got[0].Decoded["command"] != string(command.KindUpsertNode) {
		t.Fatalf("decoded command = %+v", got[0])
	}
	if got[1].Index != 4 || got[1].DecodeStatus != "corrupt" || got[1].DecodedType != "unknown" || got[1].Decoded["error"] == "" {
		t.Fatalf("empty-kind command = %+v", got[1])
	}
	if got[2].Index != 3 || got[2].DecodeStatus != "corrupt" || got[2].Decoded["error"] == "" {
		t.Fatalf("corrupt command = %+v", got[2])
	}
	if got[3].Index != 2 || got[3].DecodeStatus != "empty" || got[3].DecodedType != "noop" || got[3].Decoded["command"] != "noop" {
		t.Fatalf("noop entry = %+v", got[3])
	}
	if got[4].Index != 1 || got[4].Type != "conf_change" || got[4].DecodeStatus != "" || got[4].Decoded != nil {
		t.Fatalf("membership entry = %+v", got[4])
	}
}

func TestLogInspectionNormalizesOperatorInputsAndStableNames(t *testing.T) {
	if got := normalizeLogEntriesOptions(LogEntriesOptions{Cursor: 9}); got.Limit != defaultLogEntryLimit || got.Cursor != 9 {
		t.Fatalf("default options = %+v", got)
	}
	if got := normalizeLogEntriesOptions(LogEntriesOptions{Limit: -1}); got.Limit != defaultLogEntryLimit {
		t.Fatalf("negative limit = %+v", got)
	}
	if got := normalizeLogEntriesOptions(LogEntriesOptions{Limit: maxLogEntryLimit + 1}); got.Limit != maxLogEntryLimit {
		t.Fatalf("capped options = %+v", got)
	}
	if got := normalizeLogEntriesOptions(LogEntriesOptions{Limit: 7}); got.Limit != 7 {
		t.Fatalf("explicit options = %+v", got)
	}

	types := map[raftpb.EntryType]string{
		raftpb.EntryNormal:       "normal",
		raftpb.EntryConfChange:   "conf_change",
		raftpb.EntryConfChangeV2: "conf_change_v2",
	}
	for entryType, want := range types {
		if got := logEntryTypeName(entryType); got != want {
			t.Fatalf("logEntryTypeName(%v) = %q, want %q", entryType, got, want)
		}
	}
	unknown := raftpb.EntryType(99)
	if got := logEntryTypeName(unknown); got != unknown.String() {
		t.Fatalf("unknown log entry name = %q, want %q", got, unknown.String())
	}

	roles := map[etcdraft.StateType]string{
		etcdraft.StateLeader:       RoleLeader,
		etcdraft.StateFollower:     RoleFollower,
		etcdraft.StateCandidate:    RoleCandidate,
		etcdraft.StatePreCandidate: RoleCandidate,
	}
	for raftState, want := range roles {
		if got := raftRoleName(raftState); got != want {
			t.Fatalf("raftRoleName(%v) = %q, want %q", raftState, got, want)
		}
	}
	if got := raftRoleName(etcdraft.StateType(99)); got != RoleUnknown {
		t.Fatalf("unknown raft role = %q", got)
	}
}

type readyTransportCapture struct {
	calls    int
	messages []raftpb.Message
}

func (c *readyTransportCapture) Send(messages []raftpb.Message) {
	c.calls++
	c.messages = append(c.messages[:0], messages...)
}

func TestReadyHelpersPreserveMembershipAndTransportBoundaries(t *testing.T) {
	transport := &readyTransportCapture{}
	service := &Service{cfg: Config{Transport: transport}}
	service.sendReadyMessages(nil)
	if transport.calls != 0 {
		t.Fatalf("empty ready messages sent %d times", transport.calls)
	}
	want := []raftpb.Message{{From: 1, To: 2, Type: raftpb.MsgHeartbeat}}
	service.sendReadyMessages(want)
	if transport.calls != 1 || len(transport.messages) != 1 || transport.messages[0].To != 2 {
		t.Fatalf("transport capture = calls %d messages %+v", transport.calls, transport.messages)
	}

	entries := []raftpb.Entry{
		{Type: raftpb.EntryNormal},
		{Type: raftpb.EntryConfChange},
		{Type: raftpb.EntryConfChangeV2},
	}
	if got := countConfChanges(entries); got != 2 {
		t.Fatalf("countConfChanges() = %d, want 2", got)
	}
	if got, err := applyConfChange(nil, raftpb.Entry{Type: raftpb.EntryNormal}); err != nil || len(got.Voters) != 0 {
		t.Fatalf("normal applyConfChange() = (%+v, %v)", got, err)
	}
	for _, entryType := range []raftpb.EntryType{raftpb.EntryConfChange, raftpb.EntryConfChangeV2} {
		if _, err := applyConfChange(nil, raftpb.Entry{Type: entryType, Data: []byte{0xff}}); err == nil {
			t.Fatalf("applyConfChange(%v) accepted malformed protobuf", entryType)
		}
	}
}
