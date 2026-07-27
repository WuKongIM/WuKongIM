package keypackage

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/stretchr/testify/require"
)

func TestDeploymentKeyAuthorityRoundTripsDataKeysAndSignatures(t *testing.T) {
	const repositoryID = "wukongim-backup-production"

	body, metadata, err := GenerateDeploymentKeyPackage(repositoryID)
	require.NoError(t, err)
	require.NotEmpty(t, body)
	require.NotEmpty(t, metadata.PackageID)
	require.Equal(t, uint64(1), metadata.Revision)
	require.NotEmpty(t, metadata.ActiveWrappingKeyID)
	require.NotEmpty(t, metadata.ActiveSigningKeyID)

	authority, err := OpenDeploymentKeyAuthority(body, repositoryID)
	require.NoError(t, err)

	dataKey, err := authority.NewDataKey(context.Background())
	require.NoError(t, err)
	require.Len(t, dataKey.Plaintext, 32)
	require.Equal(t, metadata.ActiveWrappingKeyID, dataKey.Envelope.KeyID)
	require.NotEmpty(t, dataKey.Envelope.Nonce)
	require.NotEmpty(t, dataKey.Envelope.Value)

	wantPlaintext := append([]byte(nil), dataKey.Plaintext...)
	unwrapped, err := authority.OpenDataKey(
		context.Background(), dataKey.Envelope,
	)
	require.NoError(t, err)
	require.True(t, bytes.Equal(wantPlaintext, unwrapped))

	message := []byte("canonical backup manifest")
	signature, err := authority.Sign(context.Background(), message)
	require.NoError(t, err)
	require.Equal(t, "ED25519", signature.Algorithm)
	require.Equal(t, metadata.ActiveSigningKeyID, signature.KeyID)
	require.NoError(
		t,
		authority.Verify(context.Background(), signature, message),
	)
	require.Error(
		t,
		authority.Verify(
			context.Background(), signature,
			[]byte("different canonical manifest"),
		),
	)

	_, err = OpenDeploymentKeyAuthority(body, "different-repository")
	require.ErrorContains(t, err, "repository identity")
}

func TestLoadDeploymentKeyAuthorityDiscoversOnlyProtectedCredentials(
	t *testing.T,
) {
	const repositoryID = "wukongim-backup-production"

	body, _, err := GenerateDeploymentKeyPackage(repositoryID)
	require.NoError(t, err)
	credentialDirectory := t.TempDir()
	credentialPath := filepath.Join(
		credentialDirectory, DeploymentKeyPackageCredentialName,
	)
	require.NoError(t, os.WriteFile(credentialPath, body, 0o600))
	t.Setenv("CREDENTIALS_DIRECTORY", credentialDirectory)
	t.Setenv(DeploymentKeyPackageFileEnvironment, "")

	authority, err := LoadDeploymentKeyAuthority(
		context.Background(), repositoryID,
	)
	require.NoError(t, err)
	require.NotNil(t, authority)

	require.NoError(t, os.Chmod(credentialPath, 0o644))
	_, err = LoadDeploymentKeyAuthority(context.Background(), repositoryID)
	require.ErrorContains(t, err, "permissions")

	require.NoError(t, os.Remove(credentialPath))
	target := filepath.Join(credentialDirectory, "target")
	require.NoError(t, os.WriteFile(target, body, 0o600))
	require.NoError(t, os.Symlink(target, credentialPath))
	_, err = LoadDeploymentKeyAuthority(context.Background(), repositoryID)
	require.ErrorContains(t, err, "regular file")
}

