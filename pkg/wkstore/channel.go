package wkstore

type Channel struct {
	msgChan    chan []Message
	resultChan chan []Message
	stopChan   chan struct{}
}

func NewChannel() *Channel {
	return &Channel{
		msgChan:    make(chan []Message),
		resultChan: make(chan []Message),
		stopChan:   make(chan struct{}),
	}
}

func (c *Channel) StoreMsg(msgs []Message) {
	c.msgChan <- msgs
}

func (c *Channel) ResultChan() chan []Message {
	return c.resultChan
}

func (c *Channel) StartWrite() {
	go c.loopMsg()
}

func (c *Channel) StopWrite() {
	close(c.stopChan)
}

func (c *Channel) loopMsg() {
	for {
		select {
		case msgs := <-c.msgChan:
			c.hanleMsgs(msgs)
		case <-c.stopChan:
			return
		}
	}
}

func (c *Channel) hanleMsgs(msgs []Message) {
	// save message
	// save index
	// send result
	c.resultChan <- msgs
}
