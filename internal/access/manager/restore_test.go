package manager

import (
	"context"
	"net/http"
	"testing"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
)

func TestManagerRestoreRequiresExplicitGrantReauthenticationAndConfirmation(
	t *testing.T,
) {
	restore := &fakeRestoreManagement{}
	server := New(Options{
		Auth: testAuthConfig([]UserConfig{
			{
				Username: "wildcard", Password: "secret",
				Permissions: []PermissionConfig{{
					Resource: "*", Actions: []string{"*"},
				}},
			},
			{
				Username: "restore-admin", Password: "secret",
				Permissions: []PermissionConfig{{
					Resource: "cluster.restore", Actions: []string{"w"},
				}},
			},
		}),
		Restore: restore,
	})
	path := "/manager/backups/archives/backup-1/restore"
	body := []byte(`{
		"username":"restore-admin",
		"password":"secret",
		"confirmation":"RESTORE backup-1"
	}`)
	wildcard := performBackupRequest(
		server, http.MethodPost, path, body,
		mustIssueTestToken(t, server, "wildcard"),
	)
	if wildcard.Code != http.StatusForbidden {
		t.Fatalf("wildcard status = %d body=%s", wildcard.Code, wildcard.Body)
	}
	wrongPassword := performBackupRequest(
		server, http.MethodPost, path,
		[]byte(`{
			"username":"restore-admin",
			"password":"wrong",
			"confirmation":"RESTORE backup-1"
		}`),
		mustIssueTestToken(t, server, "restore-admin"),
	)
	if wrongPassword.Code != http.StatusUnauthorized {
		t.Fatalf(
			"wrong password status = %d body=%s",
			wrongPassword.Code, wrongPassword.Body,
		)
	}
	accepted := performBackupRequest(
		server, http.MethodPost, path, body,
		mustIssueTestToken(t, server, "restore-admin"),
	)
	if accepted.Code != http.StatusAccepted ||
		restore.archiveID != "backup-1" ||
		restore.initiator != "restore-admin" {
		t.Fatalf(
			"accepted status=%d archive=%q body=%s",
			accepted.Code, restore.archiveID, accepted.Body,
		)
	}
}

func TestManagerRestoreCancelUsesExactJobAndFailsClosedWhenUnwired(t *testing.T) {
	restore := &fakeRestoreManagement{}
	server := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "restore-admin", Password: "secret",
			Permissions: []PermissionConfig{{Resource: "cluster.restore", Actions: []string{"w"}}},
		}}),
		Restore: restore,
	})
	token := mustIssueTestToken(t, server, "restore-admin")
	recorder := performBackupRequest(
		server, http.MethodPost, "/manager/backups/restores/restore-a/cancel", nil, token,
	)
	if recorder.Code != http.StatusNoContent || restore.canceled != "restore-a" {
		t.Fatalf("status=%d canceled=%q body=%s", recorder.Code, restore.canceled, recorder.Body)
	}

	unwired := New(Options{Auth: testAuthConfig([]UserConfig{{
		Username: "restore-admin", Password: "secret",
		Permissions: []PermissionConfig{{Resource: "cluster.restore", Actions: []string{"w"}}},
	}})})
	recorder = performBackupRequest(
		unwired, http.MethodPost, "/manager/backups/restores/restore-a/cancel", nil,
		mustIssueTestToken(t, unwired, "restore-admin"),
	)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("unwired status=%d body=%s", recorder.Code, recorder.Body)
	}
}

type fakeRestoreManagement struct {
	archiveID string
	initiator string
	canceled  string
}

func (f *fakeRestoreManagement) StartRestore(
	_ context.Context,
	archiveID string,
	initiator string,
) (backupcontract.RestoreJob, error) {
	f.archiveID = archiveID
	f.initiator = initiator
	return backupcontract.RestoreJob{
		ID: "restore-1", BackupID: archiveID, Initiator: initiator,
		Status: backupcontract.RestoreStatusPreparing,
	}, nil
}

func (f *fakeRestoreManagement) CancelRestore(
	_ context.Context,
	jobID string,
) error {
	f.canceled = jobID
	return nil
}
