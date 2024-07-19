package clusterstore

// // AddOrUpdateSession 添加或更新session
// func (s *Store) AddOrUpdateSession(session wkdb.Session) error {

// 	data, err := session.Marshal()
// 	if err != nil {
// 		return err
// 	}

// 	cmd := NewCMD(CMDAddOrUpdateSession, data)
// 	cmdData, err := cmd.Marshal()
// 	if err != nil {
// 		return err
// 	}

// 	slotId := s.opts.GetSlotId(session.Uid)
// 	_, err = s.opts.Cluster.ProposeDataToSlot(s.ctx, slotId, cmdData)
// 	if err != nil {
// 		return err
// 	}

// 	return nil
// }

// // DeleteSessionByUid 删除用户的session
// func (s *Store) DeleteSessionByUid(uid string) error {

// 	cmd := NewCMD(CMDDeleteSessionByUid, []byte(uid))
// 	cmdData, err := cmd.Marshal()
// 	if err != nil {
// 		return err
// 	}

// 	slotId := s.opts.GetSlotId(uid)
// 	_, err = s.opts.Cluster.ProposeDataToSlot(s.ctx, slotId, cmdData)
// 	return err
// }

// // DeleteSession 删除session
// func (s *Store) DeleteSession(uid string, sessionId uint64) error {

// 	cmd := NewCMD(CMDDeleteSession, EncodeCMDDeleteSession(uid, sessionId))
// 	cmdData, err := cmd.Marshal()
// 	if err != nil {
// 		return err
// 	}
// 	slotId := s.opts.GetSlotId(uid)
// 	_, err = s.opts.Cluster.ProposeDataToSlot(s.ctx, slotId, cmdData)
// 	return err
// }

// func (s *Store) DeleteSessionByChannel(uid string, channelId string, channelType uint8) error {

// 	cmd := NewCMD(CMDDeleteSessionByChannel, EncodeCMDDeleteSessionByChannel(uid, channelId, channelType))
// 	cmdData, err := cmd.Marshal()
// 	if err != nil {
// 		return err
// 	}
// 	slotId := s.opts.GetSlotId(uid)
// 	_, err = s.opts.Cluster.ProposeDataToSlot(s.ctx, slotId, cmdData)
// 	return err
// }

// func (s *Store) DeleteSessionAndConversationByChannel(uid string, channelId string, channelType uint8) error {

// 	cmd := NewCMD(CMDDeleteSessionAndConversationByChannel, EncodeCMDDeleteSessionByChannel(uid, channelId, channelType))
// 	cmdData, err := cmd.Marshal()
// 	if err != nil {
// 		return err
// 	}
// 	slotId := s.opts.GetSlotId(uid)
// 	_, err = s.opts.Cluster.ProposeDataToSlot(s.ctx, slotId, cmdData)
// 	return err
// }

// func (s *Store) UpdateSessionUpdatedAt(slotId uint32, models []*wkdb.UpdateSessionUpdatedAtModel) error {

// 	cmd := NewCMD(CMDUpdateSessionUpdatedAt, EncodeCMDUpdateSessionUpdatedAt(models))
// 	cmdData, err := cmd.Marshal()
// 	if err != nil {
// 		return err
// 	}
// 	_, err = s.opts.Cluster.ProposeDataToSlot(s.ctx, slotId, cmdData)
// 	return err
// }

// // GetSession 获取session
// func (s *Store) GetSession(uid string, sessionId uint64) (wkdb.Session, error) {

// 	return s.wdb.GetSession(uid, sessionId)
// }

// // GetSessions 获取用户的session
// func (s *Store) GetSessions(uid string) ([]wkdb.Session, error) {

// 	return s.wdb.GetSessions(uid)
// }

// func (s *Store) GetSessionsByType(uid string, sessionType wkdb.SessionType) ([]wkdb.Session, error) {

// 	return s.wdb.GetSessionsByType(uid, sessionType)
// }

// func (s *Store) GetSessionByChannel(uid string, channelId string, channelType uint8) (wkdb.Session, error) {

// 	return s.wdb.GetSessionByChannel(uid, channelId, channelType)
// }

// func (s *Store) GetLastSessionsByUid(uid string, sessionType wkdb.SessionType, limit int) ([]wkdb.Session, error) {

// 	return s.wdb.GetLastSessionsByUid(uid, sessionType, limit)
// }

// func (s *Store) GetSessionsGreaterThanUpdatedAtByUid(uid string, sessionType wkdb.SessionType, updatedAt int64, limit int) ([]wkdb.Session, error) {

// 	return s.wdb.GetSessionsGreaterThanUpdatedAtByUid(uid, sessionType, updatedAt, limit)
// }
