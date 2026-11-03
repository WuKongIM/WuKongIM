//go:build integration

package node

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
)

// Failure inventory: Full owner work and nested committed Get must not acquire
// a fresh service-root budget after caller admission. Temporary Begin/End and
// Page/ReadUIDs follow that caller; exact Abort and durable Ensure/Claim remain
// detached. Unknown headers cannot acquire cancellation authority. These use
// real TCP/transport registration and actual adapter/control backend ports,
// without a product process, database, hidden retry, or synthetic elapsed time.
func TestPresenceGenerationTransportOwnerCallerBudget(t *testing.T) {
	for _, op := range []byte{generationBegin, generationPage, generationEnd, generationReadUIDs} {
		for _, deadline := range []bool{false, true} {
			t.Run(fmt.Sprintf("op-%d/deadline-%t", op, deadline), func(t *testing.T) {
				body := cancellationGenerationBody(t, op)
				port := newGenerationCancellationPort()
				adapter := New(Options{GenerationOwner: port})
				handler := generationCancellationHandler{handle: adapter.HandlePresenceOwnerRPC}
				runGenerationCancellationTransport(t, PresenceOwnerRPCServiceID, handler, body, port, true, deadline)
			})
		}
	}
}

func TestPresenceGenerationTransportMutationsAndUnknownStayDetached(t *testing.T) {
	body := cancellationGenerationBody(t, generationPage)
	cases := []struct {
		name string
		body []byte
	}{
		{name: "unknown-magic", body: append([]byte{'X'}, body[1:]...)},
		{name: "unknown-version", body: append(append([]byte(nil), body[:4]...), append([]byte{2}, body[5:]...)...)},
		{name: "unknown-operation", body: append(append([]byte(nil), body[:5]...), append([]byte{255}, body[6:]...)...)},
		{name: "truncated-header", body: append([]byte(nil), body[:5]...)},
	}
	for _, op := range []byte{generationRegister, generationCommit, generationAbortRoute, generationAbortSlot, generationTouch, generationUnregister} {
		cases = append(cases, struct {
			name string
			body []byte
		}{name: fmt.Sprintf("mutation-%d", op), body: cancellationGenerationBody(t, op)})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if PresenceOwnerReadOnly(tc.body) {
				t.Fatal("mutation/unknown header classified as caller-cancelable")
			}
			port := newGenerationCancellationPort()
			// Unknown bodies cannot reach a real business port. Observe the
			// running transport policy before body validation for these controls.
			handle := func(ctx context.Context, _ []byte) ([]byte, error) { return nil, port.wait(ctx) }
			if tc.name == fmt.Sprintf("mutation-%d", generationAbortSlot) {
				handle = New(Options{GenerationOwner: port}).HandlePresenceOwnerRPC
			}
			runGenerationCancellationTransport(t, PresenceOwnerRPCServiceID, generationCancellationHandler{handle: handle}, tc.body, port, false, false)
		})
	}
}

func TestPresenceGenerationTransportControlCallerBudget(t *testing.T) {
	for _, operation := range []string{"get", "ensure", "claim"} {
		for _, deadline := range []bool{false, true} {
			if operation != "get" && deadline {
				continue
			}
			t.Run(fmt.Sprintf("%s/deadline-%t", operation, deadline), func(t *testing.T) {
				port := newGenerationCancellationPort()
				handler := control.NewPresenceControlHandler(port)
				body := []byte(fmt.Sprintf(`{"version":1,"operation":%q}`, operation))
				if operation == "claim" {
					body = []byte(`{"version":1,"operation":"claim","claim":{}}`)
				}
				runGenerationCancellationTransport(t, clusternet.RPCPresenceControl, handler, body, port, operation == "get", deadline)
			})
		}
	}
}

func TestPresenceGenerationControlClassifierRejectsUnknown(t *testing.T) {
	handler := control.NewPresenceControlHandler(newGenerationCancellationPort())
	policy, ok := handler.(interface{ CancelRunningFor([]byte) bool })
	if !ok {
		t.Fatal("Presence control handler does not preserve its mixed-service caller policy")
	}
	for _, body := range []string{`{"version":1,"operation":"get"}`, `{"version":1,"operation":"capabilities"}`} {
		if !policy.CancelRunningFor([]byte(body)) {
			t.Errorf("known read lost caller policy: %s", body)
		}
	}
	for _, body := range []string{
		`{"version":1,"operation":"ensure"}`, `{"version":1,"operation":"claim","claim":{}}`,
		`{"version":2,"operation":"get"}`, `{"version":1,"operation":"future"}`,
		`{"version":1,"operation":"get","future":1}`, `{"version":1,"operation":"get"} {}`,
		`{"version":1,"operation":"get","claim":{}}`, `{"version":1,"operation":"capabilities","hash_slot":1}`,
	} {
		if policy.CancelRunningFor([]byte(body)) {
			t.Errorf("unknown/mutation body acquired caller policy: %s", body)
		}
	}
}

type generationCancellationHandler struct {
	handle func(context.Context, []byte) ([]byte, error)
}

func (h generationCancellationHandler) HandleRPC(ctx context.Context, body []byte) ([]byte, error) {
	return h.handle(ctx, body)
}
func (generationCancellationHandler) CancelRunningFor(body []byte) bool {
	return PresenceOwnerReadOnly(body)
}

type generationCancellationPort struct {
	started chan context.Context
	joined  chan struct{}
	release chan struct{}
}

