package management

import (
	"context"
	"errors"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestSystemUsersListNormalizesAndSortsUIDs(t *testing.T) {
	op := &fakeSystemUserOperator{listed: []string{" sys-b ", "sys-a", "sys-a", ""}}
	app := New(Options{SystemUsers: op})

	got, err := app.ListSystemUsers(context.Background())
	if err != nil {
		t.Fatalf("ListSystemUsers() error = %v", err)
	}
	want := ListSystemUsersResponse{
		Items: []SystemUser{{UID: "sys-a"}, {UID: "sys-b"}},
		Total: 2,
	}
	if !sameSystemUsers(got.Items, want.Items) || got.Total != want.Total {
		t.Fatalf("ListSystemUsers() = %#v, want %#v", got, want)
	}
}

func TestSystemUsersAddAndRemoveNormalizeUIDs(t *testing.T) {
	op := &fakeSystemUserOperator{}
	app := New(Options{SystemUsers: op})

	added, err := app.AddSystemUsers(context.Background(), MutateSystemUsersRequest{UIDs: []string{" sys-a ", "sys-b", "sys-a", ""}})
	if err != nil {
		t.Fatalf("AddSystemUsers() error = %v", err)
	}
	if !sameStrings(op.addSink, []string{"sys-a", "sys-b"}) || !sameStrings(added.UIDs, []string{"sys-a", "sys-b"}) || !added.Changed {
		t.Fatalf("AddSystemUsers() = %#v addSink=%#v, want normalized changed", added, op.addSink)
	}

	removed, err := app.RemoveSystemUsers(context.Background(), MutateSystemUsersRequest{UIDs: []string{" sys-a ", "sys-a"}})
	if err != nil {
		t.Fatalf("RemoveSystemUsers() error = %v", err)
	}
	if !sameStrings(op.removeSink, []string{"sys-a"}) || !sameStrings(removed.UIDs, []string{"sys-a"}) || !removed.Changed {
		t.Fatalf("RemoveSystemUsers() = %#v removeSink=%#v, want normalized changed", removed, op.removeSink)
	}
}

func TestSystemUsersRejectEmptyMutationAndPropagateOperatorErrors(t *testing.T) {
	app := New(Options{SystemUsers: &fakeSystemUserOperator{}})
	if _, err := app.AddSystemUsers(context.Background(), MutateSystemUsersRequest{UIDs: []string{" ", ""}}); err != metadb.ErrInvalidArgument {
		t.Fatalf("AddSystemUsers(empty) error = %v, want %v", err, metadb.ErrInvalidArgument)
	}

	boom := errors.New("boom")
	app = New(Options{SystemUsers: &fakeSystemUserOperator{addErr: boom}})
	if _, err := app.AddSystemUsers(context.Background(), MutateSystemUsersRequest{UIDs: []string{"sys-a"}}); !errors.Is(err, boom) {
		t.Fatalf("AddSystemUsers() error = %v, want %v", err, boom)
	}
}

type fakeSystemUserOperator struct {
	listed     []string
	listErr    error
	addSink    []string
	addErr     error
	removeSink []string
	removeErr  error
}

func (f *fakeSystemUserOperator) ListSystemUIDs(context.Context) ([]string, error) {
	return append([]string(nil), f.listed...), f.listErr
}

func (f *fakeSystemUserOperator) AddSystemUIDs(_ context.Context, uids []string) error {
	f.addSink = append([]string(nil), uids...)
	return f.addErr
}

func (f *fakeSystemUserOperator) RemoveSystemUIDs(_ context.Context, uids []string) error {
	f.removeSink = append([]string(nil), uids...)
	return f.removeErr
}

func sameSystemUsers(left, right []SystemUser) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
