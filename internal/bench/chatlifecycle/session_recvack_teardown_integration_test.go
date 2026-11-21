//go:build integration

package chatlifecycle

import (
	"context"
	"testing"
	"testing/synctest"

	wkclient "github.com/WuKongIM/WuKongIM/pkg/client"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestEngineSessionTeardownFinishesAdmittedRecvAck(t *testing.T) {
	for _, test := range []struct {
		name     string
		stop     bool
		writeErr error
		wantCode FailureCode
	}{
		{name: "expiry"},
		{name: "generation stop", stop: true},
		{name: "independent write deadline during expiry", writeErr: context.DeadlineExceeded, wantCode: FailureCodeRecvackDeadline},
		{name: "independent transport cancellation during expiry", writeErr: context.Canceled, wantCode: FailureCodeRecvackCanceled},
		{name: "disconnected transport during expiry", writeErr: wkclient.ErrClosed, wantCode: FailureCodeRecvackUnclassified},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				client := &pausedRecvAckClient{
					engineFakeClient: engineFakeClient{
						factory: &engineFakeFactory{}, frames: make(chan frame.Frame, 2),
						stop: make(chan struct{}), readExited: make(chan struct{}),
					},
					entered: make(chan uint64, 2), release: make(chan struct{}),
					writeErr: test.writeErr,
				}
				fixture := newEngineTestFixture(t, engineTestLimits{
					OnlineUsers: 1, ClientFactory: fixedSessionClientFactory{client: client},
				})
				ctx := context.Background()
				if err := fixture.engine.Start(ctx); err != nil {
					t.Fatal(err)
				}
				defer func() {
					select {
					case <-client.release:
					default:
						close(client.release)
					}
					if err := fixture.engine.Stop(); err != nil {
						t.Errorf("Stop: %v", err)
					}
				}()
				uid := fixture.identity.UID(0)
				session, err := fixture.engine.Login(ctx, SessionLogin{UID: uid})
				if err != nil {
					t.Fatal(err)
				}
				enqueueRecv := func(seq uint64) {
					intent := fixture.intent(t, fixture.identity.UID(2), "group-recvack", seq, TrafficGroup)
					client.frames <- &frame.RecvPacket{
						MessageID: int64(seq), MessageSeq: seq, ClientMsgNo: intent.Packet.ClientMsgNo,
						FromUID: intent.Logical.Sender, ChannelID: intent.ChannelID,
						ChannelType: frame.ChannelTypeGroup, Payload: intent.Packet.Payload,
					}
				}
				enqueueRecv(1)
				if got := <-client.entered; got != 1 {
					t.Fatalf("first receive acknowledgement = %d, want 1", got)
				}

				stopped := make(chan error, 1)
				if test.stop {
					enqueueRecv(2)
					go func() { stopped <- fixture.engine.Stop() }()
				} else if _, err := fixture.engine.AdvanceContext(ctx, session.Deadline); err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				if !test.stop && fixture.pool.IsOnline(uid) {
					t.Error("expired session is still routable during its in-flight acknowledgement")
				}
				select {
				case <-client.stop:
					t.Error("teardown closed the transport before the admitted RECVACK returned")
				default:
				}

				close(client.release)
				synctest.Wait()
				if test.stop {
					if err := <-stopped; err != nil {
						t.Fatal(err)
					}
				}
				verification := fixture.verifier.Snapshot()
				evidence := fixture.verifier.EvidenceSnapshot()
				if verification.Received != 1 || fixture.pool.Counts() != (SessionCounts{}) {
					t.Errorf("teardown received %d messages, sessions %+v, want one admitted message and no remaining session", verification.Received, fixture.pool.Counts())
				}
				var firstCode FailureCode
				if len(evidence.Classes) == 1 && len(evidence.Classes[0].First) == 1 {
					firstCode = evidence.Classes[0].First[0].Code
				}
				if test.wantCode == 0 {
					if verification.ReceiveAcknowledged != 1 || verification.ReceiveAckFailures != 0 || len(evidence.Classes) != 0 {
						t.Fatalf("teardown acknowledgements = %d, failures = %d, classification = %q, first code = %d, want one success and no failures",
							verification.ReceiveAcknowledged, verification.ReceiveAckFailures, evidence.Classification, firstCode)
					}
					return
				}
				if verification.ReceiveAcknowledged != 0 || verification.ReceiveAckFailures != 1 ||
					evidence.Classification != SyncClassificationHarnessInvalid || len(evidence.Classes) != 1 ||
					evidence.Classes[0].Count != 1 || len(evidence.Classes[0].First) != 1 ||
					evidence.Classes[0].First[0].Stage != EvidenceStageRecvack || firstCode != test.wantCode {
					t.Fatalf("write failure was not preserved as RECVACK code %d: failures = %d, classification = %q, first code = %d",
						test.wantCode, verification.ReceiveAckFailures, evidence.Classification, firstCode)
				}
			})
		})
	}
}

// pausedRecvAckClient pauses an already admitted write and then reports the
// same cancellation or disconnected-transport outcomes as the WKProto client.
type pausedRecvAckClient struct {
	engineFakeClient
	entered  chan uint64
	release  chan struct{}
	writeErr error
}

func (c *pausedRecvAckClient) AckRecv(ctx context.Context, ack *frame.RecvackPacket) error {
	c.entered <- ack.MessageSeq
	<-c.release
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-c.stop:
		return wkclient.ErrClosed
	default:
		return c.writeErr
	}
}
