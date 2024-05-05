package wkdb

import (
	"math"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb/key"
	"github.com/cockroachdb/pebble"
)

func (wk *wukongDB) AddOrUpdateSession(session Session) (Session, error) {

	var id uint64
	if session.Id != 0 {
		id = session.Id
	} else {
		id = uint64(wk.prmaryKeyGen.Generate().Int64())
	}

	w := wk.shardDB(session.Uid).NewBatch()
	defer w.Close()

	if err := wk.writeSession(id, session, w); err != nil {
		return EmptySession, err
	}
	err := w.Commit(wk.wo)
	if err != nil {
		return EmptySession, err
	}
	session.Id = id

	return session, nil
}

func (wk *wukongDB) GetSession(uid string, id uint64) (Session, error) {

	iter := wk.shardDB(uid).NewIter(&pebble.IterOptions{
		LowerBound: key.NewSessionColumnKey(uid, id, [2]byte{0x00, 0x00}),
		UpperBound: key.NewSessionColumnKey(uid, id, [2]byte{0xff, 0xff}),
	})

	defer iter.Close()

	sessions, err := wk.parseSessions(iter, 1)
	if err != nil {
		return EmptySession, err
	}

	if len(sessions) == 0 {
		return EmptySession, nil
	}
	return sessions[0], nil
}

func (wk *wukongDB) DeleteSession(uid string, id uint64) error {

	batch := wk.shardDB(uid).NewBatch()
	defer batch.Close()
	err := wk.deleteSession(uid, id, batch)
	if err != nil {
		return err
	}
	return batch.Commit(wk.wo)
}

func (wk *wukongDB) deleteSession(uid string, id uint64, w pebble.Writer) error {
	session, err := wk.GetSession(uid, id)
	if err != nil {
		return err
	}
	if IsEmptySession(session) {
		return nil
	}
	if err := w.DeleteRange(key.NewSessionColumnKey(uid, id, [2]byte{0x00, 0x00}), key.NewSessionColumnKey(uid, id, [2]byte{0xff, 0xff}), wk.noSync); err != nil {
		return err
	}

	if err = w.Delete(key.NewSessionChannelIndexKey(uid, session.ChannelId, session.ChannelType), wk.noSync); err != nil {
		return err
	}

	if err = w.Delete(key.NewSessionSecondIndexKey(uid, key.TableSession.Column.CreatedAt, uint64(session.CreatedAt.UnixNano()), id), wk.noSync); err != nil {
		return err
	}

	if err = w.Delete(key.NewSessionSecondIndexKey(uid, key.TableSession.Column.UpdatedAt, uint64(session.UpdatedAt.UnixNano()), id), wk.noSync); err != nil {
		return err
	}

	return nil
}

func (wk *wukongDB) DeleteSessionByChannel(uid string, channelId string, channelType uint8) error {

	sessionId, err := wk.getSessionIdByChannel(uid, channelId, channelType)
	if err != nil {
		return err
	}

	if sessionId == 0 {
		return nil
	}

	return wk.DeleteSession(uid, sessionId)
}

func (wk *wukongDB) DeleteSessionAndConversationByChannel(uid string, channelId string, channelType uint8) error {

	sessionId, err := wk.getSessionIdByChannel(uid, channelId, channelType)
	if err != nil {
		return err
	}

	if sessionId == 0 {
		return nil
	}

	batch := wk.shardDB(uid).NewBatch()
	defer batch.Close()

	if err = wk.deleteConversation(uid, sessionId, batch); err != nil {
		return err
	}

	if err = wk.deleteSession(uid, sessionId, batch); err != nil {
		return err
	}
	return batch.Commit(wk.wo)
}

func (wk *wukongDB) GetSessions(uid string) ([]Session, error) {

	iter := wk.shardDB(uid).NewIter(&pebble.IterOptions{
		LowerBound: key.NewSessionColumnKey(uid, 0, [2]byte{0x00, 0x00}),
		UpperBound: key.NewSessionColumnKey(uid, math.MaxUint64, [2]byte{0xff, 0xff}),
	})
	defer iter.Close()
	return wk.parseSessions(iter, 0)
}

