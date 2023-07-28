package client

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

// OnRecv 收到消息事件
type OnRecv func(recv *wkproto.RecvPacket) error
type OnSendack func(sendackPacket *wkproto.SendackPacket)

type Client struct {
	Statistics
	wklog.Log

	aesKey string // aes密钥
	salt   string // 安全码

	clientIDGen atomic.Uint64

	addr   string
	writer *limWriter
	reader *limReader
	opts   *Options
	status Status

	abortReconnect bool // 是否终止重连

	conn net.Conn

	pingOut           int // 发送ping次数
	pingIntervalTimer *time.Timer
	flusherExitChan   chan struct{}

	reconnQuitCh chan struct{}

	clientPrivKey [32]byte

	wg sync.WaitGroup
	mu sync.RWMutex

	proto *wkproto.WKProto
	pongs []chan struct{}

	onRecv    OnRecv
	onSendack OnSendack

	err error
}

func New(addr string, opt ...Option) *Client {
	var opts = NewOptions()
	for _, op := range opt {
		if op != nil {
			if err := op(opts); err != nil {
				panic(err)
			}
		}
	}
	c := &Client{
		addr:  addr,
		opts:  opts,
		proto: wkproto.New(),
		Log:   wklog.NewWKLog(fmt.Sprintf("IMClient[%s]", opts.UID)),
		writer: &limWriter{
			limit:  opts.DefaultBufSize,
			plimit: opts.ReconnectBufSize,
		},
		reader: &limReader{
			buf: make([]byte, opts.DefaultBufSize),
			off: -1,
		},
	}

	return c
}

// Connect 连接到IM
func (c *Client) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	_, err := c.createConn()
	if err != nil {
		return err
	}
	c.setup()
	err = c.processConnectInit()
	if err != nil {
		c.mu.Unlock()
		c.close(DISCONNECTED, err)
		c.mu.Lock()
	}

	if err != nil && c.opts.AutoReconn {
		c.setup()
		c.status = RECONNECTING
		c.writer.switchToPending()
		go c.doReconnect()
		err = nil
	}
	return err

}

// SetOnRecv 设置收消息事件
func (c *Client) SetOnRecv(onRecv OnRecv) {
	c.onRecv = onRecv
}

func (c *Client) SetOnSendack(onSendack OnSendack) {
	c.onSendack = onSendack
}

func (c *Client) close(status Status, err error) {
	c.mu.Lock()

	if c.isClosed() {
		c.status = status
		c.mu.Unlock()
		return
	}
	c.status = CLOSED

	// Kick the Go routines so they fall out.
	c.kickFlusher()

	// If the reconnect timer is waiting between a reconnect attempt,
	// this will kick it out.
	if c.reconnQuitCh != nil {
		close(c.reconnQuitCh)
		c.reconnQuitCh = nil
	}

	// Stop ping timer if set.
	c.stopPingTimer()
	c.pingIntervalTimer = nil

	// Need to close and set TCP conn to nil if reconnect loop has stopped,
	// otherwise we would incorrectly invoke Disconnect handler (if set)
	// down below.
	if c.abortReconnect && c.conn != nil {
		c.conn.Close()
		c.conn = nil
	} else if c.conn != nil {
		// Go ahead and make sure we have flushed the outbound
		c.writer.flush()
		defer c.conn.Close()
	}

	c.status = status

	c.mu.Unlock()
}

func (c *Client) isClosed() bool {
	return c.status == CLOSED
}

func (c *Client) setup() {
	c.reconnQuitCh = make(chan struct{})
	c.flusherExitChan = make(chan struct{}, 1)
	c.pongs = make([]chan struct{}, 0, 8)

}

func (c *Client) processConnectInit() error {
	c.conn.SetDeadline(time.Now().Add(c.opts.Timeout))
	defer c.conn.SetDeadline(time.Time{})
	c.status = CONNECTING

	var err error
	if err = c.sendConnect(); err != nil {
		return err
	}
	c.pingOut = 0
	if c.opts.PingInterval > 0 {
		if c.pingIntervalTimer == nil {
			c.pingIntervalTimer = time.AfterFunc(c.opts.PingInterval, c.processPingTimer)
		} else {
			c.pingIntervalTimer.Reset(c.opts.PingInterval)
		}
	}

	c.wg.Add(2)
	go c.readLoop()
	go c.flusher()

	c.reader.doneWithConnect()

	return nil
}

