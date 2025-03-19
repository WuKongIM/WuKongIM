package wkdb_test

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/stretchr/testify/assert"
)

func TestAddOrUpdatePlugin(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	plugin := wkdb.Plugin{
		No:             "plugin1",
		Name:           "Test Plugin",
		ConfigTemplate: []byte(`{"key":"value"}`),
		Config:         map[string]interface{}{"key": "value"},
		CreatedAt:      timePtr(time.Now()),
		UpdatedAt:      timePtr(time.Now()),
		Status:         1,
		Version:        "1.0.0",
		Methods:        []string{"GET", "POST"},
		Priority:       1,
	}

	err = d.AddOrUpdatePlugin(plugin)
	assert.NoError(t, err)

	plugins, err := d.GetPlugins()
	assert.NoError(t, err)
	assert.Len(t, plugins, 1)
	assert.Equal(t, plugin.No, plugins[0].No)
}

func TestDeletePlugin(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	plugin := wkdb.Plugin{
		No:             "plugin1",
		Name:           "Test Plugin",
		ConfigTemplate: []byte(`{"key":"value"}`),
		Config:         map[string]interface{}{"key": "value"},
		CreatedAt:      timePtr(time.Now()),
		UpdatedAt:      timePtr(time.Now()),
		Status:         1,
		Version:        "1.0.0",
		Methods:        []string{"GET", "POST"},
		Priority:       1,
	}

	err = d.AddOrUpdatePlugin(plugin)
	assert.NoError(t, err)

	err = d.DeletePlugin(plugin.No)
	assert.NoError(t, err)

	plugins, err := d.GetPlugins()
	assert.NoError(t, err)
	assert.Len(t, plugins, 0)
}

func TestGetPlugins(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	plugin1 := wkdb.Plugin{
		No:             "plugin1",
		Name:           "Test Plugin 1",
		ConfigTemplate: []byte(`{"key":"value1"}`),
		Config:         map[string]interface{}{"key": "value1"},
		CreatedAt:      timePtr(time.Now()),
		UpdatedAt:      timePtr(time.Now()),
		Status:         1,
		Version:        "1.0.0",
		Methods:        []string{"GET", "POST"},
		Priority:       1,
	}

	plugin2 := wkdb.Plugin{
		No:             "plugin2",
		Name:           "Test Plugin 2",
		ConfigTemplate: []byte(`{"key":"value2"}`),
		Config:         map[string]interface{}{"key": "value2"},
		CreatedAt:      timePtr(time.Now()),
		UpdatedAt:      timePtr(time.Now()),
		Status:         1,
		Version:        "1.0.0",
		Methods:        []string{"PUT", "DELETE"},
		Priority:       2,
	}

	err = d.AddOrUpdatePlugin(plugin1)
	assert.NoError(t, err)
	err = d.AddOrUpdatePlugin(plugin2)
	assert.NoError(t, err)

	plugins, err := d.GetPlugins()
	assert.NoError(t, err)
	assert.Len(t, plugins, 2)

}

func TestUpdatePluginConfig(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	plugin := wkdb.Plugin{
		No:             "plugin1",
		Name:           "Test Plugin",
		ConfigTemplate: []byte(`{"key":"value"}`),
		Config:         map[string]interface{}{"key": "value"},
		CreatedAt:      timePtr(time.Now()),
		UpdatedAt:      timePtr(time.Now()),
		Status:         1,
		Version:        "1.0.0",
		Methods:        []string{"GET", "POST"},
		Priority:       1,
	}

	err = d.AddOrUpdatePlugin(plugin)
	assert.NoError(t, err)

	err = d.UpdatePluginConfig(plugin.No, map[string]interface{}{"key": "value2"})
	assert.NoError(t, err)

	resultPlugin, err := d.GetPlugin(plugin.No)
	assert.NoError(t, err)
	assert.Equal(t, "value2", resultPlugin.Config["key"])
}

func timePtr(t time.Time) *time.Time {
	return &t
}
