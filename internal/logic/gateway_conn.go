package logic

// type gateway struct {
// 	conn              wknet.Conn
// 	connManager       *clientConnManager
// 	inboundBuffer     wknet.InboundBuffer
// 	inboundBufferLock sync.Mutex
// 	inboundChan       chan struct{}

// 	outboundBuffer     wknet.OutboundBuffer
// 	outboundChan       chan struct{}
// 	outboundBufferLock sync.Mutex

// 	maxProcessedSeq atomic.Uint64

// 	proto *proto.Proto

// 	stoppedChan chan struct{}
// 	doneChan    chan struct{}

// 	handeInboundDataing atomic.Bool
// 	wklog.Log
// }

// func newGateway(conn wknet.Conn) *gateway {
// 	connManager := newClientConnManager()
// 	return &gateway{
// 		connManager:    connManager,
// 		conn:           conn,
// 		inboundBuffer:  wknet.NewDefaultBuffer(),
// 		proto:          proto.New(),
// 		inboundChan:    make(chan struct{}, 100),
// 		outboundChan:   make(chan struct{}),
// 		outboundBuffer: wknet.NewDefaultBuffer(),
// 		Log:            wklog.NewWKLog(fmt.Sprintf("node[%s]", conn.UID())),
// 		stoppedChan:    make(chan struct{}, 1),
// 		doneChan:       make(chan struct{}),
// 	}
// }

// func (g *gateway) gatewayID() string {
// 	return g.conn.UID()
// }

// func (g *gateway) addClientConn(conn *clientConn) {
// 	g.connManager.AddConn(conn)
// }

// func (g *gateway) removeClientConn(id int64) {
// 	g.connManager.RemoveConnWithID(id)
// }

// func (g *gateway) getClientConn(id int64) *clientConn {
// 	return g.connManager.GetConn(id)
// }

// func (g *gateway) getAllClientConn() []*clientConn {
// 	return g.connManager.GetAllConns()
// }

// func (g *gateway) reset() {
// 	g.inboundBufferLock.Lock()
// 	defer g.inboundBufferLock.Unlock()
// 	_ = g.inboundBuffer.Release()
// 	g.inboundBuffer = wknet.NewDefaultBuffer()
// 	g.maxProcessedSeq.Store(0)
// 	g.connManager.Reset()
// }

// func (n *gateway) putToInbound(data []byte) (int, error) {
// 	n.inboundBufferLock.Lock()
// 	defer n.inboundBufferLock.Unlock()
// 	count, err := n.inboundBuffer.Write(data)
// 	if err != nil {
// 		return count, err
// 	}
// 	n.inboundChan <- struct{}{}
// 	return count, nil
// }

// func (n *gateway) putToOutbound(data []byte) (int, error) {
// 	n.outboundBufferLock.Lock()
// 	defer n.outboundBufferLock.Unlock()
// 	count, err := n.outboundBuffer.Write(data)
// 	if err != nil {
// 		return count, err
// 	}
// 	n.outboundChan <- struct{}{}
// 	return count, nil
// }

// func (n *gateway) start() {
// 	go n.loopHandleInboundData()

// 	go n.loopHandleOutboundData()
// }

// func (n *gateway) stop() {
// 	n.stoppedChan <- struct{}{}
// 	n.stoppedChan <- struct{}{}
// 	<-n.doneChan
// }

// func (n *gateway) onStop() {
// 	close(n.doneChan)
// }

// func (n *gateway) loopHandleInboundData() {
// 	ticker := time.NewTicker(time.Second)
// 	defer ticker.Stop()
// 	for {
// 		select {
// 		case <-ticker.C:
// 			n.handleInboundData()
// 		case <-n.inboundChan:
// 			n.handleInboundData()
// 		case <-n.stoppedChan:
// 			return
// 		}

// 	}
// }

// func (n *gateway) loopHandleOutboundData() {
// 	ticker := time.NewTicker(time.Second)
// 	defer func() {
// 		ticker.Stop()
// 		n.onStop()
// 	}()
// 	for {
// 		select {
// 		case <-ticker.C:
// 			n.handleOutboundData()
// 		case <-n.outboundChan:
// 			n.handleOutboundData()
// 		case <-n.stoppedChan:
// 			return
// 		}

// 	}
// }

// func (n *gateway) handleInboundData() {
// 	if n.handeInboundDataing.Load() {
// 		return
// 	}

// 	n.inboundBufferLock.Lock()
// 	defer n.inboundBufferLock.Unlock()

// 	n.handeInboundDataing.Store(true)
// 	defer n.handeInboundDataing.Store(false)
// 	head, tail := n.inboundBuffer.Peek(-1)
// 	if len(head) == 0 && len(tail) == 0 {
// 		return
// 	}
// 	block, size, err := n.proto.Decode(append(head, tail...))
// 	if err != nil {
// 		n.Error("decode error", zap.Error(err))
// 		return
// 	}

// 	if block.Seq <= n.maxProcessedSeq.Load() {
// 		n.Warn("seq is less than maxProcessedSeq", zap.Uint64("seq", block.Seq), zap.Uint64("maxProcessedSeq", n.maxProcessedSeq.Load()))
// 		return
// 	}

// 	clienConn := n.getClientConn(block.ConnID)
// 	if clienConn != nil {
// 		_, err = clienConn.putToInbound(block.Data)
// 		if err != nil {
// 			n.Error("putToInbound error", zap.Error(err))
// 			return
// 		}
// 	} else {
// 		n.Debug("conn is not exist", zap.Int64("connID", block.ConnID))
// 	}

// 	_, err = n.inboundBuffer.Discard(size)
// 	if err != nil {
// 		n.Error("discard error", zap.Error(err))
// 		return
// 	}
// 	n.maxProcessedSeq.Store(block.Seq)
// }

// func (n *gateway) handleOutboundData() {

// }
