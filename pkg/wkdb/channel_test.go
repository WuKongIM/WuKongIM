package wkdb_test

import (
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

	channelId := "channel1"
	channelType := uint8(1)
	subscribers := []string{"test1", "test2"}

	err = d.AddSubscribers(channelId, channelType, subscribers)
	assert.NoError(t, err)
}

func TestGetSubscribers(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	channelId := "channel1"
	channelType := uint8(1)
	subscribers := []string{"test1", "test2"}

	err = d.AddSubscribers(channelId, channelType, subscribers)
	assert.NoError(t, err)

	subscribers2, err := d.GetSubscribers(channelId, channelType)
	assert.NoError(t, err)

	assert.ElementsMatch(t, subscribers, subscribers2)
}

func TestRemoveSubscribers(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	channelId := "channel1"
	channelType := uint8(1)
	subscribers := []string{"test1", "test2"}

	err = d.AddSubscribers(channelId, channelType, subscribers)
	assert.NoError(t, err)

	err = d.RemoveSubscribers(channelId, channelType, subscribers[1:])
	assert.NoError(t, err)

	subscribers2, err := d.GetSubscribers(channelId, channelType)
	assert.NoError(t, err)

	assert.Equal(t, 1, len(subscribers2))
	assert.Equal(t, subscribers[0], subscribers2[0])
}

func TestRemoveAllSubscriber(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	channelId := "channel1"
	channelType := uint8(1)
	subscribers := []string{"test1", "test2"}

	err = d.AddSubscribers(channelId, channelType, subscribers)
	assert.NoError(t, err)

	err = d.RemoveAllSubscriber(channelId, channelType)
	assert.NoError(t, err)

	subscribers2, err := d.GetSubscribers(channelId, channelType)
	assert.NoError(t, err)

	assert.Equal(t, 0, len(subscribers2))
}

func TestAddChannel(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()
	nw := time.Now()
	channelInfo := wkdb.ChannelInfo{
		ChannelId:   "channel1",
		ChannelType: 1,
		Ban:         true,
		Large:       true,
		Disband:     true,
		CreatedAt:   &nw,
		UpdatedAt:   &nw,
	}
	_, err = d.AddOrUpdateChannel(channelInfo)
	assert.NoError(t, err)

	channelInfo2, err := d.GetChannel(channelInfo.ChannelId, channelInfo.ChannelType)
	assert.NoError(t, err)

	assert.Equal(t, channelInfo.ChannelId, channelInfo2.ChannelId)
	assert.Equal(t, channelInfo.ChannelType, channelInfo2.ChannelType)
	assert.Equal(t, channelInfo.Ban, channelInfo2.Ban)
	assert.Equal(t, channelInfo.Large, channelInfo2.Large)
	assert.Equal(t, channelInfo.Disband, channelInfo2.Disband)
	assert.Equal(t, channelInfo.CreatedAt.Unix(), channelInfo2.CreatedAt.Unix())
	assert.Equal(t, channelInfo.UpdatedAt.Unix(), channelInfo2.UpdatedAt.Unix())
}

func TestUpdateChannel(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()
	nw := time.Now()
	channelInfo := wkdb.ChannelInfo{
		ChannelId:   "channel1",
		ChannelType: 1,
		Ban:         true,
		Large:       true,
		Disband:     true,
		CreatedAt:   &nw,
		UpdatedAt:   &nw,
	}
	_, err = d.AddOrUpdateChannel(channelInfo)
	assert.NoError(t, err)

	nw = nw.Add(time.Hour)
	channelInfo.Ban = false
	channelInfo.Large = false
	channelInfo.Disband = false
	channelInfo.UpdatedAt = &nw

	_, err = d.AddOrUpdateChannel(channelInfo)
	assert.NoError(t, err)

	channelInfo2, err := d.GetChannel(channelInfo.ChannelId, channelInfo.ChannelType)
	assert.NoError(t, err)

	assert.Equal(t, channelInfo.ChannelId, channelInfo2.ChannelId)
	assert.Equal(t, channelInfo.ChannelType, channelInfo2.ChannelType)
	assert.Equal(t, channelInfo.Ban, channelInfo2.Ban)
	assert.Equal(t, channelInfo.Large, channelInfo2.Large)
	assert.Equal(t, channelInfo.Disband, channelInfo2.Disband)
	assert.Equal(t, channelInfo.CreatedAt.Unix(), channelInfo2.CreatedAt.Unix())
	assert.Equal(t, channelInfo.UpdatedAt.Unix(), channelInfo2.UpdatedAt.Unix())
}

