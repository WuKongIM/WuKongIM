package server

import "fmt"

type stream struct {
	streamNo string

	msgQueue *channelMsgQueue
	// 空闲时间
	idleTimeTick int

	payloadDecrypting     bool // 是否正在解密
	payloadDecryptingTick int  // 发起解密的tick计时
	s                     *Server

	delivering     bool // 是否正在投递
	deliveringTick int  // 发起投递的tick计时

	forwarding  bool // 是否正在转发
	forwardTick int  // 发起转发的tick计时
}

func newStream(streamNo string, s *Server) *stream {

	return &stream{
		streamNo:              streamNo,
		msgQueue:              newChannelMsgQueue(fmt.Sprintf("stream-%s", streamNo)),
		s:                     s,
		payloadDecryptingTick: s.opts.Reactor.Stream.ProcessIntervalTick,
	}
}

func (s *stream) appendMsg(msg ReactorChannelMessage) {
	s.idleTimeTick = 0
	s.msgQueue.appendMessage(msg)
}

func (s *stream) tick() {
	s.idleTimeTick++
	s.deliveringTick++
	s.payloadDecryptingTick++
	s.forwardTick++

	if s.payloadDecrypting && s.payloadDecryptingTick > s.s.opts.Reactor.Stream.ProcessIntervalTick { // 解密超时, 重新发起
		s.payloadDecrypting = false
	}

	if s.delivering && s.deliveringTick > s.s.opts.Reactor.Stream.ProcessIntervalTick { // 投递超时, 重新发起
		s.delivering = false
	}

	if s.forwarding && s.forwardTick > s.s.opts.Reactor.Stream.ProcessIntervalTick { // 转发超时, 重新发起
		s.forwarding = false
	}
}

// 获取未解密的消息
func (s *stream) payloadUnDecryptMessages() []ReactorChannelMessage {
	s.payloadDecrypting = true
	s.payloadDecryptingTick = 0
	msgs := s.msgQueue.sliceWithSize(s.msgQueue.payloadDecryptingIndex, s.msgQueue.lastIndex, 1024*1024*2)
	return msgs
}

func (s *stream) payloadDecryptFinish(messages []ReactorChannelMessage) {
	s.payloadDecrypting = false
	s.payloadDecryptingTick = s.s.opts.Reactor.Stream.ProcessIntervalTick // 设置为间隔时间，则不需要等待可以继续处理下一批请求

	lastMsg := messages[len(messages)-1]

	startIndex := s.msgQueue.getArrayIndex(s.msgQueue.payloadDecryptingIndex)

	if lastMsg.Index > s.msgQueue.payloadDecryptingIndex {
		s.msgQueue.payloadDecryptingIndex = lastMsg.Index
	}

	endIndex := s.msgQueue.getArrayIndex(s.msgQueue.payloadDecryptingIndex)

	if startIndex >= endIndex {
		return
	}
	msgLen := len(messages)
	for i := startIndex; i < endIndex; i++ {
		msg := s.msgQueue.messages[i]
		for j := 0; j < msgLen; j++ {
			decryptMsg := messages[j]
			if msg.MessageId == decryptMsg.MessageId {
				msg.SendPacket.Payload = decryptMsg.SendPacket.Payload
				msg.IsEncrypt = decryptMsg.IsEncrypt
				s.msgQueue.messages[i] = msg
				break
			}
		}
	}
}

func (s *stream) deliverFinish(messages []ReactorChannelMessage) {
	s.delivering = false
	s.deliveringTick = s.s.opts.Reactor.Stream.ProcessIntervalTick // 设置为间隔时间，则不需要等待可以继续处理下一批请求

	lastMsg := messages[len(messages)-1]
	if lastMsg.Index > s.msgQueue.deliveringIndex {
		s.msgQueue.deliveringIndex = lastMsg.Index
		s.msgQueue.truncateTo(lastMsg.Index)

	}
}

// 获取未投递的消息
func (s *stream) unDeliverMessages() []ReactorChannelMessage {
	s.delivering = true
	s.deliveringTick = 0
	msgs := s.msgQueue.sliceWithSize(s.msgQueue.deliveringIndex, s.msgQueue.payloadDecryptingIndex, 1024*1024*2)
	return msgs
}

// 获取未转发的消息
func (s *stream) unforwardMessages() []ReactorChannelMessage {
	s.forwarding = true
	s.forwardTick = 0
	msgs := s.msgQueue.sliceWithSize(s.msgQueue.forwardingIndex, s.msgQueue.payloadDecryptingIndex, 1024*1024*2)
	return msgs
}

// 是否有未解密的消息
func (s *stream) hasPayloadUnDecrypt() bool {
	if s.payloadDecrypting {
		return false
	}

	return s.msgQueue.lastIndex > s.msgQueue.payloadDecryptingIndex
}

// 有未投递的消息
func (s *stream) hasUnDeliver() bool {
	if s.delivering {
		return false
	}

	return s.msgQueue.payloadDecryptingIndex > s.msgQueue.deliveringIndex
}

// 有未转发的消息
func (s *stream) hasUnforward() bool {
	if s.forwarding {
		return false
	}

	return s.msgQueue.payloadDecryptingIndex > s.msgQueue.forwardingIndex
}
