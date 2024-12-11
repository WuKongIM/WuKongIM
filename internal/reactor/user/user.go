package reactor

type User struct {
	no    string // 唯一编号
	uid   string
	conns []Conn // 一个用户有多个连接

	connectQueue *msgQueue // 连接队列

	onSendQueue *msgQueue // 发送队列

	pingQueue *msgQueue // ping队列

	recvQueue *msgQueue // 接收消息队列
}

func (u *User) ready() []Action {

	return nil
}

func (u *User) step(action Action) error {

	return nil
}