func TestExistChannel(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	channelInfo := wkdb.ChannelInfo{
		ChannelId:   "channel1",
		ChannelType: 1,
		Ban:         true,
		Large:       true,
		Disband:     true,
	}
	_, err = d.AddOrUpdateChannel(channelInfo)
	assert.NoError(t, err)

	exist, err := d.ExistChannel(channelInfo.ChannelId, channelInfo.ChannelType)
	assert.NoError(t, err)
	assert.True(t, exist)
}

func TestDeleteChannel(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	channelInfo := wkdb.ChannelInfo{
		ChannelId:   "channel1",
		ChannelType: 1,
		Ban:         true,
		Large:       true,
	}
	_, err = d.AddOrUpdateChannel(channelInfo)
	assert.NoError(t, err)

	err = d.DeleteChannel(channelInfo.ChannelId, channelInfo.ChannelType)
	assert.NoError(t, err)

	exist, err := d.ExistChannel(channelInfo.ChannelId, channelInfo.ChannelType)
	assert.NoError(t, err)
	assert.False(t, exist)
}

func TestAddDenylist(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	channelId := "channel1"
	channelType := uint8(1)
	uids := []string{"test1", "test2"}

	err = d.AddDenylist(channelId, channelType, uids)
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

	channelId := "channel1"
	channelType := uint8(1)
	uids := []string{"test1", "test2"}

	err = d.AddDenylist(channelId, channelType, uids)
	assert.NoError(t, err)

	uids2, err := d.GetDenylist(channelId, channelType)
	assert.NoError(t, err)

	assert.ElementsMatch(t, uids, uids2)
}

func TestRemoveDenylist(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	channelId := "channel1"
	channelType := uint8(1)
	uids := []string{"test1", "test2"}

	err = d.AddDenylist(channelId, channelType, uids)
	assert.NoError(t, err)

	err = d.RemoveDenylist(channelId, channelType, uids[1:])
	assert.NoError(t, err)

	uids2, err := d.GetDenylist(channelId, channelType)
	assert.NoError(t, err)

	assert.Equal(t, 1, len(uids2))
	assert.Equal(t, uids[0], uids2[0])
}

func TestRemoveAllDenylist(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	channelId := "channel1"
	channelType := uint8(1)
	uids := []string{"test1", "test2"}

	err = d.AddDenylist(channelId, channelType, uids)
	assert.NoError(t, err)

	err = d.RemoveAllDenylist(channelId, channelType)
	assert.NoError(t, err)

	uids2, err := d.GetDenylist(channelId, channelType)
	assert.NoError(t, err)

	assert.Equal(t, 0, len(uids2))
}

func TestAddAllowlist(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	channelId := "channel1"
	channelType := uint8(1)
	uids := []string{"test1", "test2"}

	err = d.AddAllowlist(channelId, channelType, uids)
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

	channelId := "channel1"
	channelType := uint8(1)
	uids := []string{"test1", "test2"}

	err = d.AddAllowlist(channelId, channelType, uids)
	assert.NoError(t, err)

	uids2, err := d.GetAllowlist(channelId, channelType)
	assert.NoError(t, err)

	assert.ElementsMatch(t, uids, uids2)
}

func TestRemoveAllowlist(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	channelId := "channel1"
	channelType := uint8(1)
	uids := []string{"test1", "test2"}

	err = d.AddAllowlist(channelId, channelType, uids)
	assert.NoError(t, err)

	err = d.RemoveAllowlist(channelId, channelType, uids[1:])
	assert.NoError(t, err)

	uids2, err := d.GetAllowlist(channelId, channelType)
	assert.NoError(t, err)

	assert.Equal(t, 1, len(uids2))
	assert.Equal(t, uids[0], uids2[0])
}

func TestRemoveAllAllowlist(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	channelId := "channel1"
	channelType := uint8(1)
	uids := []string{"test1", "test2"}

	err = d.AddAllowlist(channelId, channelType, uids)
	assert.NoError(t, err)

	err = d.RemoveAllAllowlist(channelId, channelType)
	assert.NoError(t, err)

	uids2, err := d.GetAllowlist(channelId, channelType)
	assert.NoError(t, err)

	assert.Equal(t, 0, len(uids2))
}
