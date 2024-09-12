package wkdb_test

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/stretchr/testify/assert"
)

func TestAddAllowlist(t *testing.T) {
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
	allowlist := []wkdb.Member{
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

	err = d.AddAllowlist(channelId, channelType, allowlist)
	assert.NoError(t, err)
}

func TestGetAllowlist(t *testing.T) {
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
	allowlist := []wkdb.Member{
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

	err = d.AddAllowlist(channelId, channelType, allowlist)
	assert.NoError(t, err)

	members, err := d.GetAllowlist(channelId, channelType)
	assert.NoError(t, err)
	assert.Equal(t, 3, len(members))
}

func TestRemoveAllowlist(t *testing.T) {
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
	allowlist := []wkdb.Member{
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

	err = d.AddAllowlist(channelId, channelType, allowlist)
	assert.NoError(t, err)

	err = d.RemoveAllowlist(channelId, channelType, []string{"uid1", "uid2"})
	assert.NoError(t, err)

	members, err := d.GetAllowlist(channelId, channelType)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(members))
}

func TestRemoveAllAllowlist(t *testing.T) {
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
	allowlist := []wkdb.Member{
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

	err = d.AddAllowlist(channelId, channelType, allowlist)
	assert.NoError(t, err)

	err = d.RemoveAllAllowlist(channelId, channelType)
	assert.NoError(t, err)

	members, err := d.GetAllowlist(channelId, channelType)
	assert.NoError(t, err)
	assert.Equal(t, 0, len(members))
}

func TestExistAllowlist(t *testing.T) {
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
	allowlist := []wkdb.Member{
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

	err = d.AddAllowlist(channelId, channelType, allowlist)
	assert.NoError(t, err)

	exist, err := d.ExistAllowlist(channelId, channelType, "uid1")
	assert.NoError(t, err)
	assert.True(t, exist)
}

func TestHasAllowlist(t *testing.T) {
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
	allowlist := []wkdb.Member{
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

	err = d.AddAllowlist(channelId, channelType, allowlist)
	assert.NoError(t, err)

	has, err := d.HasAllowlist(channelId, channelType)
	assert.NoError(t, err)
	assert.True(t, has)
}
