package fsm

import (
	"context"
	"reflect"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func TestEncodeBindPluginUserCommandDecodes(t *testing.T) {
	binding := metadb.PluginUserBinding{UID: "u1", PluginNo: "bot-a", CreatedAtMS: 100, UpdatedAtMS: 101}
	decoded, err := decodeCommand(EncodeBindPluginUserCommand(binding))
	if err != nil {
		t.Fatalf("decodeCommand(bind plugin user): %v", err)
	}
	cmd, ok := decoded.(*bindPluginUserCmd)
	if !ok {
		t.Fatalf("decoded command type = %T, want *bindPluginUserCmd", decoded)
	}
	if !reflect.DeepEqual(cmd.binding, binding) {
		t.Fatalf("decoded binding = %#v, want %#v", cmd.binding, binding)
	}
}

func TestEncodeUnbindPluginUserCommandDecodes(t *testing.T) {
	decoded, err := decodeCommand(EncodeUnbindPluginUserCommand("u1", "bot-a"))
	if err != nil {
		t.Fatalf("decodeCommand(unbind plugin user): %v", err)
	}
	cmd, ok := decoded.(*unbindPluginUserCmd)
	if !ok {
		t.Fatalf("decoded command type = %T, want *unbindPluginUserCmd", decoded)
	}
	if cmd.uid != "u1" || cmd.pluginNo != "bot-a" {
		t.Fatalf("decoded unbind = %#v", cmd)
	}
}

func TestStateMachineApplyPluginBindingCommands(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{99})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots(): %v", err)
	}
	binding := metadb.PluginUserBinding{UID: "u1", PluginNo: "bot-a", CreatedAtMS: 100, UpdatedAtMS: 100}

	result, err := sm.Apply(ctx, multiraft.Command{SlotID: 11, HashSlot: 99, Index: 1, Term: 1, Data: EncodeBindPluginUserCommand(binding)})
	if err != nil {
		t.Fatalf("Apply(bind): %v", err)
	}
	if string(result) != ApplyResultOK {
		t.Fatalf("Apply(bind) result = %q, want %q", result, ApplyResultOK)
	}
	got, err := db.ForHashSlot(99).ListPluginBindingsByUID(ctx, "u1")
	if err != nil {
		t.Fatalf("ListPluginBindingsByUID(): %v", err)
	}
	if !reflect.DeepEqual(got, []metadb.PluginUserBinding{binding}) {
		t.Fatalf("bindings = %#v, want %#v", got, []metadb.PluginUserBinding{binding})
	}

	result, err = sm.Apply(ctx, multiraft.Command{SlotID: 11, HashSlot: 99, Index: 2, Term: 1, Data: EncodeUnbindPluginUserCommand("u1", "bot-a")})
	if err != nil {
		t.Fatalf("Apply(unbind): %v", err)
	}
	if string(result) != ApplyResultOK {
		t.Fatalf("Apply(unbind) result = %q, want %q", result, ApplyResultOK)
	}
	got, err = db.ForHashSlot(99).ListPluginBindingsByUID(ctx, "u1")
	if err != nil {
		t.Fatalf("ListPluginBindingsByUID(after unbind): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("bindings after unbind = %#v, want empty", got)
	}
}
