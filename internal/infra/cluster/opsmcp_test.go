package cluster

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
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

type opsMCPStateNodeStub struct {
	snapshot control.Snapshot
}

func (s *opsMCPStateNodeStub) LocalControlSnapshot(context.Context) (control.Snapshot, error) {
	return s.snapshot, nil
}
