package chatlifecycle

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/target"
	"github.com/WuKongIM/WuKongIM/internal/bench/wkproto"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestSessionPoolLoginSyncExpiryAndFreshRelogin(t *testing.T) {
	t.Parallel()
	fixture := newSessionTestFixture(t)
	uid := fixture.identity.UID(17)

	first, err := fixture.pool.Login(context.Background(), SessionLogin{
		UID: uid, UserIndex: 17, LoginOrdinal: 9,
	})
	if err != nil {
		t.Fatalf("first Login: %v", err)
	}
	wantSchedule, err := fixture.schedule.Login(9)
	if err != nil {
		t.Fatalf("schedule Login: %v", err)
	}
	if !first.TrafficReady || first.Deadline != fixture.clock.Now().Add(wantSchedule.SessionDuration) {
		t.Fatalf("first session = %+v, duration = %v", first, wantSchedule.SessionDuration)
	}
	if got := fixture.events.snapshot(); len(got) != 3 || got[0] != "factory" || got[1] != "connect" || got[2] != "sync" {
		t.Fatalf("startup order = %v, want [factory connect sync]", got)
	}
	firstToken := fixture.factory.tokens()[0]
	if firstToken == "" {
		t.Fatal("deterministic connect token is empty")
	}
	assertFullSyncRequest(t, fixture.syncer.requests()[0], uid)

	fixture.clock.Set(first.Deadline)
	if expired := fixture.pool.Expire(fixture.clock.Now()); expired != 1 {
		t.Fatalf("Expire = %d, want 1", expired)
	}
	if fixture.pool.IsOnline(uid) || !fixture.factory.clients()[0].closed() {
		t.Fatal("expired session retained online state or open socket")
	}

	fixture.events.reset()
	second, err := fixture.pool.Login(context.Background(), SessionLogin{
		UID: uid, UserIndex: 17, LoginOrdinal: 10,
	})
	if err != nil {
		t.Fatalf("second Login: %v", err)
	}
	if !second.TrafficReady {
		t.Fatal("re-login did not become traffic-ready")
	}
	clients := fixture.factory.clients()
	if len(clients) != 2 || clients[0] == clients[1] {
		t.Fatalf("re-login clients = %d, want two fresh instances", len(clients))
	}
	if tokens := fixture.factory.tokens(); len(tokens) != 2 || tokens[0] != tokens[1] {
		t.Fatalf("same UID tokens = %v, want deterministic equality", tokens)
	}
	requests := fixture.syncer.requests()
	if len(requests) != 2 {
		t.Fatalf("sync requests = %d, want 2", len(requests))
	}
	assertFullSyncRequest(t, requests[1], uid)
	if got := fixture.events.snapshot(); len(got) != 3 || got[0] != "factory" || got[1] != "connect" || got[2] != "sync" {
		t.Fatalf("re-login order = %v, want [factory connect sync]", got)
	}
	if err := fixture.pool.Logout(uid); err != nil {
		t.Fatalf("Logout: %v", err)
	}
}

func TestSessionWKProtoAdapterBindsExistingClientWithoutDialing(t *testing.T) {
	t.Parallel()
	if _, err := NewWKProtoSessionAdapter(nil); !errors.Is(err, errSessionConfig) {
		t.Fatalf("nil adapter error = %v", err)
	}
	client, err := wkproto.NewClient(wkproto.ClientConfig{Addr: "127.0.0.1:1", FrameBufferSize: 8})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	adapter, err := NewWKProtoSessionAdapter(client)
	if err != nil {
		t.Fatalf("NewWKProtoSessionAdapter: %v", err)
	}
	if snapshot := adapter.QueueSnapshot(); snapshot.Depth != 0 || snapshot.Capacity != 8 || snapshot.Inflight != 0 {
		t.Fatalf("disconnected queue snapshot = %+v", snapshot)
	}
}

