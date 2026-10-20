package replication

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

func TestBatchingRecoveryProbeDispatcherPreservesLocalStorageErrors(t *testing.T) {
	storageErr := errors.New("replica storage read failed")
	tests := []struct {
		name     string
		batchErr error
		itemErr  error
	}{
		{name: "batch_error", batchErr: fmt.Errorf("load batch: %w", storageErr)},
		{name: "item_error", itemErr: fmt.Errorf("load item: %w", storageErr)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executor := &manualPeerExecutor{}
			dispatcher := &batchingRecoveryProbeDispatcher{
				local: 1, ownerContext: context.Background(), localTimeout: time.Minute,
				store: &localRecoveryProbeErrorStore{
					recordingReplicaStore: &recordingReplicaStore{
						loadResult: LoadBatchResult{Items: []LoadResult{{Err: test.itemErr}}},
					},
					batchErr: test.batchErr,
				},
				executor: executor,
			}
			var got ProbeResult
			var probeErr error
			completed := false
			err := dispatcher.submitRecoveryProbe(context.Background(), recoveryProbeQuery{
				ChannelKey: "1:storage-error", ChannelID: ch.ChannelID{ID: "storage-error", Type: 1},
				Leader: 1, Voter: 1,
			}, func(result ProbeResult, err error) {
				got, probeErr, completed = result, err, true
			})
			if err != nil {
				t.Fatalf("submitRecoveryProbe() error = %v", err)
			}
			executor.RunNext()
			if !completed {
				t.Fatal("admitted local probe did not complete")
			}
			if !errors.Is(probeErr, storageErr) {
				t.Errorf("local probe error = %v, want original storage cause %v", probeErr, storageErr)
			}
			if errors.Is(probeErr, ch.ErrNotReady) {
				t.Errorf("local storage failure acquired temporary readiness identity: %v", probeErr)
			}
			if !reflect.DeepEqual(got, ProbeResult{}) {
				t.Errorf("local probe = %+v, want no proof on storage failure", got)
			}
		})
	}
}

// localRecoveryProbeErrorStore injects a batch-level failure at the storage port.
// Item-level failures use the existing recording store without another adapter.
type localRecoveryProbeErrorStore struct {
	*recordingReplicaStore
	batchErr error
}

func (s *localRecoveryProbeErrorStore) Load(ctx context.Context, batch LoadBatch) (LoadBatchResult, error) {
	if s.batchErr != nil {
		return LoadBatchResult{}, s.batchErr
	}
	return s.recordingReplicaStore.Load(ctx, batch)
}