func (wk *wukongDB) DeleteSessionByUid(uid string) error {

	return wk.shardDB(uid).DeleteRange(key.NewSessionColumnKey(uid, 0, [2]byte{0x00, 0x00}), key.NewSessionColumnKey(uid, math.MaxUint64, [2]byte{0xff, 0xff}), wk.wo)

}

func (wk *wukongDB) GetSessionByChannel(uid string, channelId string, channelType uint8) (Session, error) {

	sessionId, err := wk.getSessionIdByChannel(uid, channelId, channelType)
	if err != nil {
		return EmptySession, err
	}

	if sessionId == 0 {
		return EmptySession, nil
	}
	return wk.GetSession(uid, sessionId)
}

func (wk *wukongDB) UpdateSessionUpdatedAt(models []*UpdateSessionUpdatedAtModel) error {

	wk.dblock.updateSessionUpdatedAtLock.Lock()
	defer wk.dblock.updateSessionUpdatedAtLock.Unlock()

	dbUidsMap := make(map[uint32][]string)

	for _, model := range models {
		for _, uid := range model.Uids {
			shardId := wk.shardId(uid)
			dbUidsMap[shardId] = append(dbUidsMap[shardId], uid)
		}

		for shardId, uids := range dbUidsMap {
			shardDB := wk.shardDBById(shardId)
			batch := shardDB.NewBatch()
			defer batch.Close()
			if err := wk.updateSessionUpdatedAt(uids, model.ChannelId, model.ChannelType, batch); err != nil {
				return err
			}
			if err := batch.Commit(wk.wo); err != nil {
				return err
			}
		}
	}

	return nil
}

func (wk *wukongDB) updateSessionUpdatedAt(uids []string, channelId string, channelType uint8, w pebble.Writer) error {
	nw := time.Now()
	updatedAtBytes := make([]byte, 8)
	wk.endian.PutUint64(updatedAtBytes, uint64(nw.UnixNano()))
	for _, uid := range uids {

		sessionId, err := wk.getSessionIdByChannel(uid, channelId, channelType)
		if err != nil {
			return err
		}

		if sessionId == 0 {
			sessionId = uint64(wk.prmaryKeyGen.Generate().Int64())
			if err = wk.writeSession(sessionId, Session{
				Id:          sessionId,
				Uid:         uid,
				ChannelId:   channelId,
				ChannelType: channelType,
				CreatedAt:   nw,
				UpdatedAt:   nw,
			}, w); err != nil {
				return err
			}
		} else {
			if err = w.Set(key.NewSessionColumnKey(uid, sessionId, key.TableSession.Column.UpdatedAt), updatedAtBytes, wk.noSync); err != nil {
				return err
			}
		}
	}

	return nil
}