func TestSessionPoolActivatesRelationshipOnlyWhileBothEndpointsOnline(t *testing.T) {
	t.Parallel()
	fixture := newSessionTestFixture(t)
	edge := fixture.graph.Incoming(8).Items[0]

	if _, err := fixture.pool.Login(context.Background(), SessionLogin{UID: edge.OwnerUID, UserIndex: edge.OwnerIndex, LoginOrdinal: 1}); err != nil {
		t.Fatalf("owner Login: %v", err)
	}
	if fixture.pool.CanActivate(edge) {
		t.Fatal("edge activated with peer offline")
	}
	if _, err := fixture.pool.Login(context.Background(), SessionLogin{UID: edge.PeerUID, UserIndex: edge.PeerIndex, LoginOrdinal: 2}); err != nil {
		t.Fatalf("peer Login: %v", err)
	}
	if !fixture.pool.CanActivate(edge) {
		t.Fatal("edge did not activate with both endpoints online")
	}
	if err := fixture.pool.Logout(edge.OwnerUID); err != nil {
		t.Fatalf("owner Logout: %v", err)
	}
	if fixture.pool.CanActivate(edge) {
		t.Fatal("edge remained active after owner logout")
	}
	if err := fixture.pool.Logout(edge.PeerUID); err != nil {
		t.Fatalf("peer Logout: %v", err)
	}
}

func TestSessionPoolLogoutJoinsOrderedDrainBeforeReleaseRecipient(t *testing.T) {
	t.Parallel()
	fixture := newSessionTestFixture(t)
	uid := fixture.identity.UID(31)
	if _, err := fixture.pool.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 31, LoginOrdinal: 3}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	client := fixture.factory.clients()[0]
	for ordinal := uint64(1); ordinal <= 2; ordinal++ {
		logical, err := fixture.traffic.NewLogicalSend(0, ordinal, TrafficGroup, fixture.identity.UID(2), "group-drain")
		if err != nil {
			t.Fatalf("NewLogicalSend(%d): %v", ordinal, err)
		}
		payload, err := fixture.traffic.BuildPayload(logical, 256)
		if err != nil {
			t.Fatalf("BuildPayload(%d): %v", ordinal, err)
		}
		client.frames <- &frame.RecvPacket{
			MessageID: int64(ordinal), MessageSeq: ordinal, ClientMsgNo: logical.ClientMsgNo,
			FromUID: logical.Sender, ChannelID: logical.Target, ChannelType: frame.ChannelTypeGroup, Payload: payload,
		}
	}
	for want := uint64(1); want <= 2; want++ {
		got := <-client.acked
		if got != want {
			t.Fatalf("RECVACK order = %d, want %d", got, want)
		}
	}
	if got := fixture.verifier.Snapshot().SequenceCurrent; got != 1 {
		t.Fatalf("sequence current before logout = %d, want 1 channel", got)
	}
	if err := fixture.pool.Logout(uid); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if got := fixture.verifier.Snapshot().SequenceCurrent; got != 0 {
		t.Fatalf("sequence current after joined logout = %d, want 0", got)
	}
	select {
	case <-client.readExited:
	default:
		t.Fatal("Logout returned before receive drain exited")
	}
}

func TestSessionPoolUnexpectedReadExitIsBoundedHarnessEvidence(t *testing.T) {
	t.Parallel()
	fixture := newSessionTestFixture(t)
	uid := fixture.identity.UID(32)
	if _, err := fixture.pool.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 32, LoginOrdinal: 4}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	client := fixture.factory.clients()[0]
	if err := client.Close(); err != nil {
		t.Fatalf("unexpected Close: %v", err)
	}
	<-client.readExited
	snapshot := fixture.pool.Snapshot()
	if snapshot.ReadErrors != 1 || fixture.verifier.EvidenceSnapshot().Classification != SyncClassificationHarnessInvalid {
		t.Fatalf("unexpected read evidence = pool %+v verifier %+v", snapshot, fixture.verifier.EvidenceSnapshot())
	}
	if err := fixture.pool.Logout(uid); err != nil {
		t.Fatalf("Logout: %v", err)
	}
}

