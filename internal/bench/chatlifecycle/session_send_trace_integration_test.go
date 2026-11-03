//go:build integration

package chatlifecycle

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/trace"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const sessionSendLookupRegion = "wkbench.session_send.pool_lookup"
const sessionSendClientRegion = "wkbench.session_send.client_try_send"

type sessionSendTraceFactory struct {
	base   SessionClientFactory
	pool   *SessionPool
	client *sessionSendTraceClient
}

func (f *sessionSendTraceFactory) NewSession(ctx context.Context, uid, token string) (SessionClient, error) {
	base, err := f.base.NewSession(ctx, uid, token)
	if err != nil {
		return nil, err
	}
	f.client = &sessionSendTraceClient{SessionClient: base, pool: f.pool, entered: make(chan struct{}, 1)}
	return f.client, nil
}

// Only the delegated admission is controlled. Login, lookup, read/heartbeat
// ownership, and CloseAll run through the actual SessionPool implementation.
type sessionSendTraceClient struct {
	SessionClient
	pool        *SessionPool
	entered     chan struct{}
	release     <-chan struct{}
	wantPacket  *frame.SendPacket
	wantContext context.Context
	calls       int
}

func (c *sessionSendTraceClient) TrySend(ctx context.Context, packet *frame.SendPacket) error {
	c.calls++
	if packet != c.wantPacket || ctx != c.wantContext {
		return errors.New("calibration delegate arguments changed")
	}
	if !c.pool.mu.TryLock() {
		return errors.New("calibration delegate entered before pool lookup unlocked")
	}
	c.pool.mu.Unlock()
	trace.Log(ctx, "session_send_calibration", "delegate_enter")
	c.entered <- struct{}{}
	if c.release != nil {
		<-c.release
	}
	return nil
}

// This is deliberate attribution calibration, not a valid non-waiting client
// implementation or a reproduction of an SDK/server performance incident.
func TestSessionPoolTrySendTraceCalibrationChild(t *testing.T) {
	if os.Getenv("WK_SESSION_SEND_TRACE_CHILD") != "1" {
		return
	}
	oldProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(oldProcs)
	file, err := os.OpenFile(os.Getenv("WK_SESSION_SEND_TRACE_OUTPUT"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := trace.Start(&r28RegionWriter{target: file, remaining: 16 << 20}); err != nil {
		t.Fatal(err)
	}
	defer trace.Stop()
	f := newSessionTestFixture(t)
	factory := &sessionSendTraceFactory{base: f.factory, pool: f.pool}
	f.pool.factory = factory
	defer func() {
		if err := f.pool.CloseAll(); err != nil {
			t.Errorf("CloseAll: %v", err)
		}
	}()
	uid := f.identity.UID(17)
	if _, err := f.pool.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 17, LoginOrdinal: 9}); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"pool_lock", "client_wait"} {
		sessionSendTraceCalibrateCall(t, f.pool, factory.client, uid, mode)
	}
	if factory.client.calls != 2 {
		t.Fatalf("delegated calls = %d, want exactly two", factory.client.calls)
	}
	if err := f.pool.CloseAll(); err != nil {
		t.Fatal(err)
	}
	trace.Stop()
	if err := file.Sync(); err != nil {
		t.Fatal(err)
	}
}

