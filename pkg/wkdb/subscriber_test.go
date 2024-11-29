package wkdb_test

import (
	"sort"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/stretchr/testify/assert"
)

func TestAddSubscribers(t *testing.T) {
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
	subscribers := []wkdb.Member{
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

	err = d.AddSubscribers(channelId, channelType, subscribers)
	assert.NoError(t, err)

	count, err := d.GetSubscriberCount(channelId, channelType)
	assert.NoError(t, err)

	assert.Equal(t, 3, count)

}

func TestGetSubscribers(t *testing.T) {
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
	subscribers := []wkdb.Member{
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

	err = d.AddSubscribers(channelId, channelType, subscribers)
	assert.NoError(t, err)

	subscribers2, err := d.GetSubscribers(channelId, channelType)
	assert.NoError(t, err)

	assert.Equal(t, len(subscribers), len(subscribers2))

	sort.Slice(subscribers, func(i, j int) bool {
		return subscribers[i].Uid < subscribers[j].Uid
	})
	sort.Slice(subscribers2, func(i, j int) bool {
		return subscribers2[i].Uid < subscribers2[j].Uid
	})
	for i := 0; i < len(subscribers); i++ {
		assert.Equal(t, subscribers[i].Uid, subscribers2[i].Uid)
		assert.Equal(t, subscribers[i].CreatedAt.Unix(), subscribers2[i].CreatedAt.Unix())
		assert.Equal(t, subscribers[i].UpdatedAt.Unix(), subscribers2[i].UpdatedAt.Unix())
	}
}

func TestRemoveSubscribers(t *testing.T) {
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
	subscribers := []wkdb.Member{
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

	err = d.AddSubscribers(channelId, channelType, subscribers)
	assert.NoError(t, err)

	subscribers2, err := d.GetSubscribers(channelId, channelType)
	assert.NoError(t, err)
	assert.Equal(t, 3, len(subscribers2))

	err = d.RemoveSubscribers(channelId, channelType, []string{"uid1", "uid2"})
	assert.NoError(t, err)

	subscribers2, err = d.GetSubscribers(channelId, channelType)
	assert.NoError(t, err)

	assert.Equal(t, 1, len(subscribers2))
	assert.Equal(t, "uid3", subscribers2[0].Uid)
}

func TestExistSubscriber(t *testing.T) {
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
	subscribers := []wkdb.Member{
		{
			Uid:       "uid1",
			CreatedAt: &createdAt,
			UpdatedAt: &updatedAt,
		},
	}

	exist, err := d.ExistSubscriber(channelId, channelType, "uid1")
	assert.NoError(t, err)
	assert.False(t, exist)

	err = d.AddSubscribers(channelId, channelType, subscribers)
	assert.NoError(t, err)

	exist, err = d.ExistSubscriber(channelId, channelType, "uid1")
	assert.NoError(t, err)
	assert.True(t, exist)

}

func TestRemoveAllSubscriber(t *testing.T) {
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
	subscribers := []wkdb.Member{
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

	err = d.AddSubscribers(channelId, channelType, subscribers)
	assert.NoError(t, err)

	subscribers2, err := d.GetSubscribers(channelId, channelType)
	assert.NoError(t, err)
	assert.Equal(t, 3, len(subscribers2))

	err = d.RemoveAllSubscriber(channelId, channelType)
	assert.NoError(t, err)

	subscribers2, err = d.GetSubscribers(channelId, channelType)
	assert.NoError(t, err)

	assert.Equal(t, 0, len(subscribers2))
}
