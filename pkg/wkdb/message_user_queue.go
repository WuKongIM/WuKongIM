package wkdb

func (wk *wukongDB) AppendMessagesOfUserQueue(uid string, messages []Message) error {
	return nil
}

func (wk *wukongDB) UpdateMessageOfUserQueueCursorIfNeed(uid string, messageSeq uint64) error {
	return nil
}
