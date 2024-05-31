package wkdb_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/stretchr/testify/assert"
)

func TestDeviceUpdate(t *testing.T) {
	d := wkdb.NewWukongDB(wkdb.NewOptions(wkdb.WithDir(t.TempDir())))
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	u := wkdb.Device{
		Uid:         "test",
		Token:       "token",
		DeviceFlag:  2,
		DeviceLevel: 1,
	}

	err = d.AddOrUpdateDevice(u)
	assert.NoError(t, err)

}

func TestGetDevice(t *testing.T) {
	d := wkdb.NewWukongDB(wkdb.NewOptions(wkdb.WithDir(t.TempDir())))
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	u := wkdb.Device{
		Uid:         "test",
		Token:       "token",
		DeviceFlag:  2,
		DeviceLevel: 1,
	}

	err = d.AddOrUpdateDevice(u)
	assert.NoError(t, err)

	u2, err := d.GetDevice("test", 2)
	assert.NoError(t, err)

	assert.Equal(t, u.Uid, u2.Uid)
	assert.Equal(t, u.Token, u2.Token)
	assert.Equal(t, u.DeviceFlag, u2.DeviceFlag)
	assert.Equal(t, u.DeviceLevel, u2.DeviceLevel)
}
