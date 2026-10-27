package sendtrace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"time"
)

// AttemptKey selects an anonymous wire attempt, not a logical retry identity.
// A repeated CONNECT tuple or duplicate packet does not establish uniqueness.
type AttemptKey struct {
	MessageFingerprint [16]byte
	ClientSeq          uint32
	ConnectFingerprint [16]byte
}

func (k AttemptKey) Valid() bool {
	return k.ClientSeq != 0 && k.MessageFingerprint != [16]byte{} && k.ConnectFingerprint != [16]byte{}
}

// MessageFingerprint preserves the benchmark's existing logical-message digest.
func MessageFingerprint(clientMsgNo string) [16]byte {
	h := sha256.New()
	_, _ = h.Write([]byte("wukongim/chat-lifecycle/message-identity/v1"))
	_, _ = h.Write([]byte(clientMsgNo))
	var result [16]byte
	copy(result[:], h.Sum(nil))
	return result
}

type attemptKeyJSON struct {
	MessageFingerprint string `json:"message_fingerprint"`
	ClientSeq          uint32 `json:"client_seq"`
	ConnectFingerprint string `json:"connect_packet_fingerprint"`
}

func (k AttemptKey) MarshalJSON() ([]byte, error) {
	return json.Marshal(attemptKeyJSON{hex.EncodeToString(k.MessageFingerprint[:]), k.ClientSeq, hex.EncodeToString(k.ConnectFingerprint[:])})
}
func (k *AttemptKey) UnmarshalJSON(data []byte) error {
	var wire attemptKeyJSON
	if err := decodeAttemptJSON(data, &wire); err != nil {
		return err
	}
	message, ok := DecodeFingerprint(wire.MessageFingerprint)
	if !ok {
		return errors.New("invalid anonymous attempt key")
	}
	connect, ok := DecodeFingerprint(wire.ConnectFingerprint)
	if !ok {
		return errors.New("invalid anonymous attempt key")
	}
	if wire.ClientSeq == 0 {
		return errors.New("invalid anonymous attempt key")
	}
	*k = AttemptKey{MessageFingerprint: message, ClientSeq: wire.ClientSeq, ConnectFingerprint: connect}
	return nil
}

// DecodeFingerprint accepts exactly the lowercase hexadecimal fixed16 codec.
func DecodeFingerprint(raw string) ([16]byte, bool) {
	var out [16]byte
	if len(raw) != 32 {
		return out, false
	}
	for _, c := range raw {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return out, false
		}
	}
	_, err := hex.Decode(out[:], []byte(raw))
	return out, err == nil
}

// AttemptToken names one actual admitted Gateway frame in one process epoch.
// It is observation-only and must never enter the business protocol.
type AttemptToken struct {
	Epoch   [16]byte
	Ordinal uint64
}

func (t AttemptToken) Valid() bool { return t.Epoch != [16]byte{} && t.Ordinal != 0 }
func (t AttemptToken) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Epoch   string `json:"epoch"`
		Ordinal uint64 `json:"ordinal"`
	}{hex.EncodeToString(t.Epoch[:]), t.Ordinal})
}
func (t *AttemptToken) UnmarshalJSON(data []byte) error {
	var wire struct {
		Epoch   string `json:"epoch"`
		Ordinal uint64 `json:"ordinal"`
	}
	if err := decodeAttemptJSON(data, &wire); err != nil {
		return err
	}
	epoch, ok := DecodeFingerprint(wire.Epoch)
	if !ok || wire.Ordinal == 0 {
		return errors.New("invalid anonymous invocation")
	}
	*t = AttemptToken{epoch, wire.Ordinal}
	return nil
}
func decodeAttemptJSON(data []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("invalid anonymous diagnostic JSON")
	}
	return nil
}

// AttemptPhase borrows an existing same-clock stage measurement. Gateway
// phases label start+duration with EndDerived, never flush or peer receipt.
// Durable is a duration-only retry-segment fact with zero StartedAt and no end.
type AttemptPhase struct {
	Observed bool  `json:"observed"`
	Stage    Stage `json:"stage"`
	// StartedAt is a same-process projection anchored at the retained ingress
	// wall time; it is not an independent wall-clock read after a clock step.
	// It is always zero for the duration-only message.send_durable stage.
	StartedAt  time.Time     `json:"started_at"`
	Duration   time.Duration `json:"duration"`
	Result     Result        `json:"result"`
	Boundary   string        `json:"boundary"`
	EndDerived bool          `json:"end_derived"`
}

// AttemptObserver is an independently enabled, bounded diagnostic capability.
// Generic tracing must not allocate its storage or opt a caller into it.
type AttemptObserver interface {
	AttemptLinkEnabled() bool
	BeginSendAttempt(AttemptKey, time.Time) AttemptToken
	RecordAttemptPhase(AttemptToken, AttemptPhase)
	BindAttemptTrace(AttemptToken, string)
	RecordAttemptRPC(AttemptToken, RPCReference)
}

func activeAttemptObserver() AttemptObserver {
	holder := activeSink.Load()
	if holder == nil || holder.sink == nil {
		return nil
	}
	observer, ok := holder.sink.(AttemptObserver)
	if !ok || !observer.AttemptLinkEnabled() {
		return nil
	}
	return observer
}

// GatewayAttemptEnabled checks the independent capability without allocating.
func GatewayAttemptEnabled() bool { return activeAttemptObserver() != nil }

// BeginGatewayAttempt reserves one anonymous frame using its existing ingress clock.
func BeginGatewayAttempt(key AttemptKey, at time.Time) AttemptToken {
	if !key.Valid() {
		return AttemptToken{}
	}
	if observer := activeAttemptObserver(); observer != nil {
		return observer.BeginSendAttempt(key, at)
	}
	return AttemptToken{}
}

// RecordGatewayAttemptPhase forwards an already measured fact for one exact token.
func RecordGatewayAttemptPhase(token AttemptToken, phase AttemptPhase) {
	if !token.Valid() {
		return
	}
	if observer := activeAttemptObserver(); observer != nil {
		observer.RecordAttemptPhase(token, phase)
	}
}

// BindGatewayAttemptTrace binds only the original invocation, never a shared parent.
func BindGatewayAttemptTrace(token AttemptToken, trace string) {
	if !token.Valid() {
		return
	}
	if observer := activeAttemptObserver(); observer != nil {
		observer.BindAttemptTrace(token, trace)
	}
}

type gatewayAttemptContextKey struct{}

// WithGatewayAttempt carries only an observation token across one Gateway item.
func WithGatewayAttempt(ctx context.Context, token AttemptToken) context.Context {
	if !token.Valid() {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, gatewayAttemptContextKey{}, token)
}

// GatewayAttemptFromContext returns the original item token or an invalid zero value.
func GatewayAttemptFromContext(ctx context.Context) AttemptToken {
	if ctx == nil {
		return AttemptToken{}
	}
	token, _ := ctx.Value(gatewayAttemptContextKey{}).(AttemptToken)
	return token
}

// DiscardGatewayAttempt forgets a reserved descriptor rejected by its mailbox.
func DiscardGatewayAttempt(token AttemptToken) {
	if !token.Valid() {
		return
	}
	if observer := activeAttemptObserver(); observer != nil {
		if discard, ok := observer.(interface{ DiscardSendAttempt(AttemptToken) }); ok {
			discard.DiscardSendAttempt(token)
		}
	}
}
