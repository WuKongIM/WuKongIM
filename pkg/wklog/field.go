package wklog

import "time"

type FieldType int

const (
	StringType FieldType = iota
	IntType
	Int64Type
	Uint64Type
	Float64Type
	BoolType
	ErrorType
	DurationType
	AnyType
)

type Field struct {
	Key   string
	Type  FieldType
	Value any
}

func String(key, val string) Field {
	return Field{Key: key, Type: StringType, Value: val}
}

func Int(key string, val int) Field {
	return Field{Key: key, Type: IntType, Value: val}
}

func Int64(key string, val int64) Field {
	return Field{Key: key, Type: Int64Type, Value: val}
}

func Uint64(key string, val uint64) Field {
	return Field{Key: key, Type: Uint64Type, Value: val}
}

func Float64(key string, val float64) Field {
	return Field{Key: key, Type: Float64Type, Value: val}
}

func Bool(key string, val bool) Field {
	return Field{Key: key, Type: BoolType, Value: val}
}

func Error(err error) Field {
	return Field{Key: "error", Type: ErrorType, Value: err}
}

func Duration(key string, val time.Duration) Field {
	return Field{Key: key, Type: DurationType, Value: val}
}

func Any(key string, val any) Field {
	return Field{Key: key, Type: AnyType, Value: val}
}

func Event(val string) Field {
	return String("event", val)
}

func SourceModule(val string) Field {
	return String("sourceModule", val)
}

func TraceID(val string) Field {
	return String("traceID", val)
}

func RequestID(val string) Field {
	return String("requestID", val)
}

func NodeID(val uint64) Field {
	return Uint64("nodeID", val)
}

func TargetNodeID(val uint64) Field {
	return Uint64("targetNodeID", val)
}

func LeaderNodeID(val uint64) Field {
	return Uint64("leaderNodeID", val)
}

func PeerNodeID(val uint64) Field {
	return Uint64("peerNodeID", val)
}

func SlotID(val uint64) Field {
	return Uint64("slotID", val)
}

func ChannelID(val string) Field {
	return String("channelID", val)
}

func ChannelType(val int64) Field {
	return Int64("channelType", val)
}

func MessageID(val int64) Field {
	return Int64("messageID", val)
}

func UID(val string) Field {
	return String("uid", val)
}

func SessionID(val uint64) Field {
	return Uint64("sessionID", val)
}

func RaftScope(val string) Field {
	return String("raftScope", val)
}

func RaftEvent(val string) Field {
	return String("raftEvent", val)
}

func ConnID(val uint64) Field {
	return Uint64("connID", val)
}

func Attempt(val int) Field {
	return Int("attempt", val)
}

func Timeout(val time.Duration) Field {
	return Duration("timeout", val)
}

func Reason(val string) Field {
	return String("reason", val)
}
