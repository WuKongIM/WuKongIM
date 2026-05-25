package management

import (
	"context"
	"errors"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

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

func TestSystemUsersListNormalizesAndSortsUIDs(t *testing.T) {
	op := &fakeSystemUserOperator{listed: []string{" sys-b ", "sys-a", "sys-a", ""}}
	app := New(Options{SystemUsers: op})

	got, err := app.ListSystemUsers(context.Background())

	require.NoError(t, err)
	require.Equal(t, ListSystemUsersResponse{
		Items: []SystemUser{{UID: "sys-a"}, {UID: "sys-b"}},
		Total: 2,
	}, got)
}

func TestSystemUsersAddNormalizesUIDs(t *testing.T) {
	op := &fakeSystemUserOperator{}
	app := New(Options{SystemUsers: op})

	got, err := app.AddSystemUsers(context.Background(), MutateSystemUsersRequest{UIDs: []string{" sys-a ", "sys-b", "sys-a", ""}})

	require.NoError(t, err)
	require.Equal(t, []string{"sys-a", "sys-b"}, op.addSink)
	require.Equal(t, MutateSystemUsersResponse{UIDs: []string{"sys-a", "sys-b"}, Changed: true}, got)
}

func TestSystemUsersRemoveNormalizesUIDs(t *testing.T) {
	op := &fakeSystemUserOperator{}
	app := New(Options{SystemUsers: op})

	got, err := app.RemoveSystemUsers(context.Background(), MutateSystemUsersRequest{UIDs: []string{" sys-a ", "sys-a"}})

	require.NoError(t, err)
	require.Equal(t, []string{"sys-a"}, op.removeSink)
	require.Equal(t, MutateSystemUsersResponse{UIDs: []string{"sys-a"}, Changed: true}, got)
}

func TestSystemUsersRejectEmptyMutation(t *testing.T) {
	app := New(Options{SystemUsers: &fakeSystemUserOperator{}})

	_, err := app.AddSystemUsers(context.Background(), MutateSystemUsersRequest{UIDs: []string{" ", ""}})

	require.ErrorIs(t, err, metadb.ErrInvalidArgument)
}

func TestSystemUsersRequiresOperator(t *testing.T) {
	app := New(Options{})

	_, err := app.ListSystemUsers(context.Background())

	require.ErrorIs(t, err, metadb.ErrInvalidArgument)
}

func TestSystemUsersPropagatesOperatorError(t *testing.T) {
	boom := errors.New("boom")
	app := New(Options{SystemUsers: &fakeSystemUserOperator{addErr: boom}})

	_, err := app.AddSystemUsers(context.Background(), MutateSystemUsersRequest{UIDs: []string{"sys-a"}})

	require.ErrorIs(t, err, boom)
}
