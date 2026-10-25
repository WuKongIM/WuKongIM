package app

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type recoveryCloserProbe struct {
	calls *[]string
	err   error
}

func (r *recoveryCloserProbe) Close(context.Context) error {
	*r.calls = append(*r.calls, "recovery.close")
	return r.err
}

func TestRecoveryDrainPrecedesClusterCloseAndCanRetry(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "stop", true: "rollback"}[rollback], func(t *testing.T) {
			var calls []string
			blocked := errors.New("recovery owners still active")
			r := &recoveryCloserProbe{calls: &calls, err: blocked}
			a := &App{started: true, clusterStarted: true, cluster: &fakeCluster{calls: &calls}, logger: wklog.NewNop(), presenceRecovery: r}
			closeApp := a.Stop
			if rollback {
				closeApp = a.rollbackStarted
			}
			if err := closeApp(context.Background()); !errors.Is(err, blocked) {
				t.Fatalf("drain outcome=%v", err)
			}
			if !reflect.DeepEqual(calls, []string{"recovery.close"}) || !a.clusterStarted {
				t.Fatalf("dependency closed before ownership drained: %v", calls)
			}
			r.err = nil
			if err := closeApp(context.Background()); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(calls, []string{"recovery.close", "recovery.close", "cluster.stop"}) {
				t.Fatalf("retry lifecycle=%v", calls)
			}
		})
	}
}

func TestRecoveryClosesEvenWhenApplicationNeverStarted(t *testing.T) {
	var calls []string
	a := &App{logger: wklog.NewNop(), presenceRecovery: &recoveryCloserProbe{calls: &calls}}
	if err := a.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []string{"recovery.close"}) {
		t.Fatalf("unstarted recovery leaked: %v", calls)
	}
}

func TestRecoveryRecreatedAfterSuccessfulStartupRollback(t *testing.T) {
	var calls []string
	failed := errors.New("entry start failed")
	gateway := &fakeGateway{calls: &calls, startErr: failed}
	a, err := newTestApp(t, Config{}, WithCluster(&fakeCluster{calls: &calls}), WithGateway(gateway), WithLogger(wklog.NewNop()))
	if err != nil {
		t.Fatal(err)
	}
	a.presenceRecovery = &recoveryCloserProbe{calls: &calls}
	a.reopenPresenceRecovery = func() {
		calls = append(calls, "recovery.reopen")
		a.presenceRecovery = &recoveryCloserProbe{calls: &calls}
	}
	if err := a.Start(context.Background()); !errors.Is(err, failed) {
		t.Fatalf("first startup=%v", err)
	}
	if a.presenceRecovery != nil {
		t.Fatal("successful rollback retained a closed cohort")
	}
	calls = nil
	gateway.startErr = nil
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(calls) < 2 || calls[0] != "recovery.reopen" || calls[1] != "cluster.start" {
		t.Fatalf("startup recreated owners too late: %v", calls)
	}
	if err := a.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}
