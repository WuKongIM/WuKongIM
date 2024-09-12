package wkdb_test

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/stretchr/testify/assert"
)

func TestAddDenylist(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	createdAt := time.Now()
	updatedAt := time.Now()

	channelId := "channel1"
	channelType := uint8(2)
	denylist := []wkdb.Member{
		{
			Uid:       "uid1",
			CreatedAt: &createdAt,
			UpdatedAt: &updatedAt,
		},
		{
			Uid:       "uid2",
			CreatedAt: &createdAt,
			UpdatedAt: &updatedAt,
		},
		{
			Uid:       "uid3",
			CreatedAt: &createdAt,
			UpdatedAt: &updatedAt,
		},
	}

	err = d.AddDenylist(channelId, channelType, denylist)
	assert.NoError(t, err)
}

func TestGetDenylist(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	createdAt := time.Now()
	updatedAt := time.Now()

	channelId := "channel1"
	channelType := uint8(2)
	denylist := []wkdb.Member{
		{
			Uid:       "uid1",
			CreatedAt: &createdAt,
			UpdatedAt: &updatedAt,
		},
		{
			Uid:       "uid2",
			CreatedAt: &createdAt,
			UpdatedAt: &updatedAt,
		},
		{
			Uid:       "uid3",
			CreatedAt: &createdAt,
			UpdatedAt: &updatedAt,
		},
	}

	err = d.AddDenylist(channelId, channelType, denylist)
	assert.NoError(t, err)

	members, err := d.GetDenylist(channelId, channelType)
	assert.NoError(t, err)
	assert.Equal(t, len(denylist), len(members))
}

func TestRemoveDenylist(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	createdAt := time.Now()
	updatedAt := time.Now()

	channelId := "channel1"
	channelType := uint8(2)
	denylist := []wkdb.Member{
		{
			Uid:       "uid1",
			CreatedAt: &createdAt,
			UpdatedAt: &updatedAt,
		},
		{
			Uid:       "uid2",
			CreatedAt: &createdAt,
			UpdatedAt: &updatedAt,
		},
		{
			Uid:       "uid3",
			CreatedAt: &createdAt,
			UpdatedAt: &updatedAt,
		},
	}

	err = d.AddDenylist(channelId, channelType, denylist)
	assert.NoError(t, err)

	members, err := d.GetDenylist(channelId, channelType)
	assert.NoError(t, err)
	assert.Equal(t, len(denylist), len(members))

	err = d.RemoveDenylist(channelId, channelType, []string{"uid1", "uid2"})
	assert.NoError(t, err)

	members, err = d.GetDenylist(channelId, channelType)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(members))
}

func TestRemoveAllDenylist(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	createdAt := time.Now()
	updatedAt := time.Now()

	channelId := "channel1"
	channelType := uint8(2)
	denylist := []wkdb.Member{
		{
			Uid:       "uid1",
			CreatedAt: &createdAt,
			UpdatedAt: &updatedAt,
		},
		{
			Uid:       "uid2",
			CreatedAt: &createdAt,
			UpdatedAt: &updatedAt,
		},
		{
			Uid:       "uid3",
			CreatedAt: &createdAt,
			UpdatedAt: &updatedAt,
		},
	}

	err = d.AddDenylist(channelId, channelType, denylist)
	assert.NoError(t, err)

	members, err := d.GetDenylist(channelId, channelType)
	assert.NoError(t, err)
	assert.Equal(t, len(denylist), len(members))

	err = d.RemoveAllDenylist(channelId, channelType)
	assert.NoError(t, err)

	members, err = d.GetDenylist(channelId, channelType)
	assert.NoError(t, err)
	assert.Equal(t, 0, len(members))
}

func TestExistDenylist(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	createdAt := time.Now()
	updatedAt := time.Now()

	channelId := "channel1"
	channelType := uint8(2)
	denylist := []wkdb.Member{
		{
			Uid:       "uid1",
			CreatedAt: &createdAt,
			UpdatedAt: &updatedAt,
		},
		{
			Uid:       "uid2",
			CreatedAt: &createdAt,
			UpdatedAt: &updatedAt,
		},
		{
			Uid:       "uid3",
			CreatedAt: &createdAt,
			UpdatedAt: &updatedAt,
		},
	}

	err = d.AddDenylist(channelId, channelType, denylist)
	assert.NoError(t, err)

	exist, err := d.ExistDenylist(channelId, channelType, "uid1")
	assert.NoError(t, err)
	assert.True(t, exist)

	exist, err = d.ExistDenylist(channelId, channelType, "uid4")
	assert.NoError(t, err)
	assert.False(t, exist)
}