func sessionSendTraceCalibrateCall(t *testing.T, pool *SessionPool, client *sessionSendTraceClient, uid, mode string) {
	t.Helper()
	ctx, task := trace.NewTask(context.Background(), "session_send_calibration")
	defer task.End()
	client.wantContext = ctx
	client.wantPacket = &frame.SendPacket{ClientSeq: uint64(client.calls + 1)}
	client.release = nil
	release, joined := make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	var releaseOnce sync.Once
	locked := mode == "pool_lock"
	if locked {
		pool.mu.Lock()
	} else {
		client.release = release
	}
	unblock := func() {
		releaseOnce.Do(func() {
			if locked {
				pool.mu.Unlock()
			} else {
				close(release)
			}
		})
	}
	defer func() {
		// Failure also releases before joining and before the parent CloseAll.
		unblock()
		select {
		case <-joined:
		case <-time.After(time.Second):
			t.Error("calibration TrySend owner did not join")
		}
	}()
	before := client.calls
	go func() {
		defer close(joined)
		region := trace.StartRegion(ctx, "wkbench.calibration."+mode)
		err := pool.TrySend(ctx, uid, client.wantPacket)
		region.End()
		result <- err
	}()
	wantStack := ".(*SessionPool).TrySend("
	if mode == "client_wait" {
		select {
		case <-client.entered:
		case <-time.After(2 * time.Second):
			t.Fatal("calibration client entry not observed")
		}
		wantStack = ".(*sessionSendTraceClient).TrySend("
	}
	sessionSendTraceWaitBlocked(t, wantStack, mode)
	if mode == "pool_lock" {
		select {
		case <-client.entered:
			t.Fatal("delegate crossed the held pool lookup barrier")
		default:
		}
	}
	unblock()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("TrySend failed to finish after barrier release")
	}
	<-joined
	if mode == "pool_lock" {
		select {
		case <-client.entered:
		default:
			t.Fatal("delegate was not called after lookup release")
		}
	}
	if client.calls != before+1 {
		t.Fatalf("delegate count changed by %d, want one", client.calls-before)
	}
}

func sessionSendTraceWaitBlocked(t *testing.T, wantStack, mode string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	buf := make([]byte, 256<<10)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			for _, stack := range strings.Split(string(buf[:n]), "\n\n") {
				if !strings.Contains(stack, wantStack) {
					continue
				}
				if mode == "pool_lock" && strings.Contains(stack, "sync.(*RWMutex).RLock(") || mode == "client_wait" && strings.Contains(strings.SplitN(stack, "\n", 2)[0], "chan receive") {
					return
				}
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal("calibration blocked site not proven by a complete stack cut")
		case <-time.After(time.Millisecond):
		}
	}
}

