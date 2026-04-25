package multiraft_test

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func TestNewValidatesRequiredOptions(t *testing.T) {
	_, err := multiraft.New(multiraft.Options{})
	if !errors.Is(err, multiraft.ErrInvalidOptions) {
		t.Fatalf("expected ErrInvalidOptions, got %v", err)
	}
}

func TestPublicTypesExposeApprovedFields(t *testing.T) {
	var opts multiraft.Options
	opts.NodeID = 1
	opts.TickInterval = time.Second
	opts.Workers = 1

	if opts.NodeID != 1 {
		t.Fatalf("unexpected NodeID: %d", opts.NodeID)
	}
}

func TestPublicAPIReflectsLearnerSupport(t *testing.T) {
	reqType := reflect.TypeOf(multiraft.BootstrapSlotRequest{})
	if _, ok := reqType.FieldByName("Learners"); ok {
		t.Fatal("BootstrapSlotRequest unexpectedly exposes Learners without end-to-end support")
	}

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed")
	}
	typesPath := filepath.Join(filepath.Dir(file), "types.go")

	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, typesPath, nil, 0)
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}

	ast.Inspect(parsed, func(node ast.Node) bool {
		valueSpec, ok := node.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for _, name := range valueSpec.Names {
			if name.Name == "RoleLearner" {
				t.Fatal("RoleLearner is exported but runtime does not surface learner status")
			}
		}
		return true
	})
}

func TestOpenSlotRegistersSlot(t *testing.T) {
	rt := newTestRuntime(t)
	err := rt.OpenSlot(context.Background(), multiraft.SlotOptions{
		ID:           10,
		Storage:      newFakeStorage(),
		StateMachine: newFakeStateMachine(),
	})
	if err != nil {
		t.Fatalf("OpenSlot() error = %v", err)
	}
	if got := rt.Slots(); !reflect.DeepEqual(got, []multiraft.SlotID{10}) {
		t.Fatalf("Slots() = %v", got)
	}
}

func TestOpenSlotRejectsDuplicateID(t *testing.T) {
	rt := newTestRuntime(t)
	if err := rt.OpenSlot(context.Background(), newSlotOptions(10)); err != nil {
		t.Fatalf("first OpenSlot() error = %v", err)
	}
	err := rt.OpenSlot(context.Background(), newSlotOptions(10))
	if !errors.Is(err, multiraft.ErrSlotExists) {
		t.Fatalf("expected ErrSlotExists, got %v", err)
	}
}

func TestBootstrapSlotCreatesInitialMembership(t *testing.T) {
	rt := newTestRuntime(t)
	err := rt.BootstrapSlot(context.Background(), multiraft.BootstrapSlotRequest{
		Slot:   newSlotOptions(20),
		Voters: []multiraft.NodeID{1, 2, 3},
	})
	if err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}
	st, err := rt.Status(20)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if st.SlotID != multiraft.SlotID(20) {
		t.Fatalf("Status().SlotID = %d", st.SlotID)
	}
}

func TestCloseSlotMakesFutureOperationsFail(t *testing.T) {
	rt := newTestRuntime(t)
	if err := rt.OpenSlot(context.Background(), newSlotOptions(10)); err != nil {
		t.Fatalf("OpenSlot() error = %v", err)
	}
	if err := rt.CloseSlot(context.Background(), 10); err != nil {
		t.Fatalf("CloseSlot() error = %v", err)
	}
	_, err := rt.Status(10)
	if !errors.Is(err, multiraft.ErrSlotNotFound) {
		t.Fatalf("expected ErrSlotNotFound, got %v", err)
	}
}
