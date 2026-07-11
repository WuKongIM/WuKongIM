package workload

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"syscall"

	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
)

// TCPSourceErrorKind classifies worker-local TCP source failures separately
// from remote target failures.
type TCPSourceErrorKind string

const (
	// TCPSourceErrorUnavailable means the configured local address or port cannot be used.
	TCPSourceErrorUnavailable TCPSourceErrorKind = "unavailable"
	// TCPSourceErrorExhausted means every configured source candidate has been consumed.
	TCPSourceErrorExhausted TCPSourceErrorKind = "exhausted"
)

// TCPSourceError reports an explicit worker-local TCP source pool failure.
type TCPSourceError struct {
	// Kind identifies whether the pool was unavailable or exhausted.
	Kind TCPSourceErrorKind
	// LocalAddr is the candidate that produced a local configuration failure, when applicable.
	LocalAddr string
	// Capacity is the total number of unique configured candidates.
	Capacity int
	// Examined is the number of candidates allocated by this shared dialer.
	Examined int
	// Conflicts is the number of candidates skipped because the address was already in use.
	Conflicts int
	// Err is the underlying local bind or permission failure, when applicable.
	Err error
}

// Error implements error.
func (e *TCPSourceError) Error() string {
	if e == nil {
		return "tcp source error"
	}
	switch e.Kind {
	case TCPSourceErrorExhausted:
		return fmt.Sprintf("tcp source pool exhausted: capacity=%d examined=%d conflicts=%d", e.Capacity, e.Examined, e.Conflicts)
	default:
		if e.LocalAddr == "" {
			return fmt.Sprintf("tcp source unavailable: capacity=%d examined=%d conflicts=%d: %v", e.Capacity, e.Examined, e.Conflicts, e.Err)
		}
		return fmt.Sprintf("tcp source unavailable at %s: capacity=%d examined=%d conflicts=%d: %v", e.LocalAddr, e.Capacity, e.Examined, e.Conflicts, e.Err)
	}
}

// Unwrap returns the local bind or permission failure.
func (e *TCPSourceError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// IsTCPSourceError reports whether err represents worker-local source allocation.
func IsTCPSourceError(err error) bool {
	var sourceErr *TCPSourceError
	return errors.As(err, &sourceErr)
}

type tcpDialFunc func(context.Context, *net.Dialer, string, string) (net.Conn, error)

type tcpSourceDialer struct {
	ips       []net.IP
	portMin   int
	capacity  uint64
	dial      tcpDialFunc
	next      atomic.Uint64
	conflicts atomic.Uint64
}

func newTCPSourceDialer(cfg *model.TCPSourceConfig, dial tcpDialFunc) (*tcpSourceDialer, error) {
	if err := model.ValidateTCPSourceConfig(cfg); err != nil {
		return nil, fmt.Errorf("tcp source: %w", err)
	}
	if cfg == nil {
		return nil, nil
	}
	ips := make([]net.IP, 0, len(cfg.IPv4Addrs))
	for _, raw := range cfg.IPv4Addrs {
		ip := net.ParseIP(strings.TrimSpace(raw)).To4()
		if ip == nil {
			return nil, fmt.Errorf("tcp source: invalid IPv4 address %q", raw)
		}
		ips = append(ips, append(net.IP(nil), ip...))
	}
	if dial == nil {
		dial = func(ctx context.Context, dialer *net.Dialer, network, address string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, address)
		}
	}
	return &tcpSourceDialer{
		ips:      ips,
		portMin:  cfg.PortMin,
		capacity: uint64(model.TCPSourceCapacity(cfg)),
		dial:     dial,
	}, nil
}

// DialContext consumes one unique local candidate and dials address. Only
// EADDRINUSE advances to another candidate; remote target errors are preserved.
func (d *tcpSourceDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if d == nil {
		return nil, &TCPSourceError{Kind: TCPSourceErrorExhausted}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		candidate, ok := d.reserveCandidate()
		if !ok {
			return nil, d.exhaustedError()
		}
		ipIndex := candidate % uint64(len(d.ips))
		port := d.portMin + int(candidate/uint64(len(d.ips)))
		localAddr := &net.TCPAddr{IP: append(net.IP(nil), d.ips[ipIndex]...), Port: port}
		dialer := &net.Dialer{LocalAddr: localAddr}
		conn, err := d.dial(ctx, dialer, network, address)
		if err == nil {
			return conn, nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		if errors.Is(err, syscall.EADDRINUSE) {
			d.conflicts.Add(1)
			continue
		}
		if errors.Is(err, syscall.EADDRNOTAVAIL) || errors.Is(err, syscall.EACCES) {
			return nil, &TCPSourceError{
				Kind:      TCPSourceErrorUnavailable,
				LocalAddr: localAddr.String(),
				Capacity:  int(d.capacity),
				Examined:  int(d.next.Load()),
				Conflicts: int(d.conflicts.Load()),
				Err:       err,
			}
		}
		return nil, err
	}
}

func (d *tcpSourceDialer) reserveCandidate() (uint64, bool) {
	for {
		next := d.next.Load()
		if next >= d.capacity {
			return 0, false
		}
		if d.next.CompareAndSwap(next, next+1) {
			return next, true
		}
	}
}

func (d *tcpSourceDialer) exhaustedError() *TCPSourceError {
	return &TCPSourceError{
		Kind:      TCPSourceErrorExhausted,
		Capacity:  int(d.capacity),
		Examined:  int(d.next.Load()),
		Conflicts: int(d.conflicts.Load()),
	}
}