func TestSessionPoolTrySendTraceSegmentsCalibrateWaitOwnership(t *testing.T) {
	root := os.Getenv("WK_SESSION_SEND_TRACE_ARTIFACT_ROOT")
	if root == "" {
		root = t.TempDir()
	} else {
		if !filepath.IsAbs(root) {
			t.Fatal("artifact root must be absolute")
		}
		root = filepath.Join(root, "session-pool-send-regions")
		if err := os.Mkdir(root, 0700); err != nil {
			t.Fatal(err)
		}
	}
	output := filepath.Join(root, "trace.out")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestSessionPoolTrySendTraceCalibrationChild$", "-test.timeout=12s")
	cmd.Env = append(os.Environ(), "WK_SESSION_SEND_TRACE_CHILD=1", "WK_SESSION_SEND_TRACE_OUTPUT="+output)
	var childOutput r28DecodeBuffer
	cmd.Stdout, cmd.Stderr = &childOutput, &childOutput
	if err := cmd.Run(); err != nil || childOutput.truncated {
		t.Fatalf("calibration child failed/truncated: %v\n%s", err, childOutput.String())
	}
	info, err := os.Lstat(output)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 16<<20 || info.Mode().Perm() != 0600 {
		t.Fatal("raw calibration trace is absent, truncated, or invalid")
	}
	decodeCtx, decodeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer decodeCancel()
	decoder := exec.CommandContext(decodeCtx, filepath.Join(runtime.GOROOT(), "bin", "go"), "tool", "trace", "-d=parsed", output)
	var decoded r28DecodeBuffer
	decoder.Stdout, decoder.Stderr = &decoded, &decoded
	if err := decoder.Run(); err != nil || decoded.truncated {
		t.Fatalf("calibration trace decode failed/truncated: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "decoded.txt"), decoded.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	sessionSendTraceVerify(t, decoded.String())
	raw, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("calibration trace=%s bytes=%d sha256=%x child_joined=true cases=2 historical_R34_cause=unknown running_is_wall=true", output, len(raw), sha256.Sum256(raw))
}

// Resource identifies the goroutine being transitioned. Header G alone can
// identify a different executor and must never attribute that executor's wait.
func sessionSendTraceVerify(t *testing.T, decoded string) {
	t.Helper()
	header := regexp.MustCompile(`(?m)^M=-?\d+ P=-?\d+ G=(-?\d+) [^\n]*`)
	resource := regexp.MustCompile(`Resource=Goroutine\((\d+)\)`)
	indexes := header.FindAllStringSubmatchIndex(decoded, -1)
	mode, gid, segment := "", "", ""
	stage, entries, cases := 0, 0, 0
	poolWait, clientWait := false, false
	for i, index := range indexes {
		end := len(decoded)
		if i+1 < len(indexes) {
			end = indexes[i+1][0]
		}
		block := decoded[index[0]:end]
		line := strings.SplitN(block, "\n", 2)[0]
		eventGID := decoded[index[2]:index[3]]
		for _, name := range []string{"pool_lock", "client_wait"} {
			if !strings.Contains(line, `Type="wkbench.calibration.`+name+`"`) {
				continue
			}
			if strings.Contains(line, " RegionBegin ") {
				if mode != "" {
					t.Fatal("overlapping calibration calls")
				}
				mode, gid, segment, stage, entries = name, eventGID, "", 0, 0
				poolWait, clientWait = false, false
			} else if strings.Contains(line, " RegionEnd ") {
				if mode != name || eventGID != gid || stage != 4 || segment != "" || entries != 1 || poolWait != (mode == "pool_lock") || clientWait != (mode == "client_wait") {
					t.Fatalf("segment ownership incomplete: case=%s stage=%d entries=%d pool_wait=%t client_wait=%t", mode, stage, entries, poolWait, clientWait)
				}
				mode = ""
				cases++
			}
		}
		for order, name := range []string{sessionSendLookupRegion, sessionSendClientRegion} {
			if !strings.Contains(line, `Type="`+name+`"`) {
				continue
			}
			if mode == "" || eventGID != gid {
				t.Fatal("production segment escaped its real TrySend goroutine")
			}
			if strings.Contains(line, " RegionBegin ") {
				if stage != order*2 || segment != "" {
					t.Fatal("production segments overlap or begin out of order")
				}
				segment, stage = name, stage+1
			} else if strings.Contains(line, " RegionEnd ") {
				if stage != order*2+1 || segment != name {
					t.Fatal("production segment end is unmatched")
				}
				segment, stage = "", stage+1
			}
		}
		if mode == "" {
			continue
		}
		if strings.Contains(line, `Category="session_send_calibration" Message="delegate_enter"`) {
			if eventGID != gid || segment != sessionSendClientRegion {
				t.Fatal("delegate entry is outside the client segment")
			}
			entries++
		}
		waiter := resource.FindStringSubmatch(line)
		if len(waiter) != 2 || waiter[1] != gid || !strings.Contains(line, "Running->Waiting") {
			continue
		}
		if strings.Contains(block, ".(*SessionPool).TrySend") && strings.Contains(line, `Reason="sync"`) && (strings.Contains(block, ".(*RWMutex).RLock") || strings.Contains(block, "runtime_SemacquireRWMutexR")) {
			if segment != sessionSendLookupRegion {
				t.Fatal("real pool lock wait is outside lookup segment")
			}
			poolWait = true
		}
		if strings.Contains(block, ".(*sessionSendTraceClient).TrySend") && strings.Contains(line, `Reason="chan receive"`) {
			if segment != sessionSendClientRegion {
				t.Fatal("controlled delegate wait is outside client segment")
			}
			clientWait = true
		}
	}
	if cases != 2 || mode != "" {
		t.Fatalf("complete calibration calls = %d, open=%q; want two closed calls", cases, mode)
	}
}
