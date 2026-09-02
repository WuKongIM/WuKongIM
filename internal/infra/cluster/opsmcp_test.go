package cluster

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	controller "github.com/WuKongIM/WuKongIM/pkg/controller"
)

func TestOpsMCPStateReaderProjectsDetachedCredentialDigests(t *testing.T) {
	source := &opsMCPStateNodeStub{snapshot: control.Snapshot{
		Revision: 4,
		OpsMCP: &control.OpsMCPState{
			Enabled: true, OwnerNodeID: 2,
			Credentials: []control.OpsMCPCredential{{ID: "a", DigestSHA256: "digest"}},
		},
	}}
	got, err := NewOpsMCPStateReader(source).OpsMCPDesiredState(context.Background())
	if err != nil {
		t.Fatalf("OpsMCPDesiredState() error = %v", err)
	}
	if !got.Enabled || got.OwnerNodeID != 2 || got.Revision != 4 || got.Credentials[0].DigestSHA256 != "digest" {
		t.Fatalf("state = %#v", got)
	}
	got.Credentials[0].DigestSHA256 = "changed"
	if source.snapshot.OpsMCP.Credentials[0].DigestSHA256 != "digest" {
		t.Fatal("state reader aliased control snapshot")
	}
}

func TestManagementOpsMCPWriterPreservesRevisionFencedReplacement(t *testing.T) {
	t.Parallel()

	node := &opsMCPWriterNodeStub{}
	writer := NewManagementOpsMCPWriter(node)
	replacement := controller.OpsMCPState{
		Enabled: true, OwnerNodeID: 7, ProfileFenceUntilUnixMillis: 9,
		Credentials: []controller.OpsMCPCredential{{ID: "credential-1", DigestSHA256: "digest"}},
	}
	if err := writer.ReplaceOpsMCPState(context.Background(), 8, replacement); err != nil {
		t.Fatalf("ReplaceOpsMCPState() error = %v", err)
	}
	if node.expectedRevision != 8 || !reflect.DeepEqual(node.replacement, replacement) {
		t.Fatalf("replacement fence/state = %d/%#v", node.expectedRevision, node.replacement)
	}

	var nilWriter *ManagementOpsMCPWriter
	if err := nilWriter.ReplaceOpsMCPState(context.Background(), 8, replacement); err != controller.ErrNotStarted {
		t.Fatalf("nil writer error = %v, want %v", err, controller.ErrNotStarted)
	}
}

func TestOpsMCPStateReaderDistinguishesDisabledStateFromUnwiredSource(t *testing.T) {
	t.Parallel()

	disabled, err := NewOpsMCPStateReader(&opsMCPStateNodeStub{snapshot: control.Snapshot{Revision: 12}}).OpsMCPDesiredState(context.Background())
	if err != nil || disabled.Enabled || disabled.Revision != 12 || disabled.Credentials == nil || len(disabled.Credentials) != 0 {
		t.Fatalf("disabled desired state = %#v err=%v", disabled, err)
	}
	var reader *OpsMCPStateReader
	if _, err := reader.OpsMCPDesiredState(context.Background()); !errors.Is(err, controller.ErrNotStarted) {
		t.Fatalf("unwired desired state error = %v, want %v", err, controller.ErrNotStarted)
	}
}

type opsMCPStateNodeStub struct {
	snapshot control.Snapshot
}

type opsMCPWriterNodeStub struct {
	expectedRevision uint64
	replacement      controller.OpsMCPState
}

func (s *opsMCPWriterNodeStub) ReplaceOpsMCPState(_ context.Context, expectedRevision uint64, replacement controller.OpsMCPState) error {
	s.expectedRevision, s.replacement = expectedRevision, replacement
	return nil
}

func (s *opsMCPStateNodeStub) LocalControlSnapshot(context.Context) (control.Snapshot, error) {
	return s.snapshot, nil
}
