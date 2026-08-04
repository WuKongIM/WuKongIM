package chatlifecycle

import (
	"encoding/binary"
	"errors"
	"math"
	"time"
)

var (
	errRetryIdentityRequired = errors.New("chat lifecycle retry: identity space is required")
	errRetryConfig           = errors.New("chat lifecycle retry: policy must be exactly three retries at 100ms, 500ms, and 2s")
	errRetryLogicalIdentity  = errors.New("chat lifecycle retry: logical send identity is invalid")
	errRetryAttempt          = errors.New("chat lifecycle retry: attempt must be in 0..3")
	errRetryDelayOverflow    = errors.New("chat lifecycle retry: delay overflows time.Duration")
)

var fixedRetryBases = [3]time.Duration{100 * time.Millisecond, 500 * time.Millisecond, 2 * time.Second}

// RetryAttempt is one attempt of an existing logical SEND. Jitter is a
// deterministic nonnegative value in [0, BaseDelay/5].
type RetryAttempt struct {
	Attempt     uint8
	ClientMsgNo string
	BaseDelay   time.Duration
	Jitter      time.Duration
	Delay       time.Duration
}

// RetryPolicy plans only retries for existing LogicalSend values; it has no
// API that can mint a replacement identity on timeout.
type RetryPolicy struct {
	identity *IdentitySpace
	bases    [3]time.Duration
}

// NewRetryPolicy accepts only the reviewed three-retry timing contract.
func NewRetryPolicy(identity *IdentitySpace, config RetryConfig) (RetryPolicy, error) {
	if identity == nil {
		return RetryPolicy{}, errRetryIdentityRequired
	}
	if config.MaxCount != len(fixedRetryBases) || len(config.Delays) != len(fixedRetryBases) {
		return RetryPolicy{}, errRetryConfig
	}
	for index, want := range fixedRetryBases {
		if config.Delays[index] != want {
			return RetryPolicy{}, errRetryConfig
		}
	}
	return RetryPolicy{identity: identity, bases: fixedRetryBases}, nil
}

// Attempt returns attempt zero without delay or one of the three bounded
// retries. Every result reuses logical.ClientMsgNo byte-for-byte.
func (p RetryPolicy) Attempt(logical LogicalSend, attempt uint8) (RetryAttempt, error) {
	if !p.validLogicalSend(logical) {
		return RetryAttempt{}, errRetryLogicalIdentity
	}
	if attempt > uint8(len(p.bases)) {
		return RetryAttempt{}, errRetryAttempt
	}
	result := RetryAttempt{Attempt: attempt, ClientMsgNo: logical.ClientMsgNo}
	if attempt == 0 {
		return result, nil
	}
	result.BaseDelay = p.bases[attempt-1]
	maximumJitter := result.BaseDelay / 5
	messageIdentity := messageFingerprint(logical.ClientMsgNo)
	draw, err := p.identity.decisionBelow(
		"retry-nonnegative-jitter/v1",
		uint64(maximumJitter)+1,
		logical.LogicalSend,
		uint64(logical.WorkerID),
		uint64(logical.Kind),
		uint64(attempt),
		binary.BigEndian.Uint64(messageIdentity[:8]),
	)
	if err != nil {
		return RetryAttempt{}, err
	}
	result.Jitter = time.Duration(draw)
	result.Delay, err = checkedRetryDelay(result.BaseDelay, result.Jitter)
	if err != nil {
		return RetryAttempt{}, err
	}
	return result, nil
}

func (p RetryPolicy) validLogicalSend(logical LogicalSend) bool {
	return uint64(logical.WorkerID) < p.identity.workers && validTrafficKind(logical.Kind) &&
		validMarkerIdentity(logical.Sender) && validMarkerIdentity(logical.Target) &&
		logical.ClientMsgNo != "" && logical.ClientMsgNo == logicalClientMessageNo(p.identity, logical)
}

func checkedRetryDelay(base, jitter time.Duration) (time.Duration, error) {
	if base < 0 || jitter < 0 || base > time.Duration(math.MaxInt64)-jitter {
		return 0, errRetryDelayOverflow
	}
	return base + jitter, nil
}
