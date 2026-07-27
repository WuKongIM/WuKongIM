package backup_test

import (
	"context"
	"crypto/ed25519"
	"fmt"

	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

type testEd25519Signer struct {
	privateKey ed25519.PrivateKey
}

func (s testEd25519Signer) Sign(
	_ context.Context,
	message []byte,
) (backupartifact.ManifestSignature, error) {
	return backupartifact.ManifestSignature{
		Algorithm: "ed25519", KeyID: "ed25519:test",
		Value: ed25519.Sign(s.privateKey, message),
	}, nil
}

func (s testEd25519Signer) Verify(
	_ context.Context,
	signature backupartifact.ManifestSignature,
	message []byte,
) error {
	if signature.Algorithm != "ed25519" ||
		!ed25519.Verify(
			s.privateKey.Public().(ed25519.PublicKey),
			message, signature.Value,
		) {
		return fmt.Errorf("invalid signature")
	}
	return nil
}

type staticRestoreTarget struct {
	state backupinfra.RestoreTargetState
	err   error
}

func (t staticRestoreTarget) InspectRestoreTarget(
	context.Context,
) (backupinfra.RestoreTargetState, error) {
	return t.state, t.err
}
