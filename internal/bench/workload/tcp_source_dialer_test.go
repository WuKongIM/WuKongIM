package workload

import (
	"context"
	"errors"
	"net"
	"reflect"
	"sync"
	"syscall"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
)

func TestTCPSourceDialerAllocatesIPFastestCandidateOrder(t *testing.T) {
	wantErr := errors.New("target failed")
	var got []string
	dialer, err := newTCPSourceDialer(&model.TCPSourceConfig{
		IPv4Addrs: []string{"127.0.0.1", "192.0.2.1"},
		PortMin:   2000,
		PortMax:   2002,
	}, func(ctx context.Context, dialer *net.Dialer, network, address string) (net.Conn, error) {
		got = append(got, dialer.LocalAddr.String())
		return nil, wantErr
	})
	if err != nil {
		t.Fatalf("newTCPSourceDialer() error = %v", err)
	}

	for i := 0; i < 6; i++ {
		_, dialErr := dialer.DialContext(context.Background(), "tcp", "127.0.0.1:5100")
		if dialErr != wantErr {
			t.Fatalf("DialContext(%d) error = %v, want target error", i, dialErr)
		}
	}

	want := []string{"127.0.0.1:2000", "192.0.2.1:2000", "127.0.0.1:2001", "192.0.2.1:2001", "127.0.0.1:2002", "192.0.2.1:2002"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidate order = %v, want %v", got, want)
	}
}

func TestTCPSourceDialerAllocatesEachCandidateOnceConcurrently(t *testing.T) {
	const candidates = 200
	wantErr := errors.New("target failed")
	seen := make(map[string]struct{}, candidates)
	var mu sync.Mutex
	dialer, err := newTCPSourceDialer(&model.TCPSourceConfig{
		IPv4Addrs: []string{"127.0.0.1", "192.0.2.1"},
		PortMin:   2000,
		PortMax:   2099,
	}, func(ctx context.Context, dialer *net.Dialer, network, address string) (net.Conn, error) {
		local := dialer.LocalAddr.String()
		mu.Lock()
		if _, exists := seen[local]; exists {
			t.Errorf("candidate %s allocated more than once", local)
		}
		seen[local] = struct{}{}
		mu.Unlock()
		return nil, wantErr
	})
	if err != nil {
		t.Fatalf("newTCPSourceDialer() error = %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < candidates; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, dialErr := dialer.DialContext(context.Background(), "tcp", "127.0.0.1:5100")
			if dialErr != wantErr {
				t.Errorf("DialContext() error = %v, want target error", dialErr)
			}
		}()
	}
	wg.Wait()

	if len(seen) != candidates {
		t.Fatalf("unique candidates = %d, want %d", len(seen), candidates)
	}
}

func TestTCPSourceDialerSkipsAddressInUse(t *testing.T) {
	wantErr := errors.New("target failed")
	var got []string
	dialer := mustTCPSourceDialer(t, &model.TCPSourceConfig{IPv4Addrs: []string{"127.0.0.1"}, PortMin: 2000, PortMax: 2001},
		func(ctx context.Context, dialer *net.Dialer, network, address string) (net.Conn, error) {
			got = append(got, dialer.LocalAddr.String())
			if len(got) == 1 {
				return nil, &net.OpError{Op: "dial", Net: network, Err: syscall.EADDRINUSE}
			}
			return nil, wantErr
		})

	_, err := dialer.DialContext(context.Background(), "tcp", "127.0.0.1:5100")

	if err != wantErr {
		t.Fatalf("DialContext() error = %v, want target error", err)
	}
	if want := []string{"127.0.0.1:2000", "127.0.0.1:2001"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("attempted candidates = %v, want %v", got, want)
	}
}

func TestTCPSourceDialerStopsBeforeAllocatingOnCanceledContext(t *testing.T) {
	calls := 0
	dialer := mustTCPSourceDialer(t, &model.TCPSourceConfig{IPv4Addrs: []string{"127.0.0.1"}, PortMin: 2000, PortMax: 2000},
		func(ctx context.Context, dialer *net.Dialer, network, address string) (net.Conn, error) {
			calls++
			return nil, errors.New("target failed")
		})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := dialer.DialContext(ctx, "tcp", "127.0.0.1:5100")

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("DialContext() error = %v, want context.Canceled", err)
	}
	if calls != 0 {
		t.Fatalf("underlying dial calls = %d, want 0", calls)
	}
}

