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
	err := w.Commit(wk.sync)
	if err != nil {
		return EmptySession, err
	}
	session.Id = id

	err = wk.IncSessionCount(1)
	if err != nil {
		return EmptySession, err
	}

	return session, nil
}

func (wk *wukongDB) GetSession(uid string, id uint64) (Session, error) {

	iter := wk.shardDB(uid).NewIter(&pebble.IterOptions{
		LowerBound: key.NewSessionColumnKey(uid, id, key.MinColumnKey),
		UpperBound: key.NewSessionColumnKey(uid, id, key.MaxColumnKey),
	})

	defer iter.Close()

	var session Session

	err := wk.iteratorSession(iter, func(s Session) bool {
		session = s
		return false
	})
	if err != nil {
		return EmptySession, err
	}
	return session, nil
}

func (wk *wukongDB) SearchSession(req SessionSearchReq) ([]Session, error) {

	if req.Uid != "" {
		sessions, err := wk.GetSessions(req.Uid)
		if err != nil {
			return nil, err
		}
		return sessions, nil
	}

	var sessions []Session
	currentSize := 0
	var err error
	for _, db := range wk.dbs {
		iter := db.NewIter(&pebble.IterOptions{
			LowerBound: key.NewSessionUidHashKey(0),
			UpperBound: key.NewSessionUidHashKey(math.MaxUint64),
		})
		defer iter.Close()

		err = wk.iteratorSession(iter, func(s Session) bool {
			if currentSize > req.Limit*req.CurrentPage { // 大于当前页的消息终止遍历
				return false
			}
			currentSize++
			if currentSize > (req.CurrentPage-1)*req.Limit && currentSize <= req.CurrentPage*req.Limit {
				sessions = append(sessions, s)
				return true
			}
			return true
		})
		if err != nil {
			return nil, err
		}
	}
	return sessions, nil

}

func (wk *wukongDB) DeleteSession(uid string, id uint64) error {

	batch := wk.shardDB(uid).NewBatch()
	defer batch.Close()
	err := wk.deleteSession(uid, id, batch)
	if err != nil {
		return err
	}
	return batch.Commit(wk.sync)
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

	// 会话数量减少
	err = wk.IncSessionCount(-1)
	if err != nil {
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
	return batch.Commit(wk.sync)
}

func (wk *wukongDB) GetSessions(uid string) ([]Session, error) {

	iter := wk.shardDB(uid).NewIter(&pebble.IterOptions{
		LowerBound: key.NewSessionColumnKey(uid, 0, key.MinColumnKey),
		UpperBound: key.NewSessionColumnKey(uid, math.MaxUint64, key.MaxColumnKey),
	})
	defer iter.Close()

	var sessions []Session
	err := wk.iteratorSession(iter, func(s Session) bool {
		sessions = append(sessions, s)
		return true
	})
	if err != nil {
		return nil, err
	}
	return sessions, nil

}

func (wk *wukongDB) DeleteSessionByUid(uid string) error {

	return wk.shardDB(uid).DeleteRange(key.NewSessionColumnKey(uid, 0, [2]byte{0x00, 0x00}), key.NewSessionColumnKey(uid, math.MaxUint64, [2]byte{0xff, 0xff}), wk.sync)

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

	// dbUidsMap := make(map[uint32][]string)

	dbUidsMap := make(map[uint32]map[string]map[string]uint64)

	for _, model := range models {
		for uid, seq := range model.Uids {
			shardId := wk.shardId(uid)
			channelUidsMap := dbUidsMap[shardId]
			if channelUidsMap == nil {
				channelUidsMap = make(map[string]map[string]uint64)
				dbUidsMap[shardId] = channelUidsMap
			}
			uidSeqMap := channelUidsMap[ChannelToKey(model.ChannelId, model.ChannelType)]
			if uidSeqMap == nil {
				uidSeqMap = make(map[string]uint64)
			}
			uidSeqMap[uid] = seq
			channelUidsMap[ChannelToKey(model.ChannelId, model.ChannelType)] = uidSeqMap

		}
	}
	for shardId, channelUidsMap := range dbUidsMap {
		shardDB := wk.shardDBById(shardId)
		batch := shardDB.NewBatch()
		defer batch.Close()
		for channelKey, uidSeqArrayMap := range channelUidsMap {
			channelId, channelType := channelFromKey(channelKey)
			if err := wk.updateSessionUpdatedAt(uidSeqArrayMap, channelId, channelType, batch); err != nil {
				return err
			}
		}

		if err := batch.Commit(wk.sync); err != nil {
			return err
		}
	}

	return nil
}

func (wk *wukongDB) updateSessionUpdatedAt(uids map[string]uint64, channelId string, channelType uint8, w pebble.Writer) error {
	nw := time.Now()
	updatedAtBytes := make([]byte, 8)
	wk.endian.PutUint64(updatedAtBytes, uint64(nw.UnixNano()))

	var sessionCreateCount int // 新增会话数量
	for uid, messageSeq := range uids {

		sessionId, err := wk.getSessionIdByChannel(uid, channelId, channelType)
		if err != nil {
			return err
		}

		if sessionId == 0 {
			sessionCreateCount++
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

		if messageSeq > 0 { // 如果消息seq大于0，更新会话的已读消息seq
			err = wk.updateOrAddReadedToMsgSeq(uid, sessionId, messageSeq, w)
			if err != nil {
				return err
			}
		}
	}

	// 会话数量增加
	err := wk.IncSessionCount(sessionCreateCount)
	if err != nil {
		return err
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

func (wk *wukongDB) iteratorSession(iter *pebble.Iterator, iterFnc func(s Session) bool) error {

	var (
		preId      uint64
		preSession Session
	)

	for iter.First(); iter.Valid(); iter.Next() {
		id, coulmnName, err := key.ParseConversationColumnKey(iter.Key())
		if err != nil {
			return err
		}
		if preId != id {
			if preId != 0 {
				if !iterFnc(preSession) {
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
	}
	if preId != 0 {
		if !iterFnc(preSession) {
			return nil
		}
	}
	return nil
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
