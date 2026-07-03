package channel

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestMetaCarriesRuntimeAndBusinessFields(t *testing.T) {
	until := time.UnixMilli(1_700_000_000_000).UTC()
	meta := Meta{
		Key:        ChannelKey("channel/1/dTE="),
		ID:         ChannelID{ID: "u1", Type: 1},
		Leader:     100,
		Status:     StatusActive,
		Features:   Features{MessageSeqFormat: MessageSeqFormatU64},
		WriteFence: WriteFence{Token: "task-1", Version: 3, Reason: WriteFenceReasonMigration, Until: until},
	}
	require.Equal(t, ChannelID{ID: "u1", Type: 1}, meta.ID)
	require.Equal(t, StatusActive, meta.Status)
	require.True(t, meta.WriteFence.Active(until.Add(-time.Millisecond)))
	require.True(t, meta.WriteFence.Active(until))
	require.False(t, meta.WriteFence.Active(until.Add(time.Nanosecond)))
	require.True(t, meta.WriteFence.BlocksAppend())
}

func TestAppendRequestCarriesBusinessChannelID(t *testing.T) {
	req := AppendRequest{
		ChannelID: ChannelID{ID: "room-1", Type: 2},
		Message:   Message{Payload: []byte("hi")},
	}
	require.Equal(t, "room-1", req.ChannelID.ID)
	require.Equal(t, uint8(2), req.ChannelID.Type)
}

func TestCommitModeFromContextDoesNotAllocateForDefaultContext(t *testing.T) {
	allocs := testing.AllocsPerRun(1000, func() {
		if got := CommitModeFromContext(context.Background()); got != CommitModeQuorum {
			t.Fatalf("CommitModeFromContext() = %v, want %v", got, CommitModeQuorum)
		}
	})

	require.Zero(t, allocs)
}

func TestMessageCarriesDurableBusinessFields(t *testing.T) {
	t.Helper()

	msgType := reflect.TypeOf(Message{})
	tests := map[string]reflect.Type{
		"Framer":    reflect.TypeOf(frame.Framer{}),
		"Setting":   reflect.TypeOf(frame.Setting(0)),
		"MsgKey":    reflect.TypeOf(""),
		"Expire":    reflect.TypeOf(uint32(0)),
		"ClientSeq": reflect.TypeOf(uint64(0)),
		"StreamNo":  reflect.TypeOf(""),
		"Timestamp": reflect.TypeOf(int32(0)),
		"Topic":     reflect.TypeOf(""),
	}

	for name, wantType := range tests {
		field, ok := msgType.FieldByName(name)
		require.Truef(t, ok, "Message is missing %s", name)
		require.Equalf(t, wantType, field.Type, "Message.%s type", name)
	}
}
