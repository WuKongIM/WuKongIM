package backup

import "testing"

func TestLocalRestoreInstallerEnforcesNodeGlobalStagingQuota(t *testing.T) {
	installer := &LocalRestoreInstaller{stagingMaxBytes: 100}
	if !installer.reserveStagingBytes(60) {
		t.Fatal("reserveStagingBytes(60) = false")
	}
	if installer.reserveStagingBytes(50) {
		t.Fatal("concurrent reserveStagingBytes(50) = true, want node-global rejection")
	}
	installer.releaseStagingBytes(60)
	if !installer.reserveStagingBytes(50) {
		t.Fatal("reserveStagingBytes(50) after release = false")
	}
	installer.releaseStagingBytes(50)
}