func TestSessionPoolStartsIndependentLoginsConcurrentlyWithinBound(t *testing.T) {
	t.Parallel()
	fixture := newSessionTestFixture(t)
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	factory := &parallelSessionFactory{entered: entered, release: release}
	pool, err := NewSessionPool(SessionPoolConfig{
		Identity: fixture.identity, Schedule: fixture.schedule, Factory: factory,
		Syncer: fixture.syncer, Verifier: fixture.verifier, Clock: fixture.clock,
		DeviceID: "parallel-login", StartingCapacity: 2,
	})
	if err != nil {
		t.Fatalf("NewSessionPool: %v", err)
	}
	errs := make(chan error, 2)
	for index := uint64(40); index < 42; index++ {
		index := index
		go func() {
			_, loginErr := pool.Login(context.Background(), SessionLogin{
				UID: fixture.identity.UID(index), UserIndex: index, LoginOrdinal: index,
			})
			errs <- loginErr
		}()
	}
	<-entered
	<-entered
	close(release)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent Login: %v", err)
		}
	}
	if snapshot := pool.Snapshot(); snapshot.Online != 2 || snapshot.Starting != 0 {
		t.Fatalf("parallel pool snapshot = %+v", snapshot)
	}
	if err := pool.CloseAll(); err != nil {
		t.Fatalf("CloseAll: %v", err)
	}
}

type sessionTestFixture struct {
	identity *IdentitySpace
	schedule ScheduleModel
	graph    RelationshipGraph
	traffic  TrafficModel
	verifier *Verifier
	clock    *sessionFakeClock
	events   *sessionEventLog
	factory  *sessionFakeFactory
	syncer   *sessionFakeSyncer
	pool     *SessionPool
}

func newSessionTestFixture(t *testing.T) sessionTestFixture {
	t.Helper()
	cfg := LocalConfig()
	identity, err := NewIdentitySpace("session-test", 71, uint64(cfg.Workload.Workers))
	if err != nil {
		t.Fatalf("NewIdentitySpace: %v", err)
	}
	schedule, err := NewScheduleModel(identity, cfg.Workload)
	if err != nil {
		t.Fatalf("NewScheduleModel: %v", err)
	}
	graph, err := NewRelationshipGraph(identity)
	if err != nil {
		t.Fatalf("NewRelationshipGraph: %v", err)
	}
	traffic, err := NewTrafficModel(identity, cfg.Workload)
	if err != nil {
		t.Fatalf("NewTrafficModel: %v", err)
	}
	evidence, err := NewEvidenceRecorder(2, 2)
	if err != nil {
		t.Fatalf("NewEvidenceRecorder: %v", err)
	}
	verifier, err := NewVerifier(traffic, VerifierConfig{
		PendingCapacity: 128, SequenceCapacity: 128, CorrelationCapacity: 16, CorrelationDeadline: time.Minute,
	}, evidence)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	clock := &sessionFakeClock{now: time.Unix(1_700_000_000, 0)}
	events := &sessionEventLog{}
	factory := &sessionFakeFactory{events: events}
	syncer := &sessionFakeSyncer{events: events}
	pool, err := NewSessionPool(SessionPoolConfig{
		Identity: identity, Schedule: schedule, Factory: factory, Syncer: syncer,
		Verifier: verifier, Clock: clock, DeviceID: "wkbench-lifecycle", StartingCapacity: 128,
	})
	if err != nil {
		t.Fatalf("NewSessionPool: %v", err)
	}
	return sessionTestFixture{
		identity: identity, schedule: schedule, graph: graph, traffic: traffic, verifier: verifier,
		clock: clock, events: events, factory: factory, syncer: syncer, pool: pool,
	}
}

type parallelSessionFactory struct {
	entered chan<- struct{}
	release <-chan struct{}
}

func (f *parallelSessionFactory) NewSession(_, _ string) (SessionClient, error) {
	return &parallelSessionClient{
		entered: f.entered, release: f.release, stop: make(chan struct{}), readExited: make(chan struct{}),
	}, nil
}

type parallelSessionClient struct {
	entered    chan<- struct{}
	release    <-chan struct{}
	stop       chan struct{}
	readExited chan struct{}
	closeOnce  sync.Once
}

