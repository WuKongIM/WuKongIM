package backup_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	"github.com/stretchr/testify/require"
)

func TestBackupDoctorChecksBothRepositoriesKMSStagingAndUTC(t *testing.T) {
	now := time.Unix(1710000000, 0).UTC()
	primary := &fakeDoctorCheck{}
	secondary := &fakeDoctorCheck{}
	kms := &fakeKMSDoctor{}
	doctor, err := backupinfra.NewDoctor(backupinfra.DoctorOptions{
		Primary: primary, Secondary: secondary, KMS: kms, EncryptionKey: "enc", SigningKey: "sign",
		StagingDir: filepath.Join(t.TempDir(), "staging"), ApplicationDir: filepath.Join(t.TempDir(), "data"), StagingMaxBytes: 1,
		ClockProbes: []backupinfra.ClockProbe{fakeClockProbe{value: now.Add(30 * time.Second)}}, Now: func() time.Time { return now }, MaxClockSkew: time.Minute,
	})
	require.NoError(t, err)
	require.NoError(t, doctor.Check(context.Background()))
	require.Equal(t, 1, primary.calls)
	require.Equal(t, 1, secondary.calls)
	require.Equal(t, "enc", kms.encryptionKey)
	require.Equal(t, "sign", kms.signingKey)
}

func TestBackupDoctorRejectsOverlappingStagingAndClockSkew(t *testing.T) {
	base := t.TempDir()
	doctor, err := backupinfra.NewDoctor(backupinfra.DoctorOptions{
		Primary: &fakeDoctorCheck{}, Secondary: &fakeDoctorCheck{}, KMS: &fakeKMSDoctor{}, EncryptionKey: "enc", SigningKey: "sign",
		StagingDir: filepath.Join(base, "data", "backup"), ApplicationDir: filepath.Join(base, "data"), StagingMaxBytes: 1,
		ClockProbes: []backupinfra.ClockProbe{fakeClockProbe{value: time.Now()}},
	})
	require.NoError(t, err)
	require.ErrorContains(t, doctor.Check(context.Background()), "must not overlap")

	separate, err := backupinfra.NewDoctor(backupinfra.DoctorOptions{
		Primary: &fakeDoctorCheck{}, Secondary: &fakeDoctorCheck{}, KMS: &fakeKMSDoctor{}, EncryptionKey: "enc", SigningKey: "sign",
		StagingDir: filepath.Join(base, "staging"), ApplicationDir: filepath.Join(base, "data"), StagingMaxBytes: 1,
		ClockProbes: []backupinfra.ClockProbe{fakeClockProbe{value: time.Unix(1, 0)}}, Now: func() time.Time { return time.Unix(1000, 0) }, MaxClockSkew: time.Second,
	})
	require.NoError(t, err)
	require.ErrorContains(t, separate.Check(context.Background()), "UTC skew")
}

type fakeDoctorCheck struct {
	calls int
	err   error
}

func (f *fakeDoctorCheck) Check(context.Context) error { f.calls++; return f.err }

type fakeKMSDoctor struct {
	encryptionKey, signingKey string
	err                       error
}

func (f *fakeKMSDoctor) Check(_ context.Context, encryptionKey, signingKey string) error {
	f.encryptionKey, f.signingKey = encryptionKey, signingKey
	return f.err
}

type fakeClockProbe struct {
	value time.Time
	err   error
}

func (f fakeClockProbe) UTC(context.Context) (time.Time, error) { return f.value, f.err }
