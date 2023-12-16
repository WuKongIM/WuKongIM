package raftgroup

import (
	"bytes"
	"fmt"

	"go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

func LogRaftReady(ready raft.Ready) {
	if !verboseRaftLoggingEnabled() {
		return
	}

	var buf bytes.Buffer
	if ready.SoftState != nil {
		fmt.Fprintf(&buf, "  SoftState updated: %+v\n", *ready.SoftState)
	}
	if !raft.IsEmptyHardState(ready.HardState) {
		fmt.Fprintf(&buf, "  HardState updated: %+v\n", ready.HardState)
	}
	for i, e := range ready.Entries {
		fmt.Fprintf(&buf, "  New Entry[%d]: %.200s\n",
			i, raft.DescribeEntry(e, raftEntryFormatter))
	}
	for i, e := range ready.CommittedEntries {
		fmt.Fprintf(&buf, "  Committed Entry[%d]: %.200s\n",
			i, raft.DescribeEntry(e, raftEntryFormatter))
	}
	if !raft.IsEmptySnap(ready.Snapshot) {
		snap := ready.Snapshot
		snap.Data = nil
		fmt.Fprintf(&buf, "  Snapshot updated: %v\n", snap)
	}
	for i, m := range ready.Messages {
		fmt.Fprintf(&buf, "  Outgoing Message[%d]: %.2000s\n",
			i, raftDescribeMessage(m, raftEntryFormatter))
	}
	fmt.Printf("raft ready  (must-sync=%t)\n%s", ready.MustSync, buf.String())
}
func raftEntryFormatter(data []byte) string {

	return string(data)
}

// This is a fork of raft.DescribeMessage with a tweak to avoid logging
// snapshot data.
func raftDescribeMessage(m raftpb.Message, f raft.EntryFormatter) string {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "%x->%x %v Term:%d Log:%d/%d", m.From, m.To, m.Type, m.Term, m.LogTerm, m.Index)
	if m.Reject {
		fmt.Fprintf(&buf, " Rejected (Hint: %d)", m.RejectHint)
	}
	if m.Commit != 0 {
		fmt.Fprintf(&buf, " Commit:%d", m.Commit)
	}
	if len(m.Entries) > 0 {
		fmt.Fprintf(&buf, " Entries:[")
		for i, e := range m.Entries {
			if i != 0 {
				buf.WriteString(", ")
			}
			buf.WriteString(raft.DescribeEntry(e, f))
		}
		fmt.Fprintf(&buf, "]")
	}
	if len(m.Responses) > 0 {
		fmt.Fprintf(&buf, " Responses:[")
		for i, r := range m.Responses {
			if i != 0 {
				buf.WriteString(", ")
			}
			fmt.Fprintf(&buf, "%v", raftDescribeMessage(r, f))
		}
		fmt.Fprintf(&buf, "]")
	}
	if m.Snapshot != nil {
		snap := *m.Snapshot
		snap.Data = nil
		fmt.Fprintf(&buf, " Snapshot:%v", snap)
	}
	return buf.String()
}
func verboseRaftLoggingEnabled() bool {
	return true
}

const (
	MsgTypeRaftMessage = 1
)
