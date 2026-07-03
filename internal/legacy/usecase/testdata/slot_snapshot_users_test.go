package testdata

import (
	"context"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestGenerateSlotSnapshotUsersUpsertsDeterministicUsersAndDevices(t *testing.T) {
	store := &recordingSlotSnapshotUserStore{}
	app := New(AppOptions{SlotSnapshotUsers: store})

	result, err := app.GenerateSlotSnapshotUsers(context.Background(), GenerateSlotSnapshotUsersCommand{
		Prefix:       "snap-user",
		Count:        2,
		PayloadBytes: 32,
		Seed:         "seed-a",
		DeviceFlag:   1,
		DeviceLevel:  1,
	})

	require.NoError(t, err)
	require.Equal(t, GenerateSlotSnapshotUsersResult{
		Dataset:      "cluster/slot-snapshot-users",
		Prefix:       "snap-user",
		Count:        2,
		PayloadBytes: 32,
		FirstUID:     "snap-user-000000",
		LastUID:      "snap-user-000001",
	}, result)
	require.Equal(t, []metadb.User{
		{UID: "snap-user-000000"},
		{UID: "snap-user-000001"},
	}, store.users)
	require.Len(t, store.devices, 2)
	require.Equal(t, "snap-user-000000", store.devices[0].UID)
	require.Equal(t, int64(1), store.devices[0].DeviceFlag)
	require.Equal(t, int64(1), store.devices[0].DeviceLevel)
	require.Len(t, store.devices[0].Token, 32)
	require.Contains(t, store.devices[0].Token, "seed-a")
	require.Equal(t, "snap-user-000001", store.devices[1].UID)
	require.Len(t, store.devices[1].Token, 32)
	require.NotEqual(t, store.devices[0].Token, store.devices[1].Token)
}

func TestGenerateSlotSnapshotUsersRejectsInvalidInput(t *testing.T) {
	app := New(AppOptions{SlotSnapshotUsers: &recordingSlotSnapshotUserStore{}})

	for _, cmd := range []GenerateSlotSnapshotUsersCommand{
		{Prefix: "", Count: 1, PayloadBytes: 1},
		{Prefix: "snap", Count: 0, PayloadBytes: 1},
		{Prefix: "snap", Count: 1, PayloadBytes: 0},
	} {
		_, err := app.GenerateSlotSnapshotUsers(context.Background(), cmd)
		require.Error(t, err)
	}
}

func TestGenerateSlotSnapshotUsersAddsPerWriteDeadline(t *testing.T) {
	store := &deadlineCheckingSlotSnapshotUserStore{}
	app := New(AppOptions{SlotSnapshotUsers: store})

	_, err := app.GenerateSlotSnapshotUsers(context.Background(), GenerateSlotSnapshotUsersCommand{
		Prefix:       "snap-user",
		Count:        1,
		PayloadBytes: 8,
	})

	require.NoError(t, err)
	require.True(t, store.userDeadline, "user write should receive a deadline")
	require.True(t, store.deviceDeadline, "device write should receive a deadline")
}

type recordingSlotSnapshotUserStore struct {
	users   []metadb.User
	devices []metadb.Device
}

type deadlineCheckingSlotSnapshotUserStore struct {
	userDeadline   bool
	deviceDeadline bool
}

func (d *deadlineCheckingSlotSnapshotUserStore) UpsertUser(ctx context.Context, _ metadb.User) error {
	deadline, ok := ctx.Deadline()
	d.userDeadline = ok && time.Until(deadline) > 0
	return nil
}

func (d *deadlineCheckingSlotSnapshotUserStore) UpsertDevice(ctx context.Context, _ metadb.Device) error {
	deadline, ok := ctx.Deadline()
	d.deviceDeadline = ok && time.Until(deadline) > 0
	return nil
}

func (r *recordingSlotSnapshotUserStore) UpsertUser(_ context.Context, u metadb.User) error {
	r.users = append(r.users, u)
	return nil
}

func (r *recordingSlotSnapshotUserStore) UpsertDevice(_ context.Context, d metadb.Device) error {
	r.devices = append(r.devices, d)
	return nil
}