func TestTCPSourceDialerReturnsTypedLocalSourceFailure(t *testing.T) {
	for _, errno := range []syscall.Errno{syscall.EADDRNOTAVAIL, syscall.EACCES} {
		t.Run(errno.Error(), func(t *testing.T) {
			calls := 0
			dialer := mustTCPSourceDialer(t, &model.TCPSourceConfig{IPv4Addrs: []string{"192.0.2.1"}, PortMin: 2000, PortMax: 2001},
				func(ctx context.Context, dialer *net.Dialer, network, address string) (net.Conn, error) {
					calls++
					return nil, &net.OpError{Op: "dial", Net: network, Err: errno}
				})

			_, err := dialer.DialContext(context.Background(), "tcp", "127.0.0.1:5100")

			var sourceErr *TCPSourceError
			if !errors.As(err, &sourceErr) {
				t.Fatalf("DialContext() error = %T %v, want *TCPSourceError", err, err)
			}
			if sourceErr.Kind != TCPSourceErrorUnavailable || !errors.Is(err, errno) {
				t.Fatalf("source error = %#v, want unavailable wrapping %v", sourceErr, errno)
			}
			if calls != 1 {
				t.Fatalf("underlying dial calls = %d, want 1", calls)
			}
		})
	}
}

func TestTCPSourceDialerPreservesTargetErrorsWithoutTryingAnotherCandidate(t *testing.T) {
	timeoutErr := &net.DNSError{Err: "timeout", IsTimeout: true}
	tests := []struct {
		name string
		err  error
	}{
		{name: "refused", err: &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}},
		{name: "timeout", err: timeoutErr},
		{name: "network unreachable", err: &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ENETUNREACH}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			dialer := mustTCPSourceDialer(t, &model.TCPSourceConfig{IPv4Addrs: []string{"127.0.0.1"}, PortMin: 2000, PortMax: 2001},
				func(ctx context.Context, dialer *net.Dialer, network, address string) (net.Conn, error) {
					calls++
					return nil, tt.err
				})

			_, err := dialer.DialContext(context.Background(), "tcp", "127.0.0.1:5100")

			if err != tt.err {
				t.Fatalf("DialContext() error = %T %v, want original %T %v", err, err, tt.err, tt.err)
			}
			if calls != 1 {
				t.Fatalf("underlying dial calls = %d, want 1", calls)
			}
		})
	}
}

func TestTCPSourceDialerExhaustionReportsCapacityExaminedAndConflicts(t *testing.T) {
	dialer := mustTCPSourceDialer(t, &model.TCPSourceConfig{IPv4Addrs: []string{"127.0.0.1"}, PortMin: 2000, PortMax: 2001},
		func(ctx context.Context, dialer *net.Dialer, network, address string) (net.Conn, error) {
			return nil, &net.OpError{Op: "dial", Net: network, Err: syscall.EADDRINUSE}
		})

	_, err := dialer.DialContext(context.Background(), "tcp", "127.0.0.1:5100")

	var sourceErr *TCPSourceError
	if !errors.As(err, &sourceErr) {
		t.Fatalf("DialContext() error = %T %v, want *TCPSourceError", err, err)
	}
	if sourceErr.Kind != TCPSourceErrorExhausted || sourceErr.Capacity != 2 || sourceErr.Examined != 2 || sourceErr.Conflicts != 2 {
		t.Fatalf("source exhaustion = %#v, want capacity=2 examined=2 conflicts=2", sourceErr)
	}
}

func mustTCPSourceDialer(t *testing.T, cfg *model.TCPSourceConfig, dial tcpDialFunc) *tcpSourceDialer {
	t.Helper()
	dialer, err := newTCPSourceDialer(cfg, dial)
	if err != nil {
		t.Fatalf("newTCPSourceDialer() error = %v", err)
	}
	return dialer
}