func TestProtectedDeploymentFileSnapshotDetectsMutationAndReplacement(
	t *testing.T,
) {
	path := filepath.Join(t.TempDir(), "protected")
	require.NoError(t, os.WriteFile(path, []byte("secret"), 0o600))
	before, err := os.Lstat(path)
	require.NoError(t, err)
	file, err := os.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = file.Close() })
	opened, err := file.Stat()
	require.NoError(t, err)
	require.True(
		t, sameProtectedDeploymentFileSnapshot(before, opened),
	)

	changedAt := opened.ModTime().Add(time.Second)
	require.NoError(t, os.Chtimes(path, changedAt, changedAt))
	changed, err := file.Stat()
	require.NoError(t, err)
	require.False(
		t, sameProtectedDeploymentFileSnapshot(opened, changed),
	)

	replacement := filepath.Join(filepath.Dir(path), "replacement")
	require.NoError(t, os.WriteFile(replacement, []byte("secret"), 0o600))
	require.NoError(t, os.Rename(replacement, path))
	pathAfterReplacement, err := os.Lstat(path)
	require.NoError(t, err)
	require.False(
		t,
		sameProtectedDeploymentFileSnapshot(
			changed, pathAfterReplacement,
		),
	)
}

func TestDeploymentRecoveryKitRestoresExactPackage(t *testing.T) {
	const repositoryID = "wukongim-backup-production"

	body, metadata, err := GenerateDeploymentKeyPackage(repositoryID)
	require.NoError(t, err)
	kit, recoveryKey, kitMetadata, err := SealDeploymentRecoveryKit(body)
	require.NoError(t, err)
	require.NotEmpty(t, kit)
	require.Len(t, recoveryKey, 32)
	require.Equal(t, metadata, kitMetadata)

	restored, restoredMetadata, err := OpenDeploymentRecoveryKit(
		kit, recoveryKey,
	)
	require.NoError(t, err)
	require.Equal(t, metadata, restoredMetadata)
	require.Equal(t, body, restored)

	wrongKey := bytes.Repeat([]byte{0x7f}, 32)
	_, _, err = OpenDeploymentRecoveryKit(kit, wrongKey)
	require.ErrorContains(t, err, "authentication")
}

func TestDeploymentRecoveryKitRejectsInvalidPackageMaterial(t *testing.T) {
	const repositoryID = "wukongim-backup-production"

	body, _, err := GenerateDeploymentKeyPackage(repositoryID)
	require.NoError(t, err)
	value, err := decodeDeploymentKeyPackage(body)
	require.NoError(t, err)
	value.WrappingKeys[0].Material = []byte("not-an-aes-256-key")
	invalidBody, _, err := encodeDeploymentKeyPackage(value)
	require.NoError(t, err)

	_, _, _, err = SealDeploymentRecoveryKit(invalidBody)
	require.ErrorContains(t, err, "AES-256")
}

func TestDeploymentKeyRotationStagesThenActivatesWithoutLosingReads(
	t *testing.T,
) {
	const repositoryID = "wukongim-backup-production"

	initialBody, initialMetadata, err := GenerateDeploymentKeyPackage(
		repositoryID,
	)
	require.NoError(t, err)
	initialAuthority, err := OpenDeploymentKeyAuthority(
		initialBody, repositoryID,
	)
	require.NoError(t, err)
	oldDataKey, err := initialAuthority.NewDataKey(context.Background())
	require.NoError(t, err)
	oldSignature, err := initialAuthority.Sign(
		context.Background(), []byte("old manifest"),
	)
	require.NoError(t, err)

	stagedBody, stagedMetadata, err := StageDeploymentKeyRotation(initialBody)
	require.NoError(t, err)
	require.Equal(t, uint64(2), stagedMetadata.Revision)
	require.Equal(
		t,
		initialMetadata.ActiveWrappingKeyID,
		stagedMetadata.ActiveWrappingKeyID,
	)
	require.Equal(
		t,
		initialMetadata.ActiveSigningKeyID,
		stagedMetadata.ActiveSigningKeyID,
	)
	stagedAuthority, err := OpenDeploymentKeyAuthority(
		stagedBody, repositoryID,
	)
	require.NoError(t, err)

	activatedBody, activatedMetadata, err := ActivateDeploymentKeyRotation(
		stagedBody,
	)
	require.NoError(t, err)
	require.Equal(t, uint64(3), activatedMetadata.Revision)
	require.NotEqual(
		t,
		initialMetadata.ActiveWrappingKeyID,
		activatedMetadata.ActiveWrappingKeyID,
	)
	require.NotEqual(
		t,
		initialMetadata.ActiveSigningKeyID,
		activatedMetadata.ActiveSigningKeyID,
	)
	activatedAuthority, err := OpenDeploymentKeyAuthority(
		activatedBody, repositoryID,
	)
	require.NoError(t, err)

	unwrapped, err := activatedAuthority.OpenDataKey(
		context.Background(), oldDataKey.Envelope,
	)
	require.NoError(t, err)
	require.Equal(t, oldDataKey.Plaintext, unwrapped)
	require.NoError(
		t,
		activatedAuthority.Verify(
			context.Background(),
			oldSignature,
			[]byte("old manifest"),
		),
	)

	newDataKey, err := activatedAuthority.NewDataKey(context.Background())
	require.NoError(t, err)
	_, err = stagedAuthority.OpenDataKey(
		context.Background(), newDataKey.Envelope,
	)
	require.NoError(t, err)
	newSignature, err := activatedAuthority.Sign(
		context.Background(), []byte("new manifest"),
	)
	require.NoError(t, err)
	require.NoError(
		t,
		stagedAuthority.Verify(
			context.Background(),
			newSignature,
			[]byte("new manifest"),
		),
	)
}

