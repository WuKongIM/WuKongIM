package clusternet

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

func TestTransportBatchWaitZeroDisablesCoalescing(t *testing.T) {
	for _, limits := range []transport.Limits{{}, transport.DefaultLimits()} {
		got := normalizeTransportLimits(limits, 0)
		if got.WriteBatchMaxWait != 0 {
			t.Fatalf("zero wait became %v", got.WriteBatchMaxWait)
		}
	}
	limits := transport.DefaultLimits()
	limits.WriteBatchMaxWait = time.Millisecond
	if got := normalizeTransportLimits(limits, 0); got.WriteBatchMaxWait != time.Millisecond {
		t.Fatalf("explicit wait changed: %v", got.WriteBatchMaxWait)
	}
}

func TestTransportBudgetsPreserveMutationAndBackupPolicies(t *testing.T) {
	s := &TransportServer{}
	for _, tc := range []struct {
		id      uint8
		read    bool
		timeout time.Duration
	}{
		{RPCChannelAuthoritySend, false, 30 * time.Second},
		{RPCChannelCommittedReads, true, 30 * time.Second},
		{RPCOpsMCP, false, time.Minute},
		{RPCScheduledBackupSlot, false, 48 * time.Hour},
		{RPCScheduledBackupMessages, false, 48 * time.Hour},
		{RPCScheduledBackupRestore, false, 48 * time.Hour},
		{RPCScheduledBackupRepositoryProbe, false, 5 * time.Minute},
	} {
		opts := s.serviceOptions(tc.id)
		if opts.QueueTimeout != 5*time.Second || opts.Timeout != tc.timeout || opts.CancelRunning != tc.read {
			t.Fatalf("service %d budget policy = %+v", tc.id, opts)
		}
	}
	s.cfg.Service.Timeout = time.Minute
	if got := s.serviceOptions(RPCScheduledBackupRestore).Timeout; got != time.Minute {
		t.Fatalf("explicit backup timeout changed to %v", got)
	}
}

func TestTransportClientMetricsUseServerServiceAlias(t *testing.T) {
	sink := &recordingTransportObserver{}
	transportClientObserver{next: sink}.ObserveTransport(transport.Event{Name: "client_rpc", ServiceID: uint16(RPCChannelAuthoritySend), Count: 64})
	event := sink.snapshot()[0]
	if event.ServiceAlias != (&TransportServer{}).serviceOptions(RPCChannelAuthoritySend).Alias || event.Count != 64 {
		t.Fatalf("client/server metric labels or aggregate counts differ: %+v", event)
	}
}
