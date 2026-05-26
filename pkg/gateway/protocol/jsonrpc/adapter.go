package jsonrpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/gateway/protocol"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	pkgjsonrpc "github.com/WuKongIM/WuKongIM/pkg/protocol/jsonrpc"
)

const Name = "jsonrpc"

const replyTokenQueueSessionValue = "gateway.jsonrpc.reply_tokens"

type Adapter struct{}

type replyTokenQueue struct {
	mu     sync.Mutex
	tokens []string
}

var _ protocol.DecodedFrameOwner = (*Adapter)(nil)
var _ protocol.ReplyTokenTracker = (*Adapter)(nil)

func New() *Adapter {
	return &Adapter{}
}

func (a *Adapter) Name() string {
	if a == nil {
		return ""
	}
	return Name
}

// OwnsDecodedFrames reports that JSON-RPC Decode returns frames backed by decoder-owned values.
func (a *Adapter) OwnsDecodedFrames() bool {
	return a != nil
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

func (a *Adapter) OnOpen(sess session.Session) error {
	if sess != nil {
		sess.SetValue(replyTokenQueueSessionValue, &replyTokenQueue{})
	}
	return nil
}

func (a *Adapter) OnClose(sess session.Session) error {
	replyTokenQueueForSession(sess, false).clear()
	return nil
}

func (a *Adapter) TakeReplyTokens(sess session.Session, count int) []string {
	if a == nil || sess == nil || count <= 0 {
		return nil
	}

	return replyTokenQueueForSession(sess, false).take(count)
}

func (a *Adapter) pushReplyToken(sess session.Session, token string) {
	if a == nil || sess == nil || token == "" {
		return
	}

	replyTokenQueueForSession(sess, true).push(token)
}

type valueLoadOrStorer interface {
	LoadOrStoreValue(key string, value any) (actual any, loaded bool)
}

func replyTokenQueueForSession(sess session.Session, create bool) *replyTokenQueue {
	if sess == nil {
		return nil
	}
	if queue, _ := sess.Value(replyTokenQueueSessionValue).(*replyTokenQueue); queue != nil {
		return queue
	}
	if !create {
		return nil
	}

	queue := &replyTokenQueue{}
	if loader, ok := sess.(valueLoadOrStorer); ok {
		actual, _ := loader.LoadOrStoreValue(replyTokenQueueSessionValue, queue)
		if existing, _ := actual.(*replyTokenQueue); existing != nil {
			return existing
		}
	}
	sess.SetValue(replyTokenQueueSessionValue, queue)
	return queue
}

func (q *replyTokenQueue) push(token string) {
	if q == nil || token == "" {
		return
	}

	q.mu.Lock()
	q.tokens = append(q.tokens, token)
	q.mu.Unlock()
}

func (q *replyTokenQueue) take(count int) []string {
	if q == nil || count <= 0 {
		return nil
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.tokens) == 0 {
		return nil
	}
	if count > len(q.tokens) {
		count = len(q.tokens)
	}

	tokens := q.tokens[:count:count]
	if count == len(q.tokens) {
		q.tokens = nil
	} else {
		q.tokens = q.tokens[count:]
	}
	return tokens
}

func (q *replyTokenQueue) clear() {
	if q == nil {
		return
	}

	q.mu.Lock()
	q.tokens = nil
	q.mu.Unlock()
}
