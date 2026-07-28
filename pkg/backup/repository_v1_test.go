package backup_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestRepositoryMarkerV1RoundTripAndRejectsDifferentSlotCount(t *testing.T) {
	marker := backup.RepositoryMarker{
		Format:              backup.RepositoryFormat,
		Version:             backup.RepositoryVersion,
		SourceClusterID:     "cluster-a",
		HashSlotCount:       backup.DefaultHashSlotCount,
		CreatedAtUnixMillis: 1_800_000_000_000,
	}
	body, err := backup.MarshalRepositoryMarker(marker)
	if err != nil {
		t.Fatalf("MarshalRepositoryMarker(): %v", err)
	}
	loaded, err := backup.LoadRepositoryMarker(body)
	if err != nil {
		t.Fatalf("LoadRepositoryMarker(): %v", err)
	}
	if loaded != marker {
		t.Fatalf("loaded = %#v, want %#v", loaded, marker)
	}
	marker.HashSlotCount--
	if _, err := backup.MarshalRepositoryMarker(marker); err == nil {
		t.Fatal("MarshalRepositoryMarker(slot count) error = nil")
	}
}