func TestDeploymentKeyAuthorityRejectsActiveIDsThatPointToPendingKeys(
	t *testing.T,
) {
	const repositoryID = "wukongim-backup-production"

	initialBody, _, err := GenerateDeploymentKeyPackage(repositoryID)
	require.NoError(t, err)
	stagedBody, _, err := StageDeploymentKeyRotation(initialBody)
	require.NoError(t, err)
	value, err := decodeDeploymentKeyPackage(stagedBody)
	require.NoError(t, err)
	for _, key := range value.WrappingKeys {
		if key.State == "pending" {
			value.ActiveWrappingKeyID = key.ID
		}
	}
	for _, key := range value.SigningKeys {
		if key.State == "pending" {
			value.ActiveSigningKeyID = key.ID
		}
	}
	invalidBody, _, err := encodeDeploymentKeyPackage(value)
	require.NoError(t, err)

	_, err = OpenDeploymentKeyAuthority(invalidBody, repositoryID)
	require.ErrorContains(t, err, "active wrapping key")
}

func TestDeploymentKeyAuthorityRejectsWrappingMaterialBitFlip(t *testing.T) {
	const repositoryID = "wukongim-backup-production"

	body, _, err := GenerateDeploymentKeyPackage(repositoryID)
	require.NoError(t, err)
	value, err := decodeDeploymentKeyPackage(body)
	require.NoError(t, err)
	value.WrappingKeys[0].Material[0] ^= 0x01
	invalidBody, _, err := encodeDeploymentKeyPackage(value)
	require.NoError(t, err)

	_, err = OpenDeploymentKeyAuthority(invalidBody, repositoryID)
	require.ErrorContains(t, err, "wrapping key identity mismatch")
}

func TestDeploymentKeyAuthorityRejectsAuthenticatedMetadataBitFlip(
	t *testing.T,
) {
	const repositoryID = "wukongim-backup-production"

	body, _, err := GenerateDeploymentKeyPackage(repositoryID)
	require.NoError(t, err)
	value, err := decodeDeploymentKeyPackage(body)
	require.NoError(t, err)
	value.PackageID += "-changed"
	invalidBody, err := json.Marshal(value)
	require.NoError(t, err)

	_, err = OpenDeploymentKeyAuthority(invalidBody, repositoryID)
	require.ErrorContains(t, err, "package authentication")
}

