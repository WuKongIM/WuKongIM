package testkit

import (
	"sync"

	"github.com/WuKongIM/WuKongIM/internal/gateway/session"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type FakeProtocol struct {
	name string

	mu              sync.Mutex
	DecodedFrames   []frame.Frame
	DecodeConsumed  int
	DecodeErr       error
	EncodedBytes    []byte
	EncodeErr       error
	OnOpenErr       error
	OnCloseErr      error
	DecodeCalls     int
	EncodeCalls     int
	OnOpenCalls     int
	OnCloseCalls    int
	LastDecodeInput []byte
	LastEncodeFrame frame.Frame
	LastEncodeMeta  session.OutboundMeta
}

func NewFakeProtocol(name string) *FakeProtocol {
	return &FakeProtocol{name: name}
}

func (p *FakeProtocol) Name() string {
	if p == nil {
		return ""
	}
	return p.name
}

func (p *FakeProtocol) Decode(_ session.Session, in []byte) ([]frame.Frame, int, error) {
	if p == nil {
		return nil, 0, nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.DecodeCalls++
	p.LastDecodeInput = append([]byte(nil), in...)
	if p.DecodeErr != nil {
		return nil, 0, p.DecodeErr
	}

	frames := append([]frame.Frame(nil), p.DecodedFrames...)
	consumed := p.DecodeConsumed
	if consumed == 0 && len(frames) > 0 {
		consumed = len(in)
	}
	if consumed > len(in) {
		consumed = len(in)
	}
	return frames, consumed, nil
}

func (p *FakeProtocol) Encode(_ session.Session, f frame.Frame, meta session.OutboundMeta) ([]byte, error) {
	if p == nil {
		return nil, nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.EncodeCalls++
	p.LastEncodeFrame = f
	p.LastEncodeMeta = meta
	if p.EncodeErr != nil {
		return nil, p.EncodeErr
	}
	if p.EncodedBytes != nil {
		return append([]byte(nil), p.EncodedBytes...), nil
	}
	return nil, nil
}

func (p *FakeProtocol) OnOpen(_ session.Session) error {
	if p == nil {
		return nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.OnOpenCalls++
	return p.OnOpenErr
}

func (p *FakeProtocol) OnClose(_ session.Session) error {
	if p == nil {
		return nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.OnCloseCalls++
	return p.OnCloseErr
}
