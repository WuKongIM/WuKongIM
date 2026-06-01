package presence

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestActivateRegistersPendingLocalRouteThenAuthorityThenMarksActive(t *testing.T) {
	var calls []string
	local := newFakeLocalRegistry(&calls)
	authority := &fakeAuthorityClient{calls: &calls}
	app := New(Options{
		Local:       local,
		Authority:   authority,
		OwnerNodeID: 9,
		OwnerBootID: 99,
		HashSlot:    func(string) (uint16, error) { return 7, nil },
		OwnerSeq:    func(string) uint64 { return 33 },
	})

	err := app.Activate(context.Background(), ActivateCommand{
		UID:           "u1",
		DeviceID:      "d1",
		DeviceFlag:    1,
		DeviceLevel:   1,
		Listener:      "tcp",
		ConnectedUnix: 123,
		SessionID:     11,
		Session:       fakeSessionHandle{},
	})
	if err != nil {
		t.Fatalf("Activate() error = %v", err)
	}

	want := []string{"local.register_pending", "authority.register", "local.mark_active"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	if got := local.pending[11]; got.UID != "u1" || got.HashSlot != 7 || got.OwnerBootID != 99 || got.OwnerSeq != 33 {
		t.Fatalf("pending conn = %#v, want owner metadata copied", got)
	}
	if got := authority.registered; got.UID != "u1" || got.OwnerNodeID != 9 || got.OwnerBootID != 99 || got.OwnerSeq != 33 || got.SessionID != 11 {
		t.Fatalf("registered route = %#v, want route from command and owner metadata", got)
	}
}

func TestActivateRollsBackLocalRouteWhenAuthorityRegisterFails(t *testing.T) {
	var calls []string
	local := newFakeLocalRegistry(&calls)
	authority := &fakeAuthorityClient{calls: &calls, registerErr: errBoom}
	app := New(Options{Local: local, Authority: authority})

	err := app.Activate(context.Background(), ActivateCommand{
		UID:       "u1",
		SessionID: 11,
		Session:   fakeSessionHandle{},
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("Activate() error = %v, want errBoom", err)
	}

	want := []string{"local.register_pending", "authority.register", "local.mark_closing_unregister"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	if _, ok := local.pending[11]; ok {
		t.Fatal("session remains registered locally after authority register failure")
	}
}

func TestActivateReturnsHashSlotErrorBeforeLocalRegister(t *testing.T) {
	var calls []string
	local := newFakeLocalRegistry(&calls)
	authority := &fakeAuthorityClient{calls: &calls}
	app := New(Options{
		Local:     local,
		Authority: authority,
		HashSlot:  func(string) (uint16, error) { return 0, errBoom },
	})

	err := app.Activate(context.Background(), ActivateCommand{
		UID:       "u1",
		SessionID: 11,
		Session:   fakeSessionHandle{},
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("Activate() error = %v, want errBoom", err)
	}
	if len(calls) != 0 {
		t.Fatalf("calls = %v, want no local or authority calls", calls)
	}
}

func TestActivateUnregistersAuthorityWhenSessionClosedDuringActivation(t *testing.T) {
	var calls []string
	local := newFakeLocalRegistry(&calls)
	authority := &fakeAuthorityClient{calls: &calls}
	app := New(Options{
		Local:       local,
		Authority:   authority,
		OwnerNodeID: 3,
		OwnerBootID: 4,
		OwnerSeq:    func(string) uint64 { return 5 },
	})
	authority.afterRegister = func() {
		local.MarkClosingAndUnregister(11)
	}

	err := app.Activate(context.Background(), ActivateCommand{
		UID:       "u1",
		SessionID: 11,
		Session:   fakeSessionHandle{},
	})
	if !errors.Is(err, ErrSessionNotActive) {
		t.Fatalf("Activate() error = %v, want ErrSessionNotActive", err)
	}

	want := []string{"local.register_pending", "authority.register", "local.mark_closing_unregister", "local.mark_active", "authority.enqueue_unregister", "local.mark_closing_unregister"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	wantIdentity := RouteIdentity{UID: "u1", OwnerNodeID: 3, OwnerBootID: 4, SessionID: 11}
	if authority.unregisteredIdentity != wantIdentity || authority.unregisteredSeq != 5 {
		t.Fatalf("unregister = (%#v,%d), want (%#v,5)", authority.unregisteredIdentity, authority.unregisteredSeq, wantIdentity)
	}
}

func TestActivateCleansPendingLocalRouteWhenMarkActiveFails(t *testing.T) {
	var calls []string
	local := newFakeLocalRegistry(&calls)
	local.markActiveErr = errBoom
	authority := &fakeAuthorityClient{calls: &calls}
	app := New(Options{
		Local:       local,
		Authority:   authority,
		OwnerNodeID: 3,
		OwnerBootID: 4,
		OwnerSeq:    func(string) uint64 { return 5 },
	})

	err := app.Activate(context.Background(), ActivateCommand{
		UID:       "u1",
		SessionID: 11,
		Session:   fakeSessionHandle{},
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("Activate() error = %v, want errBoom", err)
	}

	want := []string{"local.register_pending", "authority.register", "local.mark_active", "authority.enqueue_unregister", "local.mark_closing_unregister"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	if _, ok := local.pending[11]; ok {
		t.Fatal("pending route remains after MarkActive failure")
	}
}

func TestActivateAppliesPendingActionsBeforeCommitThenMarksActive(t *testing.T) {
	var calls []string
	local := newFakeLocalRegistry(&calls)
	local.pending[21] = OnlineConn{UID: "u1", OwnerNodeID: 3, OwnerBootID: 4, SessionID: 21, Session: fakeSessionHandle{}}
	authority := &fakeAuthorityClient{
		calls: &calls,
		registerResult: RegisterResult{
			PendingToken: "pending-1",
			Actions: []RouteAction{{
				UID:         "u1",
				OwnerNodeID: 3,
				OwnerBootID: 4,
				SessionID:   21,
				Reason:      "presence_conflict",
			}},
		},
	}
	ownerActions := &fakeOwnerActionClient{calls: &calls, local: local}
	app := New(Options{Local: local, Authority: authority, OwnerActions: ownerActions})

	err := app.Activate(context.Background(), ActivateCommand{
		UID:       "u1",
		SessionID: 11,
		Session:   fakeSessionHandle{},
	})
	if err != nil {
		t.Fatalf("Activate() error = %v", err)
	}

	want := []string{"local.register_pending", "authority.register", "owner.apply_action", "local.mark_closing_unregister", "authority.commit", "local.mark_active"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	if _, ok := local.pending[21]; ok {
		t.Fatal("conflicting route remains after successful pending action")
	}
}

func TestActivateAbortsPendingRouteAndKeepsConflictsWhenActionFails(t *testing.T) {
	var calls []string
	local := newFakeLocalRegistry(&calls)
	local.pending[21] = OnlineConn{UID: "u1", OwnerNodeID: 3, OwnerBootID: 4, SessionID: 21, Session: fakeSessionHandle{}}
	local.pending[22] = OnlineConn{UID: "u1", OwnerNodeID: 3, OwnerBootID: 4, SessionID: 22, Session: fakeSessionHandle{err: errBoom}}
	authority := &fakeAuthorityClient{
		calls: &calls,
		registerResult: RegisterResult{
			PendingToken: "pending-1",
			Actions: []RouteAction{{
				UID:         "u1",
				OwnerNodeID: 3,
				OwnerBootID: 4,
				SessionID:   21,
				Reason:      "presence_conflict",
			}, {
				UID:         "u1",
				OwnerNodeID: 3,
				OwnerBootID: 4,
				SessionID:   22,
				Reason:      "presence_conflict",
			}},
		},
	}
	ownerActions := &fakeOwnerActionClient{calls: &calls, local: local}
	app := New(Options{Local: local, Authority: authority, OwnerActions: ownerActions})

	err := app.Activate(context.Background(), ActivateCommand{
		UID:       "u1",
		SessionID: 11,
		Session:   fakeSessionHandle{},
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("Activate() error = %v, want errBoom", err)
	}

	want := []string{"local.register_pending", "authority.register", "owner.apply_action", "local.mark_closing_unregister", "owner.apply_action", "authority.abort", "local.mark_closing_unregister"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	if _, ok := local.pending[21]; ok {
		t.Fatal("first conflicting route remains after acknowledged owner action")
	}
	if _, ok := local.pending[22]; !ok {
		t.Fatal("second conflicting route was removed after failed action")
	}
	if _, ok := local.pending[11]; ok {
		t.Fatal("new pending route remains after action failure")
	}
}

func TestActivateAbortsPendingRouteWhenOwnerActionClientMissing(t *testing.T) {
	var calls []string
	local := newFakeLocalRegistry(&calls)
	authority := &fakeAuthorityClient{
		calls: &calls,
		registerResult: RegisterResult{
			PendingToken: "pending-1",
			Actions: []RouteAction{{
				UID:         "u1",
				OwnerNodeID: 3,
				OwnerBootID: 4,
				SessionID:   21,
				Reason:      "presence_conflict",
			}},
		},
	}
	app := New(Options{Local: local, Authority: authority})

	err := app.Activate(context.Background(), ActivateCommand{
		UID:       "u1",
		SessionID: 11,
		Session:   fakeSessionHandle{},
	})
	if !errors.Is(err, ErrOwnerActionUnavailable) {
		t.Fatalf("Activate() error = %v, want ErrOwnerActionUnavailable", err)
	}
	want := []string{"local.register_pending", "authority.register", "authority.abort", "local.mark_closing_unregister"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestDeactivateRemovesLocalRouteAndQueuesAuthorityTombstone(t *testing.T) {
	var calls []string
	local := newFakeLocalRegistry(&calls)
	local.pending[11] = OnlineConn{UID: "u1", OwnerNodeID: 3, OwnerBootID: 4, OwnerSeq: 5, SessionID: 11}
	authority := &fakeAuthorityClient{calls: &calls}
	app := New(Options{Local: local, Authority: authority})

	err := app.Deactivate(context.Background(), DeactivateCommand{UID: "u1", SessionID: 11})
	if err != nil {
		t.Fatalf("Deactivate() error = %v", err)
	}

	want := []string{"local.mark_closing_unregister", "authority.enqueue_unregister"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	wantIdentity := RouteIdentity{UID: "u1", OwnerNodeID: 3, OwnerBootID: 4, SessionID: 11}
	if authority.unregisteredIdentity != wantIdentity || authority.unregisteredSeq != 5 {
		t.Fatalf("unregister = (%#v,%d), want (%#v,5)", authority.unregisteredIdentity, authority.unregisteredSeq, wantIdentity)
	}
}

func TestEndpointsByUIDUsesAuthorityClient(t *testing.T) {
	var calls []string
	authority := &fakeAuthorityClient{
		calls:     &calls,
		endpoints: []Route{{UID: "u1", SessionID: 11}},
	}
	app := New(Options{Authority: authority})

	routes, err := app.EndpointsByUID(context.Background(), "u1")
	if err != nil {
		t.Fatalf("EndpointsByUID() error = %v", err)
	}
	if len(routes) != 1 || routes[0].SessionID != 11 {
		t.Fatalf("routes = %#v, want session 11", routes)
	}
	want := []string{"authority.endpoints_by_uid"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

var errBoom = errors.New("boom")

type fakeLocalRegistry struct {
	calls         *[]string
	pending       map[uint64]OnlineConn
	markActiveErr error
}

func newFakeLocalRegistry(calls *[]string) *fakeLocalRegistry {
	return &fakeLocalRegistry{
		calls:   calls,
		pending: make(map[uint64]OnlineConn),
	}
}

func (f *fakeLocalRegistry) RegisterPending(conn OnlineConn) error {
	*f.calls = append(*f.calls, "local.register_pending")
	f.pending[conn.SessionID] = conn
	return nil
}

func (f *fakeLocalRegistry) MarkActive(sessionID uint64) error {
	*f.calls = append(*f.calls, "local.mark_active")
	if f.markActiveErr != nil {
		return f.markActiveErr
	}
	conn, ok := f.pending[sessionID]
	if !ok {
		return ErrSessionNotActive
	}
	conn.State = RouteStateActive
	f.pending[sessionID] = conn
	return nil
}

func (f *fakeLocalRegistry) MarkClosingAndUnregister(sessionID uint64) (OnlineConn, bool) {
	*f.calls = append(*f.calls, "local.mark_closing_unregister")
	conn, ok := f.pending[sessionID]
	delete(f.pending, sessionID)
	return conn, ok
}

func (f *fakeLocalRegistry) Connection(sessionID uint64) (OnlineConn, bool) {
	*f.calls = append(*f.calls, "local.connection")
	conn, ok := f.pending[sessionID]
	return conn, ok
}

type fakeAuthorityClient struct {
	calls                *[]string
	registerErr          error
	registerResult       RegisterResult
	registered           Route
	afterRegister        func()
	unregisteredIdentity RouteIdentity
	unregisteredSeq      uint64
	endpoints            []Route
	endpointsErr         error
}

func (f *fakeAuthorityClient) RegisterRoute(_ context.Context, route Route) (RegisterResult, error) {
	*f.calls = append(*f.calls, "authority.register")
	f.registered = route
	if f.registerErr != nil {
		return RegisterResult{}, f.registerErr
	}
	if f.afterRegister != nil {
		f.afterRegister()
	}
	return f.registerResult, nil
}

func (f *fakeAuthorityClient) CommitRoute(context.Context, PendingRouteToken) error {
	*f.calls = append(*f.calls, "authority.commit")
	return nil
}

func (f *fakeAuthorityClient) AbortRoute(context.Context, PendingRouteToken) error {
	*f.calls = append(*f.calls, "authority.abort")
	return nil
}

func (f *fakeAuthorityClient) EnqueueUnregister(_ context.Context, identity RouteIdentity, ownerSeq uint64) {
	*f.calls = append(*f.calls, "authority.enqueue_unregister")
	f.unregisteredIdentity = identity
	f.unregisteredSeq = ownerSeq
}

func (f *fakeAuthorityClient) EndpointsByUID(context.Context, string) ([]Route, error) {
	*f.calls = append(*f.calls, "authority.endpoints_by_uid")
	return f.endpoints, f.endpointsErr
}

type fakeOwnerActionClient struct {
	calls *[]string
	local *fakeLocalRegistry
}

func (f *fakeOwnerActionClient) ApplyRouteAction(_ context.Context, action RouteAction) error {
	*f.calls = append(*f.calls, "owner.apply_action")
	if f.local == nil {
		return nil
	}
	conn, ok := f.local.pending[action.SessionID]
	if !ok || conn.UID != action.UID || conn.OwnerNodeID != action.OwnerNodeID || conn.OwnerBootID != action.OwnerBootID {
		return nil
	}
	if conn.Session != nil {
		if err := conn.Session.CloseSession(action.Reason); err != nil {
			return err
		}
	}
	f.local.MarkClosingAndUnregister(action.SessionID)
	return nil
}

type fakeSessionHandle struct {
	err error
}

func (h fakeSessionHandle) CloseSession(string) error { return h.err }
