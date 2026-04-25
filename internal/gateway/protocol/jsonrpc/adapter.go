package jsonrpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sync"

	"github.com/WuKongIM/WuKongIM/internal/gateway/protocol"
	"github.com/WuKongIM/WuKongIM/internal/gateway/session"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	pkgjsonrpc "github.com/WuKongIM/WuKongIM/pkg/protocol/jsonrpc"
)

const Name = "jsonrpc"

type Adapter struct {
	mu     sync.Mutex
	tokens map[uint64][]string
}

var _ protocol.ReplyTokenTracker = (*Adapter)(nil)

func New() *Adapter {
	return &Adapter{
		tokens: make(map[uint64][]string),
	}
}

func (a *Adapter) Name() string {
	if a == nil {
		return ""
	}
	return Name
}

func (a *Adapter) Decode(sess session.Session, in []byte) ([]frame.Frame, int, error) {
	if a == nil || len(in) == 0 {
		return nil, 0, nil
	}

	reader := bytes.NewReader(in)
	decoder := json.NewDecoder(reader)
	msg, _, err := pkgjsonrpc.Decode(decoder)
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, 0, nil
		}
		return nil, 0, err
	}

	f, replyToken, err := pkgjsonrpc.ToFrame(msg)
	if err != nil {
		return nil, 0, err
	}
	if replyToken != "" {
		a.pushReplyToken(sess, replyToken)
	}
	return []frame.Frame{f}, int(decoder.InputOffset()), nil
}

func (a *Adapter) Encode(_ session.Session, f frame.Frame, meta session.OutboundMeta) ([]byte, error) {
	if a == nil {
		return nil, nil
	}

	msg, err := pkgjsonrpc.FromFrame(meta.ReplyToken, f)
	if err != nil {
		return nil, err
	}
	return pkgjsonrpc.Encode(msg)
}

func (a *Adapter) OnOpen(session.Session) error {
	return nil
}

func (a *Adapter) OnClose(sess session.Session) error {
	if a == nil || sess == nil {
		return nil
	}

	a.mu.Lock()
	delete(a.tokens, sess.ID())
	a.mu.Unlock()
	return nil
}

func (a *Adapter) TakeReplyTokens(sess session.Session, count int) []string {
	if a == nil || sess == nil || count <= 0 {
		return nil
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	queue := a.tokens[sess.ID()]
	if len(queue) == 0 {
		return nil
	}
	if count > len(queue) {
		count = len(queue)
	}

	tokens := append([]string(nil), queue[:count]...)
	if count == len(queue) {
		delete(a.tokens, sess.ID())
	} else {
		a.tokens[sess.ID()] = append([]string(nil), queue[count:]...)
	}
	return tokens
}

func (a *Adapter) pushReplyToken(sess session.Session, token string) {
	if a == nil || sess == nil || token == "" {
		return
	}

	a.mu.Lock()
	a.tokens[sess.ID()] = append(a.tokens[sess.ID()], token)
	a.mu.Unlock()
}
