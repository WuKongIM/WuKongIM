package meta

import (
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

var errorType = reflect.TypeOf((*error)(nil)).Elem()

// The compatibility facade is used by Slot FSM and proxy code during shutdown.
// Every operation admitted after the underlying metadata store is gone must fail
// closed instead of panicking or partially staging a mutation.
func TestClosedCompatibilityHandlesFailTheCompleteSurface(t *testing.T) {
	t.Run("db", func(t *testing.T) {
		assertErrorSurface(t, (*DB)(nil), map[string]error{"Close": nil}, dberrors.ErrClosed)

		var db *DB
		if got := db.MetaDB(); got != nil {
			t.Fatalf("MetaDB() = %p, want nil", got)
		}
		if err := db.ForHashSlot(7).validate(); !errors.Is(err, dberrors.ErrClosed) {
			t.Fatalf("ForHashSlot().validate() error = %v, want ErrClosed", err)
		}
		if err := db.ForSlot(math.MaxUint16 + 1).validate(); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("ForSlot(out of range).validate() error = %v, want ErrInvalidArgument", err)
		}
		if shards := db.ForHashSlots([]uint16{1, 7}); len(shards) != 2 {
			t.Fatalf("ForHashSlots() length = %d, want 2", len(shards))
		}
		if err := db.NewWriteBatch().ensure(); !errors.Is(err, dberrors.ErrClosed) {
			t.Fatalf("NewWriteBatch().ensure() error = %v, want ErrClosed", err)
		}
	})

	t.Run("shard", func(t *testing.T) {
		assertErrorSurface(t, (*ShardStore)(nil), nil, dberrors.ErrClosed)
		if got := (*ShardStore)(nil).HashSlot(); got != 0 {
			t.Fatalf("HashSlot() = %d, want 0", got)
		}
	})

	t.Run("write batch", func(t *testing.T) {
		assertErrorSurface(t, (*WriteBatch)(nil), map[string]error{
			"Close":                              nil,
			"AbortChannelMigration":              ErrInvalidArgument,
			"AddChannelLearner":                  ErrInvalidArgument,
			"ClaimChannelMigrationTask":          ErrInvalidArgument,
			"ClearChannelWriteFence":             ErrInvalidArgument,
			"CommitChannelLeaderTransfer":        ErrInvalidArgument,
			"PromoteLearnerAndRemoveReplica":     ErrInvalidArgument,
			"ResetChannelWriteFenceToPreCutover": ErrInvalidArgument,
			"SetChannelWriteFence":               ErrInvalidArgument,
		}, dberrors.ErrClosed)
	})
}

func TestCompatibilityHelpersPreserveErrorAndCopySemantics(t *testing.T) {
	sentinel := errors.New("sentinel")
	if err := foundError(false, sentinel); !errors.Is(err, sentinel) {
		t.Fatalf("foundError(false, sentinel) = %v", err)
	}
	if err := foundError(false, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foundError(false, nil) = %v, want ErrNotFound", err)
	}
	if err := foundError(true, nil); err != nil {
		t.Fatalf("foundError(true, nil) = %v", err)
	}
	if got := optionalVersion(nil); got != 0 {
		t.Fatalf("optionalVersion(nil) = %d, want 0", got)
	}
	if got := optionalVersion([]uint64{7, 8}); got != 7 {
		t.Fatalf("optionalVersion([7 8]) = %d, want 7", got)
	}
	if err := normalizeCompatError(ErrStaleMeta); !errors.Is(err, ErrStaleMeta) {
		t.Fatalf("normalizeCompatError(ErrStaleMeta) = %v", err)
	}
	if err := normalizeCompatError(sentinel); !errors.Is(err, sentinel) {
		t.Fatalf("normalizeCompatError(sentinel) = %v", err)
	}

	input := []uint64{1, 2, 1, 3}
	got := replaceUint64(input, 1, 9)
	if !reflect.DeepEqual(got, []uint64{9, 2, 1, 3}) {
		t.Fatalf("replaceUint64() = %v", got)
	}
	got[0] = 4
	if input[0] != 1 {
		t.Fatal("replaceUint64 mutated its input")
	}
}

func assertErrorSurface(t *testing.T, receiver any, exceptions map[string]error, want error) {
	t.Helper()
	typeOfReceiver := reflect.TypeOf(receiver)
	valueOfReceiver := reflect.ValueOf(receiver)
	for index := 0; index < typeOfReceiver.NumMethod(); index++ {
		method := typeOfReceiver.Method(index)
		bound := valueOfReceiver.Method(index)
		methodType := bound.Type()
		if methodType.NumOut() == 0 || !methodType.Out(methodType.NumOut()-1).Implements(errorType) {
			continue
		}
		t.Run(method.Name, func(t *testing.T) {
			arguments := make([]reflect.Value, methodType.NumIn())
			for argument := range arguments {
				arguments[argument] = reflect.Zero(methodType.In(argument))
			}
			var results []reflect.Value
			if methodType.IsVariadic() {
				results = bound.CallSlice(arguments)
			} else {
				results = bound.Call(arguments)
			}
			last := results[len(results)-1]
			var got error
			if !last.IsNil() {
				got = last.Interface().(error)
			}
			if exception, ok := exceptions[method.Name]; ok {
				if !errors.Is(got, exception) {
					t.Fatalf("error = %v, want %v", got, exception)
				}
				return
			}
			if !errors.Is(got, want) {
				t.Fatalf("error = %v, want %v", got, want)
			}
		})
	}
}
