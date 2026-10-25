//go:build integration

package conn

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
	"github.com/WuKongIM/WuKongIM/pkg/transport/wire"
)

type completionRaceTrial struct {
	Trial                        int              `json:"trial"`
	SetupValid                   bool             `json:"setup_valid"`
	DeadlineOffsetNS             time.Duration    `json:"deadline_offset_ns"`
	PublicationWitnessOffsetNS   time.Duration    `json:"publication_witness_offset_ns"`
	PublicationBeforeDeadline    bool             `json:"publication_before_deadline"`
	ContextDoneBeforeRelease     bool             `json:"context_done_before_release"`
	ReleaseOffsetNS              time.Duration    `json:"release_offset_ns"`
	Winner                       string           `json:"winner"`
	SampleResult                 rpctrace.Result  `json:"sample_result"`
	SampleDurationNS             time.Duration    `json:"sample_duration_ns"`
	ReceivePhaseCount            int              `json:"receive_phase_count"`
	ReceivePhaseDurationsNS      [3]time.Duration `json:"receive_phase_durations_ns"`
	PendingAfterCall             int              `json:"pending_after_call"`
	PayloadReleases              int32            `json:"payload_releases"`
	RecorderActiveAfterFinish    int64            `json:"recorder_active_after_finish"`
	CallerJoined                 bool             `json:"caller_joined"`
	ConnectionReaderWriterJoined bool             `json:"connection_reader_writer_joined"`
}

// This existing internal transport seam makes timely publication and a paused
// caller deterministic. Select's choice between two ready cases remains random;
// the fixed trial budget is never retried to force a desired failure count.
func TestProposedTimelyRPCCompletionContractWithDelayedCaller(t *testing.T) {
	const trials = 32
	var replies, timeouts, invalid, other int
	for trial := 1; trial <= trials; trial++ {
		row := runRPCCompletionRaceTrial(t, trial)
		data, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("RPC_COMPLETION_TRIAL %s", data)
		if !row.SetupValid {
			invalid++
			continue
		}
		switch row.Winner {
		case "reply":
			replies++
		case "deadline":
			timeouts++
		default:
			other++
		}
		if row.PendingAfterCall != 0 || row.PayloadReleases != 1 || row.RecorderActiveAfterFinish != 0 || !row.CallerJoined || !row.ConnectionReaderWriterJoined {
			t.Errorf("trial %d did not join its original owners", trial)
		}
		if row.Winner == "reply" && row.ReceivePhaseCount != 3 || row.Winner == "deadline" && row.ReceivePhaseCount != 0 {
			t.Errorf("trial %d receive phases disagree with the actual receive branch", trial)
		}
	}
	t.Logf("RPC_COMPLETION_COUNTS trials=%d replies=%d deadlines=%d invalid=%d other=%d", trials, replies, timeouts, invalid, other)
	if invalid != 0 || other != 0 {
		t.Errorf("diagnostic setup/other errors: invalid=%d other=%d; not deadline-race proof", invalid, other)
	}
	if timeouts != 0 {
		t.Errorf("%d deadline-eligible completed response publications became timeouts after caller scheduling delay", timeouts)
	}
}

func runRPCCompletionRaceTrial(t *testing.T, trial int) completionRaceTrial {
	t.Helper()
	admission, release, notified := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce, notifyOnce sync.Once
	releaseCaller := func() { releaseOnce.Do(func() { close(release) }) }
	var publicationWitness time.Time
	observer := receiveBridgeObserver(func(event core.Event) {
		if event.Name == "scheduler_admission" && event.Result == "ok" {
			close(admission)
			<-release
		}
		// handleRPCResponse emits this event after the candidate commit and index removal.
		if event.Name == "pending_rpc" && event.Inflight == 0 {
			notifyOnce.Do(func() {
				publicationWitness = time.Now()
				close(notified)
			})
		}
	})
	raw, peer := net.Pipe()
	connection := New(raw, receiveBridgeConfig(true, observer), nil)
	connection.AcceptRPCTrace([16]byte{9})
	connection.Start()
	trace, recorder := receiveBridgeTrace(t)
	origin := time.Now()
	deadline := origin.Add(500 * time.Millisecond)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer func() {
		releaseCaller()
		cancel()
		peer.Close()
		connection.Close(nil)
		trace.Finish(rpctrace.ResultError)
	}()
	var released atomic.Int32
	result, callerJoined := make(chan receiveBridgeResult, 1), make(chan struct{})
	go func() {
		defer close(callerJoined)
		body, err := connection.Call(rpctrace.With(ctx, trace), Outbound{Priority: core.PriorityRPC, ServiceID: 7,
			Payload: core.NewOwnedBuffer([]byte("request"), func([]byte) { released.Add(1) })})
		result <- receiveBridgeResult{body: body, err: err}
	}()
	waitClosed(t, admission)
	header := receiveBridgeRequest(t, peer)
	receiveBridgeReply(t, peer, header, 7, []byte{wire.ResponseOK, 'o', 'k'})
	waitClosed(t, notified)
	beforeDeadline := publicationWitness.Before(deadline)
	<-ctx.Done()
	releasedAt := time.Now()
	releaseCaller()
	got := receiveBridgeAwait(t, result)
	waitClosed(t, callerJoined)
	sample := trace.Finish(core.RPCTraceResult(got.err))
	row := completionRaceTrial{Trial: trial, SetupValid: beforeDeadline, DeadlineOffsetNS: deadline.Sub(origin),
		PublicationWitnessOffsetNS: publicationWitness.Sub(origin), PublicationBeforeDeadline: beforeDeadline,
		ContextDoneBeforeRelease: ctx.Err() != nil, ReleaseOffsetNS: releasedAt.Sub(origin),
		SampleResult: sample.Result, SampleDurationNS: sample.Duration, PendingAfterCall: connection.pending.Len(),
		RecorderActiveAfterFinish: recorder.Active(), CallerJoined: true}
	switch {
	case got.err == nil && string(got.body) == "ok":
		row.Winner = "reply"
	case errors.Is(got.err, context.DeadlineExceeded):
		row.Winner = "deadline"
	default:
		row.Winner = "other_error"
	}
	for _, span := range sample.Spans {
		for index, stage := range [3]rpctrace.Stage{rpctrace.StageResponseReceiveObserver, rpctrace.StageResponseDispatch, rpctrace.StageResponseResume} {
			if span.Stage == stage {
				row.ReceivePhaseCount++
				row.ReceivePhaseDurationsNS[index] = span.Duration
			}
		}
	}
	peer.Close()
	connection.Close(nil)
	row.ConnectionReaderWriterJoined = true
	row.PayloadReleases = released.Load()
	return row
}
