package wkdb_test

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/stretchr/testify/assert"
)

func TestDeviceAdd(t *testing.T) {
	d := wkdb.NewWukongDB(wkdb.NewOptions(wkdb.WithDir(t.TempDir())))
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	u := wkdb.Device{
		Id:          1,
		Uid:         "test",
		Token:       "token",
		DeviceFlag:  2,
		DeviceLevel: 1,
	}

	err = d.AddDevice(u)
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
		Id:          1,
		Uid:         "test",
		Token:       "token",
		DeviceFlag:  2,
		DeviceLevel: 1,
	}

	err = d.AddDevice(u)
	assert.NoError(t, err)

	u2, err := d.GetDevice("test", 2)
	assert.NoError(t, err)

	assert.Equal(t, u.Id, u2.Id)
	assert.Equal(t, u.Uid, u2.Uid)
	assert.Equal(t, u.Token, u2.Token)
	assert.Equal(t, u.DeviceFlag, u2.DeviceFlag)
	assert.Equal(t, u.DeviceLevel, u2.DeviceLevel)
}

func TestGetDevices(t *testing.T) {
	d := wkdb.NewWukongDB(wkdb.NewOptions(wkdb.WithDir(t.TempDir())))
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	u := wkdb.Device{
		Id:          1,
		Uid:         "test",
		Token:       "token",
		DeviceFlag:  2,
		DeviceLevel: 1,
	}

	err = d.AddDevice(u)
	assert.NoError(t, err)

	u = wkdb.Device{
		Id:          2,
		Uid:         "test2",
		Token:       "token2",
		DeviceFlag:  1,
		DeviceLevel: 0,
	}

	err = d.AddDevice(u)
	assert.NoError(t, err)

	us, err := d.GetDevices("test")
	assert.NoError(t, err)

	assert.Equal(t, 1, len(us))

}

func TestSearchDevices(t *testing.T) {
	d := wkdb.NewWukongDB(wkdb.NewOptions(wkdb.WithDir(t.TempDir())))
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	createdAt := time.Now()
	u := wkdb.Device{
		Id:          1,
		Uid:         "test",
		Token:       "token",
		DeviceFlag:  2,
		DeviceLevel: 1,
		CreatedAt:   &createdAt,
		UpdatedAt:   &createdAt,
	}

	err = d.AddDevice(u)
	assert.NoError(t, err)

	u = wkdb.Device{
		Id:          2,
		Uid:         "test2",
		Token:       "token2",
		DeviceFlag:  1,
		DeviceLevel: 0,
		CreatedAt:   &createdAt,
		UpdatedAt:   &createdAt,
	}

	err = d.AddDevice(u)
	assert.NoError(t, err)

	us, err := d.SearchDevice(wkdb.DeviceSearchReq{
		Uid:   "test",
		Limit: 10,
	})
	assert.NoError(t, err)

	assert.Equal(t, 1, len(us))

}
