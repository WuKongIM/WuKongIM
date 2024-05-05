package wkdb_test

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/stretchr/testify/assert"
)

func newTestDB(t testing.TB) wkdb.DB {
	return wkdb.NewWukongDB(wkdb.NewOptions(wkdb.WithDir(t.TempDir()), wkdb.WithShardNum(2)))
}

func TestAddOrUpdateSession(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	uid := "test1"
	session := wkdb.Session{
		Uid:         uid,
		ChannelId:   "test",
		ChannelType: 1,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	_, err = d.AddOrUpdateSession(session)
	assert.NoError(t, err)
}

func TestGetSession(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	uid := "test1"
	session := wkdb.Session{
		Uid:         uid,
		ChannelId:   "test",
		ChannelType: 1,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	resultSession, err := d.AddOrUpdateSession(session)
	assert.NoError(t, err)

	resultSession, err = d.GetSession(uid, resultSession.Id)
	assert.NoError(t, err)
	assert.Equal(t, session.Uid, resultSession.Uid)
	assert.Equal(t, session.ChannelId, resultSession.ChannelId)
	assert.Equal(t, session.ChannelType, resultSession.ChannelType)
	assert.Equal(t, session.CreatedAt.Unix(), resultSession.CreatedAt.Unix())
	assert.Equal(t, session.UpdatedAt.Unix(), resultSession.UpdatedAt.Unix())
}

func TestDeleteSession(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	uid := "test1"
	session := wkdb.Session{
		Uid:         uid,
		ChannelId:   "test",
		ChannelType: 1,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	resultSession, err := d.AddOrUpdateSession(session)
	assert.NoError(t, err)

	err = d.DeleteSession(uid, resultSession.Id)
	assert.NoError(t, err)

	resultSession, err = d.GetSession(uid, resultSession.Id)
	assert.NoError(t, err)

	assert.Equal(t, wkdb.EmptySession, resultSession)
}

func TestDeleteSessionByChannel(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	uid := "test1"
	session := wkdb.Session{
		Uid:         uid,
		ChannelId:   "test",
		ChannelType: 1,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	resultSession, err := d.AddOrUpdateSession(session)
	assert.NoError(t, err)

	err = d.DeleteSessionByChannel(uid, session.ChannelId, session.ChannelType)
	assert.NoError(t, err)

	resultSession, err = d.GetSession(uid, resultSession.Id)
	assert.NoError(t, err)

	assert.Equal(t, wkdb.EmptySession, resultSession)
}

func TestDeleteSessionAndConversationByChannel(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	uid := "test1"
	session := wkdb.Session{
		Uid:         uid,
		ChannelId:   "test",
		ChannelType: 1,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	resultSession, err := d.AddOrUpdateSession(session)
	assert.NoError(t, err)

	err = d.DeleteSessionAndConversationByChannel(uid, session.ChannelId, session.ChannelType)
	assert.NoError(t, err)

	resultSession, err = d.GetSession(uid, resultSession.Id)
	assert.NoError(t, err)

	assert.Equal(t, wkdb.EmptySession, resultSession)
}

func TestGetSessions(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	uid := "uid1"
	session := wkdb.Session{
		Uid:         uid,
		ChannelId:   "test1",
		ChannelType: 1,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	_, err = d.AddOrUpdateSession(session)
	assert.NoError(t, err)

	session = wkdb.Session{
		Uid:         uid,
		ChannelId:   "test2",
		ChannelType: 1,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	_, err = d.AddOrUpdateSession(session)
	assert.NoError(t, err)

	sessions, err := d.GetSessions(uid)
	assert.NoError(t, err)
	assert.Len(t, sessions, 2)
}

func TestDeleteSessionByUid(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	uid := "uid1"
	session := wkdb.Session{
		Uid:         uid,
		ChannelId:   "test1",
		ChannelType: 1,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	_, err = d.AddOrUpdateSession(session)
	assert.NoError(t, err)

	session = wkdb.Session{
		Uid:         uid,
		ChannelId:   "test2",
		ChannelType: 1,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	_, err = d.AddOrUpdateSession(session)
	assert.NoError(t, err)

	err = d.DeleteSessionByUid(uid)
	assert.NoError(t, err)

	sessions, err := d.GetSessions(uid)
	assert.NoError(t, err)
	assert.Len(t, sessions, 0)
}

func TestGetSessionByChannel(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	uid := "test1"
	sessions := []wkdb.Session{
		{
			Uid:         uid,
			ChannelId:   "test1",
			ChannelType: 1,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		},
		{
			Uid:         uid,
			ChannelId:   "test2",
			ChannelType: 1,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		},
	}

	for _, session := range sessions {
		_, err = d.AddOrUpdateSession(session)
		assert.NoError(t, err)
	}

	resultSession, err := d.GetSessionByChannel(uid, sessions[1].ChannelId, sessions[1].ChannelType)
	assert.NoError(t, err)
	assert.Equal(t, sessions[1].Uid, resultSession.Uid)
	assert.Equal(t, sessions[1].ChannelId, resultSession.ChannelId)
	assert.Equal(t, sessions[1].ChannelType, resultSession.ChannelType)
	assert.Equal(t, sessions[1].CreatedAt.Unix(), resultSession.CreatedAt.Unix())
	assert.Equal(t, sessions[1].UpdatedAt.Unix(), resultSession.UpdatedAt.Unix())
}

func TestUpdateSessionUpdatedAt(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	sessions := []wkdb.Session{
		{
			Uid:         "u1",
			ChannelId:   "test1",
			ChannelType: 1,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		},
		{
			Uid:         "u2",
			ChannelId:   "test1",
			ChannelType: 1,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		},
	}

	for _, session := range sessions {
		_, err := d.AddOrUpdateSession(session)
		assert.NoError(t, err)
	}

	err = d.UpdateSessionUpdatedAt([]*wkdb.UpdateSessionUpdatedAtModel{
		{
			Uids:        map[string]uint64{"u1": 0, "u2": 0},
			ChannelId:   "test1",
			ChannelType: 1,
		},
	})
	assert.NoError(t, err)

	resultSession, err := d.GetSessionByChannel("u1", "test1", 1)
	assert.NoError(t, err)
	assert.True(t, resultSession.UpdatedAt.UnixNano() > sessions[0].UpdatedAt.UnixNano())
}

func TestGetLastSessionsByUid(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	uid := "test1"
	sessions := []wkdb.Session{
		{
			Uid:         uid,
			ChannelId:   "test1",
			ChannelType: 1,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		},
		{
			Uid:         uid,
			ChannelId:   "test2",
			ChannelType: 1,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		},
	}

	for _, session := range sessions {
		_, err = d.AddOrUpdateSession(session)
		assert.NoError(t, err)
	}

	resultSessions, err := d.GetLastSessionsByUid(uid, 1)
	assert.NoError(t, err)
	assert.Len(t, resultSessions, 1)
	assert.Equal(t, sessions[1].Uid, resultSessions[0].Uid)
	assert.Equal(t, sessions[1].ChannelId, resultSessions[0].ChannelId)
	assert.Equal(t, sessions[1].ChannelType, resultSessions[0].ChannelType)
	assert.Equal(t, sessions[1].CreatedAt.Unix(), resultSessions[0].CreatedAt.Unix())
	assert.Equal(t, sessions[1].UpdatedAt.Unix(), resultSessions[0].UpdatedAt.Unix())
}

func TestGetSessionsGreaterThanUpdatedAtByUid(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	uid := "test1"
	sessions := []wkdb.Session{
		{
			Uid:         uid,
			ChannelId:   "test1",
			ChannelType: 1,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		},
		{
			Uid:         uid,
			ChannelId:   "test2",
			ChannelType: 1,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now().Add(time.Second),
		},
	}

	for _, session := range sessions {
		_, err = d.AddOrUpdateSession(session)
		assert.NoError(t, err)
	}

	resultSessions, err := d.GetSessionsGreaterThanUpdatedAtByUid(uid, sessions[0].UpdatedAt.UnixNano(), 0)
	assert.NoError(t, err)
	assert.Len(t, resultSessions, 1)
	assert.Equal(t, sessions[1].Uid, resultSessions[0].Uid)
	assert.Equal(t, sessions[1].ChannelId, resultSessions[0].ChannelId)
	assert.Equal(t, sessions[1].ChannelType, resultSessions[0].ChannelType)
	assert.Equal(t, sessions[1].CreatedAt.Unix(), resultSessions[0].CreatedAt.Unix())
	assert.Equal(t, sessions[1].UpdatedAt.Unix(), resultSessions[0].UpdatedAt.Unix())
}
