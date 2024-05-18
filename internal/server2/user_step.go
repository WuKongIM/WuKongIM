package server

func (u *userHandler) step(a *UserAction) error {

	switch a.ActionType {
	case UserActionProcess:
		for _, msg := range a.Messages {
			msg.Index = u.processMsgQueue.lastIndex + 1
			u.processMsgQueue.appendMessage(msg)
		}
	}

	return nil

}