func newGenerationCancellationPort() *generationCancellationPort {
	return &generationCancellationPort{started: make(chan context.Context, 1), joined: make(chan struct{}), release: make(chan struct{})}
}
func (p *generationCancellationPort) wait(ctx context.Context) error {
	defer close(p.joined)
	p.started <- ctx
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.release:
		return errors.New("controlled port released after policy observation")
	}
}
func (p *generationCancellationPort) BeginSlot(ctx context.Context, _ presence.GenerationClaim) (presence.OwnerCut, error) {
	return presence.OwnerCut{}, p.wait(ctx)
}
func (p *generationCancellationPort) ReadSlotPage(ctx context.Context, _ presence.SlotPageRequest, _ *presence.RecoveryResultBudget) (*presence.OwnedSlotPage, error) {
	return nil, p.wait(ctx)
}
func (p *generationCancellationPort) EndSlot(ctx context.Context, _ presence.SlotEndRequest) (presence.SlotEnd, error) {
	return presence.SlotEnd{}, p.wait(ctx)
}
func (p *generationCancellationPort) AbortSlot(ctx context.Context, _ presence.GenerationClaim, _ presence.OwnerCut) error {
	return p.wait(ctx)
}
func (p *generationCancellationPort) ReadGenerationUIDs(ctx context.Context, _ presence.GenerationClaim, _ presence.OwnerCut, _ []string, _ *presence.RecoveryResultBudget) (*presence.OwnedSlotRoutes, error) {
	return nil, p.wait(ctx)
}
func (*generationCancellationPort) PresenceCapabilities() controller.PresenceCapabilities {
	return controller.PresenceCapabilities{NodeID: 2, BootID: 3}
}
func (p *generationCancellationPort) EnsurePresenceOwnerSet(ctx context.Context) (controller.PresenceOwnerSet, error) {
	return controller.PresenceOwnerSet{}, p.wait(ctx)
}
func (p *generationCancellationPort) ClaimPresenceGeneration(ctx context.Context, _ controller.ClaimPresenceGenerationRequest) (controller.PresenceGeneration, error) {
	return controller.PresenceGeneration{}, p.wait(ctx)
}
func (p *generationCancellationPort) GetPresenceGeneration(ctx context.Context, _ uint16) (controller.PresenceGenerationSnapshot, error) {
	return controller.PresenceGenerationSnapshot{}, p.wait(ctx)
}

func cancellationGenerationBody(t *testing.T, op byte) []byte {
	t.Helper()
	request := generationRequest{Op: op, Claim: wireGeneration(), Owner: wireCut(), Sequence: 1, MaxRows: 1, MaxBytes: 4096, Route: wireRoute(), Token: "pending", UIDs: []string{"user"}}
	body, err := encodeGenerationRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeGenerationRequest(body)
	if err != nil || decoded.Op != op || !decoded.Claim.Valid() {
		t.Fatalf("invalid Full request fixture: %+v %v", decoded, err)
	}
	return body
}

func runGenerationCancellationTransport(t *testing.T, service uint8, handler clusternet.Handler, body []byte, port *generationCancellationPort, wantCaller, deadline bool) {
	t.Helper()
	server := clusternet.NewTransportServer(clusternet.TransportServerConfig{Service: clusternet.TransportServiceConfig{Concurrency: 1, QueueSize: 1}})
	server.Register(service, handler)
	if err := server.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	discovery := clusternet.NewDiscovery()
	discovery.Update([]clusternet.NodeAddress{{NodeID: 2, Addr: server.Addr()}})
	client := clusternet.NewTransportClient(clusternet.TransportClientConfig{Discovery: discovery, PoolSize: 1})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	callerDeadline, _ := ctx.Deadline()
	done := make(chan error, 1)
	var once sync.Once
	t.Cleanup(func() {
		cancel()
		once.Do(func() { close(port.release) })
		client.Stop()
		stop, end := context.WithTimeout(context.Background(), 2*time.Second)
		defer end()
		if err := server.StopContext(stop); err != nil {
			t.Errorf("transport owner failed to join: %v", err)
		}
	})
	go func() { _, err := client.Call(ctx, 2, service, body); done <- err }()
	var executed context.Context
	select {
	case executed = <-port.started:
	case err := <-done:
		t.Fatalf("caller ended before actual port: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("actual port never started")
	}
	executionDeadline, ok := executed.Deadline()
	if !ok {
		t.Error("port has no finite execution budget")
	} else if wantCaller && executionDeadline.After(callerDeadline.Add(50*time.Millisecond)) {
		t.Errorf("port reset original caller deadline: caller=%s serving=%s", callerDeadline, executionDeadline)
	} else if !wantCaller && !executionDeadline.After(callerDeadline.Add(5*time.Second)) {
		t.Errorf("started cleanup/mutation inherited caller deadline: caller=%s serving=%s", callerDeadline, executionDeadline)
	}
	if !deadline {
		cancel()
	}
	select {
	case err := <-done:
		if err == nil {
			t.Error("canceled caller unexpectedly obtained a successful result")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("caller did not join")
	}
	if wantCaller {
		select {
		case <-executed.Done():
		case <-time.After(500 * time.Millisecond):
			t.Error("started read/temporary scan work detached from original caller cancellation")
		}
	} else {
		select {
		case <-executed.Done():
			t.Error("started cleanup/mutation was canceled by abandoned caller")
		case <-time.After(25 * time.Millisecond):
		}
	}
	once.Do(func() { close(port.release) })
	select {
	case <-port.joined:
	case <-time.After(time.Second):
		t.Fatal("actual port did not join after release")
	}
}