func (c *parallelSessionClient) Connect(ctx context.Context, _, _ string) error {
	c.entered <- struct{}{}
	select {
	case <-c.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (c *parallelSessionClient) ReadFrame(ctx context.Context) (frame.Frame, error) {
	select {
	case <-ctx.Done():
		c.closeReadExited()
		return nil, ctx.Err()
	case <-c.stop:
		c.closeReadExited()
		return nil, errors.New("closed")
	}
}
func (c *parallelSessionClient) Send(context.Context, *frame.SendPacket) error       { return nil }
func (c *parallelSessionClient) AckRecv(context.Context, *frame.RecvackPacket) error { return nil }
func (c *parallelSessionClient) Close() error {
	c.closeOnce.Do(func() { close(c.stop) })
	return nil
}
func (c *parallelSessionClient) QueueSnapshot() SessionQueueSnapshot { return SessionQueueSnapshot{} }
func (c *parallelSessionClient) closeReadExited() {
	select {
	case <-c.readExited:
	default:
		close(c.readExited)
	}
}

func assertFullSyncRequest(t *testing.T, got target.ConversationSyncRequest, uid string) {
	t.Helper()
	want := NewConversationSyncRequest(uid)
	if got != want {
		t.Fatalf("sync request = %+v, want %+v", got, want)
	}
}

type sessionFakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *sessionFakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *sessionFakeClock) Set(now time.Time) {
	c.mu.Lock()
	c.now = now
	c.mu.Unlock()
}

type sessionEventLog struct {
	mu     sync.Mutex
	events []string
}

func (l *sessionEventLog) add(event string) {
	l.mu.Lock()
	l.events = append(l.events, event)
	l.mu.Unlock()
}

func (l *sessionEventLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.events...)
}

func (l *sessionEventLog) reset() {
	l.mu.Lock()
	l.events = nil
	l.mu.Unlock()
}

type sessionFakeFactory struct {
	mu       sync.Mutex
	events   *sessionEventLog
	tokensV  []string
	clientsV []*sessionFakeClient
}

func (f *sessionFakeFactory) NewSession(_ string, token string) (SessionClient, error) {
	f.events.add("factory")
	client := &sessionFakeClient{
		events: f.events, frames: make(chan frame.Frame, 8), acked: make(chan uint64, 8),
		stop: make(chan struct{}), readExited: make(chan struct{}),
	}
	f.mu.Lock()
	f.tokensV = append(f.tokensV, token)
	f.clientsV = append(f.clientsV, client)
	f.mu.Unlock()
	return client, nil
}

func (f *sessionFakeFactory) tokens() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.tokensV...)
}

func (f *sessionFakeFactory) clients() []*sessionFakeClient {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*sessionFakeClient(nil), f.clientsV...)
}

type sessionFakeSyncer struct {
	mu        sync.Mutex
	events    *sessionEventLog
	requestsV []target.ConversationSyncRequest
}

func (s *sessionFakeSyncer) ConversationSync(_ context.Context, request target.ConversationSyncRequest) ([]target.ConversationSyncConversation, error) {
	s.events.add("sync")
	s.mu.Lock()
	s.requestsV = append(s.requestsV, request)
	s.mu.Unlock()
	return nil, nil
}

func (s *sessionFakeSyncer) requests() []target.ConversationSyncRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]target.ConversationSyncRequest(nil), s.requestsV...)
}

type sessionFakeClient struct {
	mu         sync.Mutex
	events     *sessionEventLog
	frames     chan frame.Frame
	acked      chan uint64
	stop       chan struct{}
	readExited chan struct{}
	closeOnce  sync.Once
	isClosed   bool
}

func (c *sessionFakeClient) Connect(_ context.Context, _, _ string) error {
	c.events.add("connect")
	return nil
}

func (c *sessionFakeClient) ReadFrame(ctx context.Context) (frame.Frame, error) {
	select {
	case packet := <-c.frames:
		return packet, nil
	case <-ctx.Done():
		c.closeReadExited()
		return nil, ctx.Err()
	case <-c.stop:
		c.closeReadExited()
		return nil, errors.New("session closed")
	}
}

func (c *sessionFakeClient) Send(context.Context, *frame.SendPacket) error { return nil }

func (c *sessionFakeClient) AckRecv(_ context.Context, ack *frame.RecvackPacket) error {
	c.acked <- ack.MessageSeq
	return nil
}

func (c *sessionFakeClient) Close() error {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.isClosed = true
		c.mu.Unlock()
		close(c.stop)
	})
	return nil
}

func (c *sessionFakeClient) QueueSnapshot() SessionQueueSnapshot { return SessionQueueSnapshot{} }

func (c *sessionFakeClient) closed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.isClosed
}

func (c *sessionFakeClient) closeReadExited() {
	select {
	case <-c.readExited:
	default:
		close(c.readExited)
	}
}
