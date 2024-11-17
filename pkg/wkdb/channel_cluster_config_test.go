package wkdb_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/stretchr/testify/assert"
)

func TestSaveChannelClusterConfig(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	channelId := "channel1"
	channelType := uint8(1)

	createdAt := time.Now()
	updatedAt := time.Now()
	config := wkdb.ChannelClusterConfig{
		ChannelId:       channelId,
		ChannelType:     channelType,
		ReplicaMaxCount: 3,
		Replicas:        []uint64{1, 2, 3},
		LeaderId:        1001,
		Term:            1,
		CreatedAt:       &createdAt,
		UpdatedAt:       &updatedAt,
	}

	err = d.SaveChannelClusterConfig(config)
	assert.NoError(t, err)

}

func TestSaveChannelClusterConfigs(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	channelId := "channel1"
	channelType := uint8(1)

	createdAt := time.Now()
	updatedAt := time.Now()

	count := 100
	configs := make([]wkdb.ChannelClusterConfig, 0)
	for i := 0; i < count; i++ {
		configs = append(configs, wkdb.ChannelClusterConfig{
			ChannelId:       fmt.Sprintf("%s%d", channelId, i),
			ChannelType:     channelType,
			ReplicaMaxCount: 3,
			Replicas:        []uint64{1, 2, 3},
			LeaderId:        1001 + uint64(i),
			Term:            1,
			CreatedAt:       &createdAt,
			UpdatedAt:       &updatedAt,
		})
	}

	err = d.SaveChannelClusterConfigs(configs)
	assert.NoError(t, err)

	results, err := d.GetChannelClusterConfigs(0, count)
	assert.NoError(t, err)

	assert.Equal(t, len(results), count)

}

func TestGetChannelClusterConfig(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	channelId := "channel1"
	channelType := uint8(1)
	createdAt := time.Now()
	updatedAt := time.Now()
	config := wkdb.ChannelClusterConfig{
		ChannelId:       channelId,
		ChannelType:     channelType,
		ReplicaMaxCount: 3,
		Replicas:        []uint64{1, 2, 3},
		LeaderId:        1001,
		Term:            1,
		CreatedAt:       &createdAt,
		UpdatedAt:       &updatedAt,
	}

	err = d.SaveChannelClusterConfig(config)
	assert.NoError(t, err)

	config2, err := d.GetChannelClusterConfig(channelId, channelType)
	assert.NoError(t, err)

	assert.Equal(t, config.ChannelId, config2.ChannelId)
	assert.Equal(t, config.ChannelType, config2.ChannelType)
	assert.Equal(t, config.ReplicaMaxCount, config2.ReplicaMaxCount)
	assert.Equal(t, config.LeaderId, config2.LeaderId)
	assert.Equal(t, config.Term, config2.Term)
	assert.Equal(t, config.Replicas, config2.Replicas)
	assert.Equal(t, config.CreatedAt.Unix(), config2.CreatedAt.Unix())
	assert.Equal(t, config.UpdatedAt.Unix(), config2.UpdatedAt.Unix())

}

func TestGetChannelClusterConfigs(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	config1 := wkdb.ChannelClusterConfig{
		ChannelId:       "channel1",
		ChannelType:     1,
		ReplicaMaxCount: 3,
		Replicas:        []uint64{1, 2, 3},
		LeaderId:        1001,
		Term:            1,
	}

	err = d.SaveChannelClusterConfig(config1)
	assert.NoError(t, err)

	createdAt := time.Now()
	updatedAt := time.Now()
	config2 := wkdb.ChannelClusterConfig{
		ChannelId:       "channel2",
		ChannelType:     1,
		ReplicaMaxCount: 3,
		Replicas:        []uint64{4, 5, 6},
		LeaderId:        1001,
		Term:            1,
		CreatedAt:       &createdAt,
		UpdatedAt:       &updatedAt,
	}

	err = d.SaveChannelClusterConfig(config2)
	assert.NoError(t, err)

	configs, err := d.GetChannelClusterConfigs(0, 100)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(configs))

	configs, err = d.GetChannelClusterConfigs(configs[0].Id, 100)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(configs))
}
