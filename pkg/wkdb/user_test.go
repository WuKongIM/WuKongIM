package wkdb_test

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/stretchr/testify/assert"
)

func TestUserAdd(t *testing.T) {
	d := newTestDB(t)
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

	err = d.AddUser(u)
	assert.NoError(t, err)
}

func TestGetUser(t *testing.T) {
	d := newTestDB(t)
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

	err = d.AddUser(u)
	assert.NoError(t, err)

	u2, err := d.GetUser("test")
	assert.NoError(t, err)

	assert.Equal(t, u.Uid, u2.Uid)
	assert.Equal(t, u.CreatedAt.Unix(), u2.CreatedAt.Unix())
	assert.Equal(t, u.UpdatedAt.Unix(), u2.UpdatedAt.Unix())
}

func TestSearchUser(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	tn := time.Now()
	u1 := wkdb.User{
		Uid:       "test1",
		CreatedAt: &tn,
		UpdatedAt: &tn,
	}

	tn2 := tn.Add(time.Hour)
	u2 := wkdb.User{
		Uid:       "test2",
		CreatedAt: &tn2,
		UpdatedAt: &tn2,
	}

	err = d.AddUser(u1)
	assert.NoError(t, err)

	err = d.AddUser(u2)
	assert.NoError(t, err)

	users, err := d.SearchUser(wkdb.UserSearchReq{
		Limit:           10,
		OffsetCreatedAt: time.Now().Add(30 * time.Minute).UnixNano(),
	})
	assert.NoError(t, err)

	assert.Equal(t, 1, len(users))

}

func TestExistUser(t *testing.T) {
	d := newTestDB(t)
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

	exist, err := d.ExistUser("test")
	assert.NoError(t, err)
	assert.False(t, exist)

	err = d.AddUser(u)
	assert.NoError(t, err)

	exist, err = d.ExistUser("test")
	assert.NoError(t, err)
	assert.True(t, exist)
}
