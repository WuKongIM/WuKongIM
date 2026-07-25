package backup_test

import (
	"context"
	"crypto/ed25519"
	"testing"

	backup "github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestGenerationVectorRoundTripHasStableContentIdentity(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	signer := ed25519ManifestSigner{privateKey: privateKey}
	first, err := backup.NewGenerationVector([]string{
		"rebase-00000-00000000000000000001",
		"rebase-00001-00000000000000000002",
	})
	if err != nil {
		t.Fatalf("NewGenerationVector() error = %v", err)
	}
	retry, err := backup.NewGenerationVector(first.Generations)
	if err != nil {
		t.Fatalf("NewGenerationVector(retry) error = %v", err)
	}
	if retry.ID != first.ID {
		t.Fatalf("content identity changed: %q != %q", retry.ID, first.ID)
	}
	signed, err := backup.SignGenerationVector(context.Background(), first, signer, "signing-key")
	if err != nil {
		t.Fatalf("SignGenerationVector() error = %v", err)
	}
	body, err := backup.MarshalGenerationVector(signed)
	if err != nil {
		t.Fatalf("MarshalGenerationVector() error = %v", err)
	}
	loaded, err := backup.LoadGenerationVector(context.Background(), body, signer)
	if err != nil {
		t.Fatalf("LoadGenerationVector() error = %v", err)
	}
	if loaded.ID != first.ID || len(loaded.Generations) != 2 ||
		loaded.Generations[1] != first.Generations[1] {
		t.Fatalf("loaded vector = %+v", loaded)
	}

	changed, err := backup.NewGenerationVector([]string{
		first.Generations[0],
		"rebase-00001-00000000000000000003",
	})
	if err != nil {
		t.Fatalf("NewGenerationVector(changed) error = %v", err)
	}
	if changed.ID == first.ID {
		t.Fatal("changed vector reused content identity")
	}
}
