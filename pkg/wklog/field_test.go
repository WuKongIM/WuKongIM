package wklog

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFieldConstructors(t *testing.T) {
	errBoom := errors.New("boom")
	duration := 5 * time.Second

	tests := []struct {
		name  string
		field Field
		key   string
		typ   FieldType
		value any
	}{
		{name: "string", field: String("module", "cluster"), key: "module", typ: StringType, value: "cluster"},
		{name: "int", field: Int("retries", 3), key: "retries", typ: IntType, value: 3},
		{name: "int64", field: Int64("offset", 9), key: "offset", typ: Int64Type, value: int64(9)},
		{name: "uint64", field: Uint64("nodeID", 7), key: "nodeID", typ: Uint64Type, value: uint64(7)},
		{name: "float64", field: Float64("ratio", 1.5), key: "ratio", typ: Float64Type, value: 1.5},
		{name: "bool", field: Bool("leader", true), key: "leader", typ: BoolType, value: true},
		{name: "error", field: Error(errBoom), key: "error", typ: ErrorType, value: errBoom},
		{name: "duration", field: Duration("latency", duration), key: "latency", typ: DurationType, value: duration},
		{name: "any", field: Any("payload", map[string]int{"count": 1}), key: "payload", typ: AnyType, value: map[string]int{"count": 1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.key, tt.field.Key)
			require.Equal(t, tt.typ, tt.field.Type)
			require.Equal(t, tt.value, tt.field.Value)
		})
	}
}

func TestSemanticHelpersUseStableKeys(t *testing.T) {
	duration := 2 * time.Second

	tests := []struct {
		name  string
		field Field
		want  Field
	}{
		{
			name:  "event",
			field: Event("message.send.persist.failed"),
			want:  Field{Key: "event", Type: StringType, Value: "message.send.persist.failed"},
		},
		{
			name:  "source module",
			field: SourceModule("message.send"),
			want:  Field{Key: "sourceModule", Type: StringType, Value: "message.send"},
		},
		{
			name:  "trace id",
			field: TraceID("gw-9f2c"),
			want:  Field{Key: "traceID", Type: StringType, Value: "gw-9f2c"},
		},
		{
			name:  "request id",
			field: RequestID("req-123"),
			want:  Field{Key: "requestID", Type: StringType, Value: "req-123"},
		},
		{
			name:  "node id",
			field: NodeID(3),
			want:  Field{Key: "nodeID", Type: Uint64Type, Value: uint64(3)},
		},
		{
			name:  "target node id",
			field: TargetNodeID(9),
			want:  Field{Key: "targetNodeID", Type: Uint64Type, Value: uint64(9)},
		},
		{
			name:  "slot id",
			field: SlotID(8),
			want:  Field{Key: "slotID", Type: Uint64Type, Value: uint64(8)},
		},
		{
			name:  "channel id",
			field: ChannelID("u1@u2"),
			want:  Field{Key: "channelID", Type: StringType, Value: "u1@u2"},
		},
		{
			name:  "channel type",
			field: ChannelType(1),
			want:  Field{Key: "channelType", Type: Int64Type, Value: int64(1)},
		},
		{
			name:  "message id",
			field: MessageID(88),
			want:  Field{Key: "messageID", Type: Int64Type, Value: int64(88)},
		},
		{
			name:  "uid",
			field: UID("u1"),
			want:  Field{Key: "uid", Type: StringType, Value: "u1"},
		},
		{
			name:  "session id",
			field: SessionID(11),
			want:  Field{Key: "sessionID", Type: Uint64Type, Value: uint64(11)},
		},
		{
			name:  "raft scope",
			field: RaftScope("slot"),
			want:  Field{Key: "raftScope", Type: StringType, Value: "slot"},
		},
		{
			name:  "raft event",
			field: RaftEvent("leader_change"),
			want:  Field{Key: "raftEvent", Type: StringType, Value: "leader_change"},
		},
		{
			name:  "conn id",
			field: ConnID(21),
			want:  Field{Key: "connID", Type: Uint64Type, Value: uint64(21)},
		},
		{
			name:  "attempt",
			field: Attempt(2),
			want:  Field{Key: "attempt", Type: IntType, Value: 2},
		},
		{
			name:  "reason",
			field: Reason("metadata stale"),
			want:  Field{Key: "reason", Type: StringType, Value: "metadata stale"},
		},
		{
			name:  "timeout",
			field: Timeout(duration),
			want:  Field{Key: "timeout", Type: DurationType, Value: duration},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.field)
		})
	}
}
