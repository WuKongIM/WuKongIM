package gateway

type connContext struct {
	ringBuffer *RingBuffer
	g          *Gateway
}

func newConnContext(g *Gateway) *connContext {
	return &connContext{
		ringBuffer: &RingBuffer{},
		g:          g,
	}
}

func (c *connContext) write(data []byte) (int, error) {
	return c.ringBuffer.Write(data)
}

func (c *connContext) start() {
}

func (c *connContext) stop() {

}