func TestRepositoryPinnedAuthorityRejectsPackageSubstitutionAndRollback(
	t *testing.T,
) {
	const repositoryID = "wukongim-backup-production"
	root := t.TempDir()
	primary, err := backupinfra.NewFileRepository(
		"primary", filepath.Join(root, "primary"),
	)
	require.NoError(t, err)
	secondary, err := backupinfra.NewFileRepository(
		"secondary", filepath.Join(root, "secondary"),
	)
	require.NoError(t, err)

	initialBody, _, err := GenerateDeploymentKeyPackage(repositoryID)
	require.NoError(t, err)
	initial, err := OpenDeploymentKeyAuthority(initialBody, repositoryID)
	require.NoError(t, err)
	initialPinned, err := NewRepositoryPinnedAuthority(
		initial, primary, secondary, func() bool { return true },
	)
	require.NoError(t, err)
	require.NoError(t, initialPinned.Check(context.Background()))

	stagedBody, _, err := StageDeploymentKeyRotation(initialBody)
	require.NoError(t, err)
	staged, err := OpenDeploymentKeyAuthority(stagedBody, repositoryID)
	require.NoError(t, err)
	stagedPinned, err := NewRepositoryPinnedAuthority(
		staged, primary, secondary, func() bool { return true },
	)
	require.NoError(t, err)
	require.NoError(t, stagedPinned.Check(context.Background()))

	activeBody, _, err := ActivateDeploymentKeyRotation(stagedBody)
	require.NoError(t, err)
	active, err := OpenDeploymentKeyAuthority(activeBody, repositoryID)
	require.NoError(t, err)
	activePinned, err := NewRepositoryPinnedAuthority(
		active, primary, secondary, func() bool { return true },
	)
	require.NoError(t, err)
	require.NoError(t, activePinned.Check(context.Background()))
	require.NoError(t, activePinned.Check(context.Background()))

	require.ErrorContains(
		t,
		initialPinned.Check(context.Background()),
		"newer active revision 3",
	)
	require.ErrorContains(
		t,
		stagedPinned.Check(context.Background()),
		"rollback detected",
	)

	replacementBody, _, err := GenerateDeploymentKeyPackage(repositoryID)
	require.NoError(t, err)
	replacement, err := OpenDeploymentKeyAuthority(
		replacementBody, repositoryID,
	)
	require.NoError(t, err)
	replacementPinned, err := NewRepositoryPinnedAuthority(
		replacement, primary, secondary, func() bool { return true },
	)
	require.NoError(t, err)
	require.ErrorContains(
		t,
		replacementPinned.Check(context.Background()),
		"repository pin metadata mismatch",
	)
}

func TestRepositoryPinnedAuthorityAllowsOnlyFencedPublisherToCreateRoot(
	t *testing.T,
) {
	const repositoryID = "wukongim-backup-production"
	root := t.TempDir()
	primary, err := backupinfra.NewFileRepository(
		"primary", filepath.Join(root, "primary"),
	)
	require.NoError(t, err)
	secondary, err := backupinfra.NewFileRepository(
		"secondary", filepath.Join(root, "secondary"),
	)
	require.NoError(t, err)
	body, _, err := GenerateDeploymentKeyPackage(repositoryID)
	require.NoError(t, err)

	followerAuthority, err := OpenDeploymentKeyAuthority(body, repositoryID)
	require.NoError(t, err)
	follower, err := NewRepositoryPinnedAuthority(
		followerAuthority, primary, secondary, func() bool { return false },
	)
	require.NoError(t, err)
	require.ErrorIs(
		t, follower.Check(context.Background()), ErrRepositoryPinPending,
	)
	_, err = primary.Stat(context.Background(), deploymentKeyRootPinKey)
	require.ErrorIs(t, err, backupartifact.ErrObjectNotFound)

	leaderAuthority, err := OpenDeploymentKeyAuthority(body, repositoryID)
	require.NoError(t, err)
	leader, err := NewRepositoryPinnedAuthority(
		leaderAuthority, primary, secondary, func() bool { return true },
	)
	require.NoError(t, err)
	require.NoError(t, leader.Check(context.Background()))
	require.NoError(t, follower.Check(context.Background()))

	_, err = leader.NewDataKey(context.Background())
	require.ErrorContains(t, err, "runtime qualification is required")
	leader.Qualify()
	dataKey, err := leader.NewDataKey(context.Background())
	require.NoError(t, err)
	leader.Invalidate()
	_, err = leader.OpenDataKey(context.Background(), dataKey.Envelope)
	require.ErrorContains(t, err, "runtime qualification is required")
}
