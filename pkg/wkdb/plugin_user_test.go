package wkdb_test

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/stretchr/testify/assert"
)

func TestAddPluginUsers(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	pluginNo := "plugin1"
	uids := []string{"user1", "user2"}
	pluginUsers := make([]wkdb.PluginUser, 0, len(uids))
	for _, uid := range uids {
		pluginUsers = append(pluginUsers, wkdb.PluginUser{
			PluginNo: pluginNo,
			Uid:      uid,
		})
	}

	err = d.AddOrUpdatePluginUsers(pluginUsers)
	assert.NoError(t, err)

	resultUids, err := d.GetPluginUsers(pluginNo)
	assert.NoError(t, err)

	assert.ElementsMatch(t, uids, resultUids)

}

func TestRemovePluginUser(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	pluginNo := "plugin1"
	uid := "user1"

	err = d.AddOrUpdatePluginUsers([]wkdb.PluginUser{
		{
			PluginNo: pluginNo,
			Uid:      uid,
		},
	})
	assert.NoError(t, err)

	err = d.RemovePluginUser(pluginNo, uid)
	assert.NoError(t, err)

	resultUids, err := d.GetPluginUsers(pluginNo)
	assert.NoError(t, err)

	assert.Len(t, resultUids, 0)
}

func TestGetPluginUsers(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	pluginNo := "plugin1"
	uids := []string{"user1", "user2"}

	pluginUsers := make([]wkdb.PluginUser, 0, len(uids))
	for _, uid := range uids {
		pluginUsers = append(pluginUsers, wkdb.PluginUser{
			PluginNo: pluginNo,
			Uid:      uid,
		})
	}

	err = d.AddOrUpdatePluginUsers(pluginUsers)
	assert.NoError(t, err)

	resultUids, err := d.GetPluginUsers(pluginNo)
	assert.NoError(t, err)

	assert.ElementsMatch(t, uids, resultUids)
}

func TestGetHighestPriorityPluginByUid(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	uid := "uid1"

	plugin := wkdb.Plugin{
		No:             "plugin1",
		Name:           "Test Plugin",
		ConfigTemplate: []byte(`{"key":"value"}`),
		CreatedAt:      timePtr(time.Now()),
		UpdatedAt:      timePtr(time.Now()),
		Status:         1,
		Version:        "1.0.0",
		Methods:        []string{"GET", "POST"},
		Priority:       1,
	}

	err = d.AddOrUpdatePlugin(plugin)
	assert.NoError(t, err)

	err = d.AddOrUpdatePluginUsers([]wkdb.PluginUser{
		{
			PluginNo: plugin.No,
			Uid:      uid,
		},
	})
	assert.NoError(t, err)

	resultPluginNo, err := d.GetHighestPriorityPluginByUid(uid)
	assert.NoError(t, err)

	assert.Equal(t, plugin.No, resultPluginNo)
}
