package server

func (u *userHandler) step(a *UserAction) error {

	switch a.ActionType {
	case UserActionProcess:
		for _, msg := range a.Messages {
			msg.Index = u.msgQueue.lastIndex + 1
			u.msgQueue.appendMessage(msg)
		}
		// case UserActionProcessResp:
		// 	fmt.Println("a.Index---->", a.Index)
		// 	u.msgQueue.processTo(a.Index)
	}

	return nil

}