func (c *Client) kickFlusher() {
	if c.writer != nil {
		select {
		case c.flusherExitChan <- struct{}{}:
		default:
		}
	}
}

func (c *Client) processPingTimer() {
	c.mu.Lock()
	if c.status != CONNECTED {
		c.mu.Unlock()
		return
	}
	c.pingOut++

	if c.pingOut > c.opts.MaxPingCount {
		c.mu.Unlock()
		c.processOpErr(ErrStaleConnection)
		return
	}
	c.sendPing(nil)
	c.pingIntervalTimer.Reset(c.opts.PingInterval)
	c.mu.Unlock()
}

// 重连
func (c *Client) doReconnect() {
	// We want to make sure we have the other watchers shutdown properly
	// here before we proceed past this point.
	c.waitForExits()

	c.mu.Lock()
	c.err = nil
	waitForGoRoutines := false

	for {

		c.mu.Unlock()

		var jitter = c.opts.ReconnectJitter
		var st = c.opts.ReconnectWait
		if jitter > 0 {
			st += time.Duration(rand.Int63n(int64(jitter)))
		}
		var rt = time.NewTimer(st)
		select {
		case <-c.reconnQuitCh:
			rt.Stop()
		case <-rt.C:
		}

		if waitForGoRoutines {
			c.waitForExits()
			waitForGoRoutines = false
		}
		c.mu.Lock()

		// Check if we have been closed first.
		if c.isClosed() {
			break
		}
		// Try to create a new connection
		_, err := c.createConn()
		if err != nil {
			c.err = nil
			continue
		}
		c.Reconnects.Inc()

		// Process connect logic
		if c.err = c.processConnectInit(); c.err != nil {
			// Check if we should abort reconnect. If so, break out
			// of the loop and connection will be closed.
			if c.abortReconnect {
				break
			}
			c.status = RECONNECTING
			continue
		}
		c.err = c.flushReconnectPendingItems()
		if c.err != nil {
			c.status = RECONNECTING
			// Stop the ping timer (if set)
			c.stopPingTimer()
			// Since processConnectInit() returned without error, the
			// go routines were started, so wait for them to return
			// on the next iteration (after releasing the lock).
			waitForGoRoutines = true
			continue
		}
		// Done with the pending buffer
		c.writer.doneWithPending()

		// This is where we are truly connected.
		c.status = CONNECTED

		c.mu.Unlock()

		// Make sure to flush everything
		c.Flush()

		return
	}
	if c.err == nil {
		c.err = ErrNoServers
	}
	c.mu.Unlock()
	c.close(CLOSED, nil)

}

// Flush will perform a round trip to the server and return when it
// receives the internal reply.
func (c *Client) Flush() error {
	return c.FlushTimeout(10 * time.Second)
}

// FlushTimeout allows a Flush operation to have an associated timeout.
func (c *Client) FlushTimeout(timeout time.Duration) (err error) {

	if timeout <= 0 {
		return ErrBadTimeout
	}
	c.mu.Lock()
	if c.isClosed() {
		c.mu.Unlock()
		return ErrConnectionClosed
	}
	t := globalTimerPool.Get(timeout)
	defer globalTimerPool.Put(t)

	// Create a buffered channel to prevent chan send to block
	// in processPong() if this code here times out just when
	// PONG was received.
	ch := make(chan struct{}, 1)
	c.sendPing(ch) // 注意这里flush是发送ping请求 因为sendPing执行了writer.flush 同时发送ping请求如果有pong响应则说明消息一定发送成功了
	c.mu.Unlock()

	select {
	case _, ok := <-ch:
		if !ok {
			err = ErrConnectionClosed
		} else {
			close(ch)
		}
	case <-t.C:
		err = ErrTimeout
	}

	if err != nil {
		c.removeFlushEntry(ch)
	}
	return

}