func (wk *wukongDB) GetLastSessionsByUid(uid string, limit int) ([]Session, error) {

	ids, err := wk.getLastSessionIdsOrderByUpdatedAt(uid, 0, limit)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	sessions := make([]Session, 0, len(ids))

	for _, id := range ids {
		session, err := wk.GetSession(uid, id)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	return sessions, nil
}

func (wk *wukongDB) GetSessionsGreaterThanUpdatedAtByUid(uid string, updatedAt int64, limit int) ([]Session, error) {
	ids, err := wk.getLastSessionIdsOrderByUpdatedAt(uid, uint64(updatedAt)+1, limit)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}

	sessions := make([]Session, 0, len(ids))

	for _, id := range ids {
		session, err := wk.GetSession(uid, id)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	return sessions, nil
}

func (wk *wukongDB) getLastSessionIdsOrderByUpdatedAt(uid string, updatedAt uint64, limit int) ([]uint64, error) {

	iter := wk.shardDB(uid).NewIter(&pebble.IterOptions{
		LowerBound: key.NewSessionSecondIndexKey(uid, key.TableSession.Column.UpdatedAt, updatedAt, 0),
		UpperBound: key.NewSessionSecondIndexKey(uid, key.TableSession.Column.UpdatedAt, math.MaxUint64, math.MaxUint64),
	})

	defer iter.Close()

	var (
		ids = make([]uint64, 0)
	)

	for iter.Last(); iter.Valid(); iter.Prev() {
		id, _, _, err := key.ParseSessionSecondIndexKey(iter.Key())
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
		if limit > 0 && len(ids) >= limit {
			break
		}
	}
	return ids, nil
}

func (wk *wukongDB) getSessionIdByChannel(uid string, channelId string, channelType uint8) (uint64, error) {

	result, closer, err := wk.shardDB(uid).Get(key.NewSessionChannelIndexKey(uid, channelId, channelType))
	if err != nil {
		if err == pebble.ErrNotFound {
			return 0, nil
		}
		return 0, err
	}
	defer closer.Close()

	if result != nil {
		return wk.endian.Uint64(result), nil
	}

	return 0, nil
}

func (wk *wukongDB) parseSessions(iter *pebble.Iterator, limit int) ([]Session, error) {

	var (
		sessions       = make([]Session, 0, limit)
		preId          uint64
		preSession     Session
		lastNeedAppend bool = true
		hasData        bool = false
	)

	for iter.First(); iter.Valid(); iter.Next() {
		id, coulmnName, err := key.ParseConversationColumnKey(iter.Key())
		if err != nil {
			return nil, err
		}
		if preId != id {
			if preId != 0 {
				sessions = append(sessions, preSession)
				if limit > 0 && len(sessions) >= limit {
					lastNeedAppend = false
					break
				}
			}

			preId = id
			preSession = Session{
				Id: id,
			}
		}
		switch coulmnName {
		case key.TableSession.Column.Uid:
			preSession.Uid = string(iter.Value())
		case key.TableSession.Column.ChannelId:
			preSession.ChannelId = string(iter.Value())
		case key.TableSession.Column.ChannelType:
			preSession.ChannelType = iter.Value()[0]
		case key.TableSession.Column.CreatedAt:
			t := int64(wk.endian.Uint64(iter.Value()))
			preSession.CreatedAt = time.Unix(t/1e9, t%1e9)
		case key.TableSession.Column.UpdatedAt:
			t := int64(wk.endian.Uint64(iter.Value()))
			preSession.UpdatedAt = time.Unix(t/1e9, t%1e9)

		}
		hasData = true
	}
	if lastNeedAppend && hasData {
		sessions = append(sessions, preSession)
	}

	return sessions, nil
}

func (wk *wukongDB) writeSession(id uint64, session Session, w pebble.Writer) error {
	var (
		err error
	)

	// uid
	if err = w.Set(key.NewSessionColumnKey(session.Uid, id, key.TableSession.Column.Uid), []byte(session.Uid), wk.noSync); err != nil {
		return err
	}

	// channelId
	if err = w.Set(key.NewSessionColumnKey(session.Uid, id, key.TableSession.Column.ChannelId), []byte(session.ChannelId), wk.noSync); err != nil {
		return err
	}

	// channelType
	if err = w.Set(key.NewSessionColumnKey(session.Uid, id, key.TableSession.Column.ChannelType), []byte{session.ChannelType}, wk.noSync); err != nil {
		return err
	}

	// createdAt
	createdAtBytes := make([]byte, 8)
	createdAt := uint64(session.CreatedAt.UnixNano())
	wk.endian.PutUint64(createdAtBytes, createdAt)
	if err = w.Set(key.NewSessionColumnKey(session.Uid, id, key.TableSession.Column.CreatedAt), createdAtBytes, wk.noSync); err != nil {
		return err
	}

	// updatedAt
	updatedAtBytes := make([]byte, 8)
	updatedAt := uint64(session.UpdatedAt.UnixNano())
	wk.endian.PutUint64(updatedAtBytes, updatedAt)
	if err = w.Set(key.NewSessionColumnKey(session.Uid, id, key.TableSession.Column.UpdatedAt), updatedAtBytes, wk.noSync); err != nil {
		return err
	}

	// channel index
	idBytes := make([]byte, 8)
	wk.endian.PutUint64(idBytes, id)
	if err = w.Set(key.NewSessionChannelIndexKey(session.Uid, session.ChannelId, session.ChannelType), idBytes, wk.noSync); err != nil {
		return err
	}

	// createdAt second index
	if err = w.Set(key.NewSessionSecondIndexKey(session.Uid, key.TableSession.Column.CreatedAt, createdAt, id), nil, wk.noSync); err != nil {
		return err
	}

	// updatedAt second index
	if err = w.Set(key.NewSessionSecondIndexKey(session.Uid, key.TableSession.Column.UpdatedAt, updatedAt, id), nil, wk.noSync); err != nil {
		return err
	}

	return nil
}
