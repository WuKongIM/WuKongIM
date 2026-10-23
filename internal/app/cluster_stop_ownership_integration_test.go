//go:build integration

package app

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type appUnjoinedRPC struct{}

func (appUnjoinedRPC) Error() string        { return "cluster RPC still owned" }
func (appUnjoinedRPC) StopIncomplete() bool { return true }

func TestClusterIncompleteStopRetainsAuditDependency(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "stop", true: "rollback"}[rollback], func(t *testing.T) {
			var calls []string
			cluster := &fakeCluster{calls: &calls, stopErr: appUnjoinedRPC{}}
			audit := newControllerTaskAuditRuntime(filepath.Join(t.TempDir(), controllerTaskAuditFileName), wklog.NewNop())
			t.Cleanup(func() { _ = audit.Close() })
			a := &App{started: true, clusterStarted: true, cluster: cluster, logger: wklog.NewNop(), controllerTaskAudit: audit}
			closeApp := a.Stop
			if rollback {
				closeApp = a.rollbackStarted
			}
			var unfinished interface{ StopIncomplete() bool }
			if err := closeApp(context.Background()); !errors.As(err, &unfinished) {
				t.Fatalf("Stop lost ownership error: %v", err)
			}
			if _, err := audit.ListControllerTaskAudits(context.Background(), managementusecase.ControllerTaskAuditListRequest{}); err != nil {
				t.Errorf("live cluster lost its audit dependency: %v", err)
			}
			if !a.clusterStarted {
				t.Error("live cluster ownership discarded")
			}
			cluster.stopErr = nil
			if err := closeApp(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, err := audit.ListControllerTaskAudits(context.Background(), managementusecase.ControllerTaskAuditListRequest{}); err == nil {
				t.Error("joined Stop left audit open")
			}
			if !reflect.DeepEqual(calls, []string{"cluster.stop", "cluster.stop"}) {
				t.Fatalf("retry did not join original cluster: %v", calls)
			}
		})
	}
}

func TestClusterOrdinaryStopErrorStillClosesAudit(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "stop", true: "rollback"}[rollback], func(t *testing.T) {
			ordinary := errors.New("ordinary cluster cleanup diagnostic")
			audit := newControllerTaskAuditRuntime(filepath.Join(t.TempDir(), controllerTaskAuditFileName), wklog.NewNop())
			t.Cleanup(func() { _ = audit.Close() })
			a := &App{started: true, clusterStarted: true, cluster: &fakeCluster{stopErr: ordinary}, logger: wklog.NewNop(), controllerTaskAudit: audit}
			closeApp := a.Stop
			if rollback {
				closeApp = a.rollbackStarted
			}
			if err := closeApp(context.Background()); !errors.Is(err, ordinary) {
				t.Fatalf("Stop lost ordinary error: %v", err)
			}
			if _, err := audit.ListControllerTaskAudits(context.Background(), managementusecase.ControllerTaskAuditListRequest{}); err == nil {
				t.Error("ordinary diagnostic blocked audit cleanup")
			}
		})
	}
}

func TestClusterIncompleteStartupRemainsReachableFromStop(t *testing.T) {
	var calls []string
	cluster := &fakeCluster{calls: &calls, startErr: appUnjoinedRPC{}, stopErr: appUnjoinedRPC{}}
	a, err := newTestApp(t, Config{}, WithCluster(cluster), WithLogger(wklog.NewNop()))
	if err != nil {
		t.Fatal(err)
	}
	audit := newControllerTaskAuditRuntime(filepath.Join(t.TempDir(), controllerTaskAuditFileName), wklog.NewNop())
	a.controllerTaskAudit = audit
	t.Cleanup(func() { cluster.stopErr = nil; _ = a.Stop(context.Background()); _ = audit.Close() })
	var unfinished interface{ StopIncomplete() bool }
	if err := a.Start(context.Background()); !errors.As(err, &unfinished) {
		t.Fatalf("startup lost ownership: %v", err)
	}
	if err := a.Stop(context.Background()); !errors.As(err, &unfinished) {
		t.Errorf("never-started app lost cluster owner: %v", err)
	}
	if _, err := audit.ListControllerTaskAudits(context.Background(), managementusecase.ControllerTaskAuditListRequest{}); err != nil {
		t.Errorf("incomplete startup audit closed: %v", err)
	}
	cluster.stopErr = nil
	if err := a.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []string{"cluster.start", "cluster.stop", "cluster.stop"}) {
		t.Fatalf("failed startup owner unreachable: %v", calls)
	}
	if _, err := audit.ListControllerTaskAudits(context.Background(), managementusecase.ControllerTaskAuditListRequest{}); err == nil {
		t.Error("joined startup rollback left audit open")
	}
}