// FIXME: This is a hack
// removeFlushEntry is needed when we need to discard queued up responses
// for our pings as part of a flush call. This happens when we have a flush
// call outstanding and we call close.
func (c *Client) removeFlushEntry(ch chan struct{}) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pongs == nil {
		return false
	}
	for i, cp := range c.pongs {
		if cp == ch {
			c.pongs[i] = nil
			return true
		}
	}
	return false
}

func (c *Client) flushReconnectPendingItems() error {
	return c.writer.flushPendingBuffer()
}

func (c *Client) waitForExits() {
	// Kick old flusher forcefully.
	select {
	case c.flusherExitChan <- struct{}{}:
	default:
	}

	// Wait for any previous go routines.
	c.wg.Wait()
}

func (c *Client) processOpErr(err error) {
	c.mu.Lock()
	if c.isConnecting() || c.isClosed() || c.isReconnecting() {
		c.mu.Unlock()
		return
	}

	if c.opts.AutoReconn && c.status == CONNECTED {
		// Set our new status
		c.status = RECONNECTING
		c.stopPingTimer()
		if c.conn != nil {
			c.conn.Close()
			c.conn = nil
		}

		// Create pending buffer before reconnecting.
		c.writer.switchToPending()
		go c.doReconnect()
		c.mu.Unlock()
		return
	}
	c.status = DISCONNECTED
	c.err = err
	c.mu.Unlock()
	c.close(CLOSED, nil)
}

func (c *Client) stopPingTimer() {
	if c.pingIntervalTimer != nil {
		c.pingIntervalTimer.Stop()
	}
}

func (c *Client) sendPing(ch chan struct{}) error {

	c.pongs = append(c.pongs, ch)
	data, err := c.proto.EncodeFrame(&wkproto.PingPacket{}, c.opts.ProtoVersion)
	if err != nil {
		return err
	}
	c.writer.appendBufs(data)
	c.writer.flush()

	return nil
}

func (c *Client) readLoop() {
	defer c.wg.Done()
	// Create a parseState if needed.
	c.mu.Lock()
	conn := c.conn
	br := c.reader
	c.mu.Unlock()

	if conn == nil {
		return
	}
	var remainingData []byte
	var packetData []byte
	tmpBuff := make([]byte, 0)
	for {
		buf, err := br.Read()
		if err == nil {
			// With websocket, it is possible that there is no error but
			// also no buffer returned (either WS control message or read of a
			// partial compressed message). We could call parse(buf) which
			// would ignore an empty buffer, but simply go back to top of the loop.
			if len(buf) == 0 {
				continue
			}
			tmpBuff = append(tmpBuff, buf...)
			packetData, remainingData, err = parse(tmpBuff)
			if remainingData == nil {
				tmpBuff = make([]byte, 0)
			} else {
				tmpBuff = remainingData
			}
			if len(packetData) > 0 {
				err = c.handlePacketDatas(packetData)
			}

		}
		if err != nil {
			c.processOpErr(err)
			break
		}

	}
}

