package backup_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	"github.com/stretchr/testify/require"
)

func TestBackupDoctorChecksRepositoriesKeyAuthorityStagingAndUTC(t *testing.T) {
	now := time.Unix(1710000000, 0).UTC()
	primary := &fakeDoctorCheck{}
	secondary := &fakeDoctorCheck{}
	keyAuthority := &fakeKeyAuthorityDoctor{}
	doctor, err := backupinfra.NewDoctor(backupinfra.DoctorOptions{
		Primary: primary, Secondary: secondary,
		KeyAuthority: keyAuthority,
		StagingDir:   filepath.Join(t.TempDir(), "staging"), ApplicationDir: filepath.Join(t.TempDir(), "data"), StagingMaxBytes: 1,
		ClockProbes: []backupinfra.ClockProbe{fakeClockProbe{value: now.Add(30 * time.Second)}}, Now: func() time.Time { return now }, MaxClockSkew: time.Minute,
	})
	require.NoError(t, err)
	report, err := doctor.Check(context.Background())
	require.NoError(t, err)
	require.Equal(t, backupusecase.HealthHealthy, report.Primary)
	require.Equal(t, backupusecase.HealthHealthy, report.Secondary)
	require.Equal(t, backupusecase.HealthHealthy, report.KeyAuthority)
	require.Equal(t, backupusecase.HealthHealthy, report.Staging)
	require.Equal(t, backupusecase.HealthHealthy, report.UTC)
	require.Equal(t, 1, primary.calls)
	require.Equal(t, 1, secondary.calls)
	require.Equal(t, 1, keyAuthority.calls)
}

func TestBackupDoctorRejectsOverlappingStagingAndClockSkew(t *testing.T) {
	base := t.TempDir()
	doctor, err := backupinfra.NewDoctor(backupinfra.DoctorOptions{
		Primary: &fakeDoctorCheck{}, Secondary: &fakeDoctorCheck{},
		KeyAuthority: &fakeKeyAuthorityDoctor{},
		StagingDir:   filepath.Join(base, "data", "backup"), ApplicationDir: filepath.Join(base, "data"), StagingMaxBytes: 1,
		ClockProbes: []backupinfra.ClockProbe{fakeClockProbe{value: time.Now()}},
	})
	require.NoError(t, err)
	_, checkErr := doctor.Check(context.Background())
	require.ErrorContains(t, checkErr, "must not overlap")

	separate, err := backupinfra.NewDoctor(backupinfra.DoctorOptions{
		Primary: &fakeDoctorCheck{}, Secondary: &fakeDoctorCheck{},
		KeyAuthority: &fakeKeyAuthorityDoctor{},
		StagingDir:   filepath.Join(base, "staging"), ApplicationDir: filepath.Join(base, "data"), StagingMaxBytes: 1,
		ClockProbes: []backupinfra.ClockProbe{fakeClockProbe{value: time.Unix(1, 0)}}, Now: func() time.Time { return time.Unix(1000, 0) }, MaxClockSkew: time.Second,
	})
	require.NoError(t, err)
	_, checkErr = separate.Check(context.Background())
	require.ErrorContains(t, checkErr, "UTC skew")
}

func TestBackupDoctorDoesNotCheckKeysBeforeRepositoriesQualify(t *testing.T) {
	primary := &fakeDoctorCheck{err: errors.New("ObjectWorm is disabled")}
	keyAuthority := &fakeKeyAuthorityDoctor{}
	doctor, err := backupinfra.NewDoctor(backupinfra.DoctorOptions{
		Primary: primary, Secondary: &fakeDoctorCheck{},
		KeyAuthority:    keyAuthority,
		StagingDir:      filepath.Join(t.TempDir(), "staging"),
		ApplicationDir:  filepath.Join(t.TempDir(), "data"),
		StagingMaxBytes: 1,
		ClockProbes: []backupinfra.ClockProbe{
			fakeClockProbe{value: time.Now()},
		},
	})
	require.NoError(t, err)

	report, checkErr := doctor.Check(context.Background())
	require.ErrorContains(t, checkErr, "ObjectWorm is disabled")
	require.Equal(t, backupusecase.HealthFailed, report.Primary)
	require.Equal(t, backupusecase.HealthFailed, report.KeyAuthority)
	require.Zero(t, keyAuthority.calls)
}

func TestBackupDoctorOpensAndRevokesRuntimeKeyGate(t *testing.T) {
	primary := &fakeDoctorCheck{}
	keyAuthority := &fakeQualifiedKeyAuthorityDoctor{}
	doctor, err := backupinfra.NewDoctor(backupinfra.DoctorOptions{
		Primary: primary, Secondary: &fakeDoctorCheck{},
		KeyAuthority:    keyAuthority,
		StagingDir:      filepath.Join(t.TempDir(), "staging"),
		ApplicationDir:  filepath.Join(t.TempDir(), "data"),
		StagingMaxBytes: 1,
		ClockProbes: []backupinfra.ClockProbe{
			fakeClockProbe{value: time.Now()},
		},
	})
	require.NoError(t, err)
	_, err = doctor.Check(context.Background())
	require.NoError(t, err)
	require.True(t, keyAuthority.qualified)

	primary.err = errors.New("ObjectWorm became unavailable")
	_, err = doctor.Check(context.Background())
	require.Error(t, err)
	require.False(t, keyAuthority.qualified)
}

type fakeDoctorCheck struct {
	calls int
	err   error
}

func (f *fakeDoctorCheck) Check(context.Context) error { f.calls++; return f.err }

type fakeKeyAuthorityDoctor struct {
	calls int
	err   error
}

type fakeQualifiedKeyAuthorityDoctor struct {
	fakeKeyAuthorityDoctor
	qualified bool
}

func (f *fakeQualifiedKeyAuthorityDoctor) Qualify() {
	f.qualified = true
}

func (f *fakeQualifiedKeyAuthorityDoctor) Invalidate() {
	f.qualified = false
}

func (f *fakeKeyAuthorityDoctor) Check(context.Context) error {
	f.calls++
	return f.err
}

type fakeClockProbe struct {
	value time.Time
	err   error
}

func (f fakeClockProbe) UTC(context.Context) (time.Time, error) { return f.value, f.err }
