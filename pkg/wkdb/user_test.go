package wkdb_test

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/stretchr/testify/assert"
)

func TestUserAddOrUpdate(t *testing.T) {
	d := wkdb.NewWukongDB(wkdb.NewOptions(wkdb.WithDir(t.TempDir())))
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	tn := time.Now()
	u := wkdb.User{
		Uid:       "test",
		CreatedAt: &tn,
		UpdatedAt: &tn,
	}

	err = d.AddOrUpdateUser(u)
	assert.NoError(t, err)
}

func TestGetUser(t *testing.T) {
	d := wkdb.NewWukongDB(wkdb.NewOptions(wkdb.WithDir(t.TempDir())))
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	tn := time.Now()
	u := wkdb.User{
		Uid:       "test",
		CreatedAt: &tn,
		UpdatedAt: &tn,
	}

	err = d.AddOrUpdateUser(u)
	assert.NoError(t, err)

	u2, err := d.GetUser("test")
	assert.NoError(t, err)

	assert.Equal(t, u.Uid, u2.Uid)
	assert.Equal(t, u.CreatedAt.Unix(), u2.CreatedAt.Unix())
	assert.Equal(t, u.UpdatedAt.Unix(), u2.UpdatedAt.Unix())
}