func (c *Client) handlePacketDatas(packetData []byte) error {
	packets := make([]wkproto.Frame, 0)
	offset := 0
	var err error
	for len(packetData) > offset {
		packet, size, err := c.proto.DecodeFrame(packetData[offset:], c.opts.ProtoVersion)
		if err != nil { //
			return err
		}
		packets = append(packets, packet)
		offset += size
	}

	if len(packets) > 0 {
		for _, packet := range packets {
			err = c.handlePacket(packet)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Client) handlePacket(frame wkproto.Frame) error {

	c.InBytes.Add(uint64(frame.GetFrameSize()))
	c.InMsgs.Inc()

	switch frame.GetFrameType() {
	case wkproto.SENDACK: // 发送回执
		c.handleSendackPacket(frame.(*wkproto.SendackPacket))
	case wkproto.RECV: // 收到消息
		c.handleRecvPacket(frame.(*wkproto.RecvPacket))
	case wkproto.PONG: // pong
		c.handlePong()
	}
	return nil
}

func (c *Client) handleSendackPacket(packet *wkproto.SendackPacket) {
	if c.onSendack != nil {
		c.onSendack(packet)
	}
}

// 处理接受包
func (c *Client) handleRecvPacket(packet *wkproto.RecvPacket) {
	var err error
	var payload []byte
	if c.onRecv != nil {
		if !packet.Setting.IsSet(wkproto.SettingNoEncrypt) {
			payload, err = wkutil.AesDecryptPkcs7Base64(packet.Payload, []byte(c.aesKey), []byte(c.salt))
			if err != nil {
				panic(err)
			}
			packet.Payload = payload
		}
		err = c.onRecv(packet)
	}
	if err == nil {
		c.sendPacket(&wkproto.RecvackPacket{
			Framer:     packet.Framer,
			MessageID:  packet.MessageID,
			MessageSeq: packet.MessageSeq,
		})
	}
}

func (c *Client) handlePong() {
	var ch chan struct{}
	c.mu.Lock()
	if len(c.pongs) > 0 {
		ch = c.pongs[0]
		c.pongs = append(c.pongs[:0], c.pongs[1:]...)
	}
	c.pingOut = 0
	c.mu.Unlock()
	if ch != nil {
		ch <- struct{}{}
	}
}

func (c *Client) SendMessage(channel *Channel, payload []byte, opt ...SendOption) error {
	opts := NewSendOptions()
	if len(opt) > 0 {
		for _, op := range opt {
			op(opts)
		}
	}
	var err error
	var setting wkproto.Setting
	newPayload := payload
	if !opts.NoEncrypt {
		// 加密消息内容
		newPayload, err = wkutil.AesEncryptPkcs7Base64(payload, []byte(c.aesKey), []byte(c.salt))
		if err != nil {
			c.Error("加密消息payload失败！", zap.Error(err))
			return err
		}
	} else {
		setting.Set(wkproto.SettingNoEncrypt)
	}

	clientMsgNo := opts.ClientMsgNo
	if clientMsgNo == "" {
		clientMsgNo = wkutil.GenUUID() // TODO: uuid生成非常耗性能
	}
	clientSeq := c.clientIDGen.Add(1)
	packet := &wkproto.SendPacket{
		Framer: wkproto.Framer{
			NoPersist: opts.NoPersist,
			SyncOnce:  opts.SyncOnce,
			RedDot:    opts.RedDot,
		},
		Setting:     setting,
		ClientSeq:   clientSeq,
		ClientMsgNo: clientMsgNo,
		ChannelID:   channel.ChannelID,
		ChannelType: channel.ChannelType,
		Payload:     newPayload,
	}
	packet.RedDot = true

	// 加密消息通道
	if !opts.NoEncrypt {
		signStr := packet.VerityString()
		actMsgKey, err := wkutil.AesEncryptPkcs7Base64([]byte(signStr), []byte(c.aesKey), []byte(c.salt))
		if err != nil {
			c.Error("加密数据失败！", zap.Error(err))
			return err
		}
		packet.MsgKey = wkutil.MD5(string(actMsgKey))
	}
	return c.appendPacket(packet)
}
func (c *Client) Close() {
	c.close(CLOSED, nil)
}
func (c *Client) flusher() {
	defer c.wg.Done()

	c.mu.Lock()
	bw := c.writer
	conn := c.conn
	fch := c.flusherExitChan
	c.mu.Unlock()

	if conn == nil || bw == nil {
		return
	}
	for {
		if _, ok := <-fch; !ok {
			return
		}
		c.mu.Lock()

		// Check to see if we should bail out.
		if !c.isConnected() || c.isConnecting() || conn != c.conn {
			c.mu.Unlock()
			return
		}
		if bw.buffered() > 0 {
			if err := bw.flush(); err != nil {
				if c.err == nil {
					c.err = err
				}
			}
		}
		c.mu.Unlock()
	}
}

func (c *Client) sendConnect() error {
	var clientPubKey [32]byte
	c.clientPrivKey, clientPubKey = wkutil.GetCurve25519KeypPair() // 生成服务器的DH密钥对
	packet := &wkproto.ConnectPacket{
		Version:         c.opts.ProtoVersion,
		DeviceID:        wkutil.GenUUID(),
		DeviceFlag:      wkproto.APP,
		ClientKey:       base64.StdEncoding.EncodeToString(clientPubKey[:]),
		ClientTimestamp: time.Now().Unix(),
		UID:             c.opts.UID,
		Token:           c.opts.Token,
	}
	err := c.sendPacket(packet)
	if err != nil {
		return err
	}
	f, err := c.proto.DecodePacketWithConn(c.conn, c.opts.ProtoVersion)
	if err != nil {
		return err
	}
	connack, ok := f.(*wkproto.ConnackPacket)
	if !ok {
		return errors.New("返回包类型有误！不是连接回执包！")
	}
	if connack.ReasonCode != wkproto.ReasonSuccess {
		return errors.New("连接失败！")
	}
	c.salt = connack.Salt

	serverKey, err := base64.StdEncoding.DecodeString(connack.ServerKey)
	if err != nil {
		return err
	}
	var serverPubKey [32]byte
	copy(serverPubKey[:], serverKey[:32])

	shareKey := wkutil.GetCurve25519Key(c.clientPrivKey, serverPubKey) // 共享key
	c.aesKey = wkutil.MD5(base64.StdEncoding.EncodeToString(shareKey[:]))[:16]

	return nil
}

// 发送包
func (c *Client) sendPacket(packet wkproto.Frame) error {
	data, err := c.proto.EncodeFrame(packet, c.opts.ProtoVersion)
	if err != nil {
		return err
	}
	c.OutBytes.Add(uint64(len(data)))
	c.OutMsgs.Inc()
	err = c.writer.writeDirect(data)
	if err != nil {
		return err
	}
	return nil
}

func (c *Client) appendPacket(packet wkproto.Frame) error {

	data, err := c.proto.EncodeFrame(packet, c.opts.ProtoVersion)
	if err != nil {
		return err
	}
	c.OutBytes.Add(uint64(len(data)))
	c.OutMsgs.Inc()

	if err := c.writer.appendBufs(data); err != nil {
		return err
	}

	if len(c.flusherExitChan) == 0 {
		c.kickFlusher()
	}
	return nil
}

func (c *Client) createConn() (net.Conn, error) {
	network, address, _ := parseAddr(c.addr)
	conn, err := net.DialTimeout(network, address, c.opts.Timeout)
	if err != nil {
		return nil, err
	}
	c.conn = conn
	c.bindToNewConn(conn)
	return conn, nil
}

func (c *Client) newWriter(conn net.Conn) io.Writer {
	var w io.Writer = conn
	if c.opts.FlusherTimeout > 0 {
		w = &timeoutWriter{conn: conn, timeout: c.opts.FlusherTimeout}
	}
	return w
}
func (c *Client) bindToNewConn(conn net.Conn) {
	bw := c.writer
	bw.w, bw.bufs = c.newWriter(conn), nil
	br := c.reader
	br.r, br.n, br.off = conn, 0, -1
}

// Test if Conn is connected or connecting.
func (c *Client) isConnected() bool {
	return c.status == CONNECTED
}

// Test if Conn is in the process of connecting
func (c *Client) isConnecting() bool {
	return c.status == CONNECTING
}

// Test if Conn is being reconnected.
func (c *Client) isReconnecting() bool {
	return c.status == RECONNECTING
}

// Channel Channel
type Channel struct {
	ChannelID   string
	ChannelType uint8
}

// NewChannel 创建频道
func NewChannel(channelID string, channelType uint8) *Channel {
	return &Channel{
		ChannelID:   channelID,
		ChannelType: channelType,
	}
}

func parseAddr(addr string) (network, address string, port int) {
	network = "tcp"
	address = strings.ToLower(addr)
	if strings.Contains(address, "://") {
		pair := strings.Split(address, "://")
		network = pair[0]
		address = pair[1]
		pair2 := strings.Split(address, ":")
		portStr := pair2[1]
		portInt64, _ := strconv.ParseInt(portStr, 10, 64)
		port = int(portInt64)
	}
	return
}
