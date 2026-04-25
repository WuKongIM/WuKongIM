package gnet

import (
	"encoding/binary"
	"errors"
	"fmt"
	"unicode/utf8"
)

const (
	wsOpcodeContinuation = 0x0
	wsOpcodeText         = 0x1
	wsOpcodeBinary       = 0x2
	wsOpcodeClose        = 0x8
	wsOpcodePing         = 0x9
	wsOpcodePong         = 0xa
)

const (
	wsCloseNormalClosure = 1000
	wsCloseProtocolError = 1002
	wsCloseInvalidData   = 1007
)

var errWSNeedMoreData = errors.New("need more websocket data")

type wsFrame struct {
	final   bool
	opcode  byte
	masked  bool
	maskKey [4]byte
	payload []byte
}

type wsProtocolError struct {
	code uint16
	err  error
}

func (e *wsProtocolError) Error() string {
	if e == nil || e.err == nil {
		return ""
	}
	return e.err.Error()
}

func newWSProtocolError(code uint16, msg string) error {
	return &wsProtocolError{
		code: code,
		err:  fmt.Errorf("gateway/transport/gnet: %s", msg),
	}
}

func wsCloseCodeForErr(err error) uint16 {
	var protocolErr *wsProtocolError
	if errors.As(err, &protocolErr) && protocolErr != nil {
		return protocolErr.code
	}
	return wsCloseProtocolError
}

func decodeWSFrame(buf []byte) (wsFrame, int, error) {
	if len(buf) < 2 {
		return wsFrame{}, 0, errWSNeedMoreData
	}

	first := buf[0]
	if first&0x70 != 0 {
		return wsFrame{}, 0, newWSProtocolError(wsCloseProtocolError, "reserved websocket bits are not supported")
	}

	second := buf[1]
	payloadLen := int(second & 0x7f)
	offset := 2
	switch payloadLen {
	case 126:
		if len(buf) < offset+2 {
			return wsFrame{}, 0, errWSNeedMoreData
		}
		payloadLen = int(binary.BigEndian.Uint16(buf[offset : offset+2]))
		offset += 2
	case 127:
		if len(buf) < offset+8 {
			return wsFrame{}, 0, errWSNeedMoreData
		}
		payloadLen64 := binary.BigEndian.Uint64(buf[offset : offset+8])
		if payloadLen64 > uint64(^uint(0)>>1) {
			return wsFrame{}, 0, newWSProtocolError(wsCloseProtocolError, "websocket frame is too large")
		}
		payloadLen = int(payloadLen64)
		offset += 8
	}

	frame := wsFrame{
		final:  first&0x80 != 0,
		opcode: first & 0x0f,
		masked: second&0x80 != 0,
	}

	if frame.masked {
		if len(buf) < offset+4 {
			return wsFrame{}, 0, errWSNeedMoreData
		}
		copy(frame.maskKey[:], buf[offset:offset+4])
		offset += 4
	}

	if len(buf) < offset+payloadLen {
		return wsFrame{}, 0, errWSNeedMoreData
	}

	frame.payload = append([]byte(nil), buf[offset:offset+payloadLen]...)
	if frame.masked {
		for i := range frame.payload {
			frame.payload[i] ^= frame.maskKey[i%4]
		}
	}

	if isWSControlOpcode(frame.opcode) {
		if !frame.final {
			return wsFrame{}, 0, newWSProtocolError(wsCloseProtocolError, "fragmented websocket control frame")
		}
		if len(frame.payload) > 125 {
			return wsFrame{}, 0, newWSProtocolError(wsCloseProtocolError, "websocket control frame payload is too large")
		}
	}

	return frame, offset + payloadLen, nil
}

func encodeWSFrame(frame wsFrame) ([]byte, error) {
	if isWSControlOpcode(frame.opcode) && len(frame.payload) > 125 {
		return nil, fmt.Errorf("gateway/transport/gnet: websocket control frame payload is too large")
	}

	payloadLen := len(frame.payload)
	headerLen := 2
	switch {
	case payloadLen < 126:
	case payloadLen <= 0xffff:
		headerLen += 2
	default:
		headerLen += 8
	}
	if frame.masked {
		headerLen += 4
	}

	buf := make([]byte, headerLen+payloadLen)
	buf[0] = frame.opcode
	if frame.final {
		buf[0] |= 0x80
	}

	offset := 2
	switch {
	case payloadLen < 126:
		buf[1] = byte(payloadLen)
	case payloadLen <= 0xffff:
		buf[1] = 126
		binary.BigEndian.PutUint16(buf[offset:offset+2], uint16(payloadLen))
		offset += 2
	default:
		buf[1] = 127
		binary.BigEndian.PutUint64(buf[offset:offset+8], uint64(payloadLen))
		offset += 8
	}

	copy(buf[offset:], frame.payload)
	return buf, nil
}

func buildWSCloseFrame(code uint16, text string) []byte {
	payload := make([]byte, 2+len(text))
	binary.BigEndian.PutUint16(payload[:2], code)
	copy(payload[2:], text)

	frame, err := encodeWSFrame(wsFrame{
		final:   true,
		opcode:  wsOpcodeClose,
		payload: payload,
	})
	if err != nil {
		return nil
	}
	return frame
}

func isWSControlOpcode(opcode byte) bool {
	return opcode == wsOpcodeClose || opcode == wsOpcodePing || opcode == wsOpcodePong
}

func validWSClosePayload(payload []byte) error {
	if len(payload) == 0 {
		return nil
	}
	if len(payload) == 1 {
		return newWSProtocolError(wsCloseProtocolError, "invalid websocket close payload")
	}
	if !utf8.Valid(payload[2:]) {
		return newWSProtocolError(wsCloseInvalidData, "invalid websocket close reason")
	}
	return nil
}

func wsPayloadLooksText(payload []byte) bool {
	if !utf8.Valid(payload) {
		return false
	}
	for _, b := range payload {
		if b < 0x20 && b != '\n' && b != '\r' && b != '\t' {
			return false
		}
	}
	return true
}
