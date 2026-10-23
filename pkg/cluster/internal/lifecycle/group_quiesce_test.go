package lifecycle_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/internal/lifecycle"
)

type incompleteOwner struct{}

func (incompleteOwner) Error() string        { return "owner has not returned" }
func (incompleteOwner) StopIncomplete() bool { return true }

type quiesceResource struct {
	name                          string
	calls                         *[]string
	quiesceErr, stopErr, startErr error
}

func (r *quiesceResource) Start(context.Context) error {
	*r.calls = append(*r.calls, "start:"+r.name)
	return r.startErr
}
func (r *quiesceResource) Quiesce(context.Context) error {
	*r.calls = append(*r.calls, "quiesce:"+r.name)
	return r.quiesceErr
}
func (r *quiesceResource) Stop(context.Context) error {
	*r.calls = append(*r.calls, "stop:"+r.name)
	return r.stopErr
}

func TestGroupIncompleteQuiesceRetainsDependenciesForRetry(t *testing.T) {
	var calls []string
	entry := &quiesceResource{name: "entry", calls: &calls, quiesceErr: incompleteOwner{}}
	dependency := &quiesceResource{name: "dependency", calls: &calls}
	var g lifecycle.Group
	if err := g.Start(context.Background(), lifecycle.NamedResource{Resource: entry}, lifecycle.NamedResource{Resource: dependency}); err != nil {
		t.Fatal(err)
	}
	calls = nil
	var unfinished interface{ StopIncomplete() bool }
	if err := g.Stop(context.Background()); !errors.As(err, &unfinished) {
		t.Fatalf("incomplete owner lost: %v", err)
	}
	if !reflect.DeepEqual(calls, []string{"quiesce:entry"}) {
		t.Fatalf("dependency stopped before entry joined: %v", calls)
	}
	entry.quiesceErr = nil
	calls = nil
	if err := g.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []string{"quiesce:entry", "quiesce:dependency", "stop:dependency", "stop:entry"}) {
		t.Fatalf("retry order=%v", calls)
	}
	calls = nil
	if err := g.Stop(context.Background()); err != nil || len(calls) != 0 {
		t.Fatalf("joined resources stopped again: %v %v", calls, err)
	}
}

func TestGroupOrdinaryQuiesceAndStopErrorsStillCleanAllResources(t *testing.T) {
	var calls []string
	first, second := errors.New("quiesce diagnostic failure"), errors.New("ordinary stop failure")
	entry := &quiesceResource{name: "entry", calls: &calls, quiesceErr: first}
	dependency := &quiesceResource{name: "dependency", calls: &calls, stopErr: second}
	var g lifecycle.Group
	if err := g.Start(context.Background(), lifecycle.NamedResource{Resource: entry}, lifecycle.NamedResource{Resource: dependency}); err != nil {
		t.Fatal(err)
	}
	calls = nil
	err := g.Stop(context.Background())
	if !errors.Is(err, first) || !errors.Is(err, second) {
		t.Fatalf("diagnostics lost: %v", err)
	}
	if !reflect.DeepEqual(calls, []string{"quiesce:entry", "quiesce:dependency", "stop:dependency", "stop:entry"}) {
		t.Fatalf("ordinary error blocked cleanup: %v", calls)
	}
}

func TestGroupStartFailureRetainsIncompleteRollback(t *testing.T) {
	var calls []string
	boom := errors.New("start failed")
	entry := &quiesceResource{name: "entry", calls: &calls, quiesceErr: incompleteOwner{}}
	failed := &quiesceResource{name: "failed", calls: &calls, startErr: boom}
	var g lifecycle.Group
	err := g.Start(context.Background(), lifecycle.NamedResource{Resource: entry}, lifecycle.NamedResource{Resource: failed})
	var unfinished interface{ StopIncomplete() bool }
	if !errors.Is(err, boom) || !errors.As(err, &unfinished) {
		t.Fatalf("startup rollback discarded ownership: %v", err)
	}
	entry.quiesceErr = nil
	calls = nil
	if err := g.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []string{"quiesce:entry", "stop:entry"}) {
		t.Fatalf("rollback owner not retained: %v", calls)
	}
}

func TestGroupIncompleteStopRetainsOnlyUnjoinedPrefix(t *testing.T) {
	var calls []string
	entry := &quiesceResource{name: "entry", calls: &calls}
	owner := &quiesceResource{name: "owner", calls: &calls, stopErr: incompleteOwner{}}
	joined := &quiesceResource{name: "joined", calls: &calls}
	var g lifecycle.Group
	if err := g.Start(context.Background(), lifecycle.NamedResource{Resource: entry}, lifecycle.NamedResource{Resource: owner}, lifecycle.NamedResource{Resource: joined}); err != nil {
		t.Fatal(err)
	}
	calls = nil
	var unfinished interface{ StopIncomplete() bool }
	if err := g.Stop(context.Background()); !errors.As(err, &unfinished) {
		t.Fatalf("Stop lost incomplete owner: %v", err)
	}
	if !reflect.DeepEqual(calls, []string{"quiesce:entry", "quiesce:owner", "quiesce:joined", "stop:joined", "stop:owner"}) {
		t.Fatalf("Stop crossed owned dependency: %v", calls)
	}
	owner.stopErr = nil
	calls = nil
	if err := g.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []string{"quiesce:entry", "quiesce:owner", "stop:owner", "stop:entry"}) {
		t.Fatalf("retry did not retain exact unfinished prefix: %v", calls)
	}
}
