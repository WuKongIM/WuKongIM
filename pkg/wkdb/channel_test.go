package wkdb_test

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/stretchr/testify/assert"
)

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
	_, err = d.AddChannel(channelInfo)
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
	createdAt := time.Now()
	updatedAt := time.Now()
	channelInfo := wkdb.ChannelInfo{
		ChannelId:   "channel1",
		ChannelType: 1,
		Ban:         true,
		Large:       true,
		Disband:     true,
		CreatedAt:   &createdAt,
		UpdatedAt:   &updatedAt,
	}
	_, err = d.AddChannel(channelInfo)
	assert.NoError(t, err)

	nw := time.Now()
	nw = nw.Add(time.Hour)
	channelInfo.Ban = false
	channelInfo.Large = false
	channelInfo.Disband = false
	channelInfo.UpdatedAt = &nw

	err = d.UpdateChannel(channelInfo)
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
	_, err = d.AddChannel(channelInfo)
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
	_, err = d.AddChannel(channelInfo)
	assert.NoError(t, err)

	err = d.DeleteChannel(channelInfo.ChannelId, channelInfo.ChannelType)
	assert.NoError(t, err)

	exist, err := d.ExistChannel(channelInfo.ChannelId, channelInfo.ChannelType)
	assert.NoError(t, err)
	assert.False(t, exist)
}
