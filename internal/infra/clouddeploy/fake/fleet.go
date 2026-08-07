// Package fake implements a deterministic provider-free deployment Fleet.
package fake

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/WuKongIM/WuKongIM/internal/usecase/clouddeploy"
)

var ErrInjectedFailure = errors.New("internal/infra/clouddeploy/fake: injected failure")

// Options supplies one bounded readiness snapshot and exact failed operation.
type Options struct {
	Snapshot      clouddeploy.ReadinessSnapshot
	FailOperation string
}

// Fleet records deterministic deployment operations without touching a host.
type Fleet struct {
	mu            sync.Mutex
	snapshot      clouddeploy.ReadinessSnapshot
	failOperation string
	operations    []string
}

// New returns an empty fake Fleet.
func New(options Options) *Fleet {
	return &Fleet{snapshot: options.Snapshot, failOperation: options.FailOperation}
}

// Operations returns an isolated copy of the ordered operation log.
func (f *Fleet) Operations() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.operations)
}

func (f *Fleet) StageBundle(_ context.Context, host clouddeploy.HostPlan, _ string) error {
	return f.record("stage:" + host.Role)
}

func (f *Fleet) RelayBundle(_ context.Context, load, host clouddeploy.HostPlan, _ string) error {
	return f.record("relay:" + load.Role + ":" + host.Role)
}

func (f *Fleet) VerifyBundle(_ context.Context, host clouddeploy.HostPlan, _ string) error {
	return f.record("verify:" + host.Role)
}

func (f *Fleet) PrepareHost(_ context.Context, host clouddeploy.HostPlan) error {
	return f.record("prepare:" + host.Role)
}

func (f *Fleet) ActivateHost(_ context.Context, host clouddeploy.HostPlan) error {
	return f.record("activate:" + host.Role)
}

func (f *Fleet) Snapshot(_ context.Context, _ clouddeploy.DeploymentPlan) (clouddeploy.ReadinessSnapshot, error) {
	if err := f.record("snapshot"); err != nil {
		return clouddeploy.ReadinessSnapshot{}, err
	}
	return f.snapshot, nil
}

func (f *Fleet) record(operation string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.operations = append(f.operations, operation)
	if operation == f.failOperation {
		return fmt.Errorf("%w: %s", ErrInjectedFailure, operation)
	}
	return nil
}
