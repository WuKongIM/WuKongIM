package state

import (
	"strings"
	"testing"
)

func validBackupJobContract() ScheduledBackupJob {
	slots := make([]BackupSlotProgress, BackupHashSlotCount)
	for hashSlot := range slots {
		slots[hashSlot] = BackupSlotProgress{
			HashSlot: uint16(hashSlot), Status: BackupSlotStatusPending,
		}
	}
	return ScheduledBackupJob{
		ID: "backup-1", Trigger: BackupTriggerScheduled, Status: BackupJobStatusPreparing,
		PlanRevision: 2, ScheduledAtUnixMillis: 1_800_000_000_000,
		StartedAtUnixMillis: 1_800_000_000_100, DeadlineUnixMillis: 1_800_003_600_100,
		UpdatedUnixMillis: 1_800_000_000_100, Slots: slots,
	}
}

func validRestoreJobContract() ScheduledRestoreJob {
	slots := make([]RestoreSlotProgress, BackupHashSlotCount)
	for hashSlot := range slots {
		slots[hashSlot] = RestoreSlotProgress{
			HashSlot: uint16(hashSlot), Status: "pending",
		}
	}
	return ScheduledRestoreJob{
		ID: "restore-1", BackupID: "backup-1", Initiator: "operator", Status: "preparing",
		StartedUnixMillis: 1_800_000_000_100, DeadlineUnixMillis: 1_800_003_600_100,
		UpdatedUnixMillis: 1_800_000_000_100, TargetActivation: "activation-b", Slots: slots,
	}
}

func TestScheduledBackupJobAcceptsEveryActivePhaseAndFencedSlotState(t *testing.T) {
	for _, trigger := range []BackupTrigger{BackupTriggerInitial, BackupTriggerScheduled, BackupTriggerManual} {
		job := validBackupJobContract()
		job.Trigger = trigger
		if err := validateScheduledBackupJob(job); err != nil {
			t.Fatalf("valid trigger %q: %v", trigger, err)
		}
	}
	for _, status := range []BackupJobStatus{
		BackupJobStatusPreparing, BackupJobStatusExporting, BackupJobStatusVerifying,
		BackupJobStatusPublishing, BackupJobStatusCleaning,
	} {
		job := validBackupJobContract()
		job.Status = status
		if err := validateScheduledBackupJob(job); err != nil {
			t.Fatalf("valid active status %q: %v", status, err)
		}
	}

	job := validBackupJobContract()
	job.Slots[0] = BackupSlotProgress{
		HashSlot: 0, Status: BackupSlotStatusRunning, Attempt: 1, OwnerNodeID: 2, OwnerTerm: 9,
	}
	job.Slots[1] = BackupSlotProgress{
		HashSlot: 1, Status: BackupSlotStatusComplete, Attempt: 2, OwnerNodeID: 3, OwnerTerm: 10,
		ManifestKey: "backups/backup-1/slots/001/manifest.json", ManifestSHA256: strings.Repeat("a", 64),
	}
	job.Slots[2] = BackupSlotProgress{HashSlot: 2, Status: BackupSlotStatusFailed, Attempt: 3, ErrorCode: "retryable"}
	if err := validateScheduledBackupJob(job); err != nil {
		t.Fatalf("valid fenced slot progress: %v", err)
	}
}

func TestScheduledBackupJobRejectsIncompleteAuthorityAndArtifacts(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ScheduledBackupJob)
	}{
		{name: "missing id", mutate: func(j *ScheduledBackupJob) { j.ID = "" }},
		{name: "missing plan revision", mutate: func(j *ScheduledBackupJob) { j.PlanRevision = 0 }},
		{name: "missing start", mutate: func(j *ScheduledBackupJob) { j.StartedAtUnixMillis = 0 }},
		{name: "deadline before start", mutate: func(j *ScheduledBackupJob) { j.DeadlineUnixMillis = j.StartedAtUnixMillis }},
		{name: "update before start", mutate: func(j *ScheduledBackupJob) { j.UpdatedUnixMillis = j.StartedAtUnixMillis - 1 }},
		{name: "missing hash slot", mutate: func(j *ScheduledBackupJob) { j.Slots = j.Slots[:BackupHashSlotCount-1] }},
		{name: "unknown trigger", mutate: func(j *ScheduledBackupJob) { j.Trigger = "unknown" }},
		{name: "terminal status", mutate: func(j *ScheduledBackupJob) { j.Status = BackupJobStatusSucceeded }},
		{name: "out of order slot", mutate: func(j *ScheduledBackupJob) { j.Slots[17].HashSlot = 18 }},
		{name: "unknown slot status", mutate: func(j *ScheduledBackupJob) { j.Slots[0].Status = "unknown" }},
		{name: "running without attempt", mutate: func(j *ScheduledBackupJob) {
			j.Slots[0] = BackupSlotProgress{HashSlot: 0, Status: BackupSlotStatusRunning, OwnerNodeID: 1, OwnerTerm: 1}
		}},
		{name: "running without owner", mutate: func(j *ScheduledBackupJob) {
			j.Slots[0] = BackupSlotProgress{HashSlot: 0, Status: BackupSlotStatusRunning, Attempt: 1, OwnerTerm: 1}
		}},
		{name: "running without term", mutate: func(j *ScheduledBackupJob) {
			j.Slots[0] = BackupSlotProgress{HashSlot: 0, Status: BackupSlotStatusRunning, Attempt: 1, OwnerNodeID: 1}
		}},
		{name: "complete without manifest", mutate: func(j *ScheduledBackupJob) {
			j.Slots[0] = BackupSlotProgress{
				HashSlot: 0, Status: BackupSlotStatusComplete, Attempt: 1, OwnerNodeID: 1, OwnerTerm: 1,
				ManifestSHA256: strings.Repeat("a", 64),
			}
		}},
		{name: "complete oversized manifest key", mutate: func(j *ScheduledBackupJob) {
			j.Slots[0] = BackupSlotProgress{
				HashSlot: 0, Status: BackupSlotStatusComplete, Attempt: 1, OwnerNodeID: 1, OwnerTerm: 1,
				ManifestKey: strings.Repeat("x", 513), ManifestSHA256: strings.Repeat("a", 64),
			}
		}},
		{name: "complete malformed digest length", mutate: func(j *ScheduledBackupJob) {
			j.Slots[0] = BackupSlotProgress{
				HashSlot: 0, Status: BackupSlotStatusComplete, Attempt: 1, OwnerNodeID: 1, OwnerTerm: 1,
				ManifestKey: "manifest.json", ManifestSHA256: "short",
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			job := validBackupJobContract()
			test.mutate(&job)
			if err := validateScheduledBackupJob(job); err == nil {
				t.Fatal("validateScheduledBackupJob() error = nil")
			}
		})
	}
}

func TestScheduledRestoreJobAcceptsEveryActivePhaseAndReplicaEvidence(t *testing.T) {
	for _, status := range []string{
		"preparing", "validated", "maintenance", "staging", "verifying",
		"switching", "finalizing", "rolling_back",
	} {
		job := validRestoreJobContract()
		job.Status = status
		if err := validateScheduledRestoreJob(job); err != nil {
			t.Fatalf("valid active status %q: %v", status, err)
		}
	}
	job := validRestoreJobContract()
	job.Slots[0] = RestoreSlotProgress{HashSlot: 0, Status: "staging", Attempt: 1, ReplicaNodeIDs: []uint64{1, 2, 3}}
	job.Slots[1] = RestoreSlotProgress{HashSlot: 1, Status: "staged", ReplicaNodeIDs: []uint64{3, 2, 1}}
	job.Slots[2] = RestoreSlotProgress{HashSlot: 2, Status: "verified", ReplicaNodeIDs: []uint64{1}}
	job.Slots[3] = RestoreSlotProgress{HashSlot: 3, Status: "failed", ErrorCode: "checksum_mismatch"}
	if err := validateScheduledRestoreJob(job); err != nil {
		t.Fatalf("valid restore evidence: %v", err)
	}
}

func TestScheduledRestoreJobRejectsIncompleteMaintenanceEvidence(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ScheduledRestoreJob)
	}{
		{name: "missing id", mutate: func(j *ScheduledRestoreJob) { j.ID = "" }},
		{name: "missing backup", mutate: func(j *ScheduledRestoreJob) { j.BackupID = "" }},
		{name: "blank initiator", mutate: func(j *ScheduledRestoreJob) { j.Initiator = " \t" }},
		{name: "oversized initiator", mutate: func(j *ScheduledRestoreJob) { j.Initiator = strings.Repeat("x", 129) }},
		{name: "missing status", mutate: func(j *ScheduledRestoreJob) { j.Status = "" }},
		{name: "unknown status", mutate: func(j *ScheduledRestoreJob) { j.Status = "succeeded" }},
		{name: "missing start", mutate: func(j *ScheduledRestoreJob) { j.StartedUnixMillis = 0 }},
		{name: "deadline before start", mutate: func(j *ScheduledRestoreJob) { j.DeadlineUnixMillis = j.StartedUnixMillis }},
		{name: "update before start", mutate: func(j *ScheduledRestoreJob) { j.UpdatedUnixMillis = j.StartedUnixMillis - 1 }},
		{name: "missing activation", mutate: func(j *ScheduledRestoreJob) { j.TargetActivation = "" }},
		{name: "missing hash slot", mutate: func(j *ScheduledRestoreJob) { j.Slots = j.Slots[:BackupHashSlotCount-1] }},
		{name: "oversized job error", mutate: func(j *ScheduledRestoreJob) { j.ErrorCode = strings.Repeat("x", 129) }},
		{name: "out of order slot", mutate: func(j *ScheduledRestoreJob) { j.Slots[200].HashSlot = 199 }},
		{name: "oversized slot error", mutate: func(j *ScheduledRestoreJob) { j.Slots[0].ErrorCode = strings.Repeat("x", 129) }},
		{name: "unknown slot status", mutate: func(j *ScheduledRestoreJob) { j.Slots[0].Status = "unknown" }},
		{name: "staging without attempt", mutate: func(j *ScheduledRestoreJob) { j.Slots[0].Status = "staging" }},
		{name: "zero replica", mutate: func(j *ScheduledRestoreJob) { j.Slots[0].ReplicaNodeIDs = []uint64{1, 0} }},
		{name: "duplicate replica", mutate: func(j *ScheduledRestoreJob) { j.Slots[0].ReplicaNodeIDs = []uint64{1, 2, 1} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			job := validRestoreJobContract()
			test.mutate(&job)
			if err := validateScheduledRestoreJob(job); err == nil {
				t.Fatal("validateScheduledRestoreJob() error = nil")
			}
		})
	}
}

func TestScheduledBackupCloneOwnsEveryNestedMutableValue(t *testing.T) {
	state := ScheduledBackupState{
		Revision: 1,
		Plan: &BackupPlan{
			Store:                  BackupStoreConfig{CredentialCiphertext: []byte("secret")},
			RepositoryVerification: &BackupRepositoryVerification{Status: BackupRepositoryVerificationVerified, VerifiedAtUnixMillis: 1},
		},
		ActiveBackup:           &ScheduledBackupJob{Slots: []BackupSlotProgress{{HashSlot: 1, Status: BackupSlotStatusPending}}},
		ActiveRestore:          &ScheduledRestoreJob{Slots: []RestoreSlotProgress{{HashSlot: 2, Status: "pending", ReplicaNodeIDs: []uint64{1, 2}}}},
		ActiveArchiveOperation: &BackupArchiveOperation{Token: "token"},
		History:                []BackupTaskRecord{{ID: "history"}},
	}
	clone := state.Clone()
	clone.Plan.Store.CredentialCiphertext[0] = 'X'
	clone.Plan.RepositoryVerification.Status = BackupRepositoryVerificationUnverified
	clone.ActiveBackup.Slots[0].Status = BackupSlotStatusFailed
	clone.ActiveRestore.Slots[0].ReplicaNodeIDs[0] = 9
	clone.ActiveArchiveOperation.Token = "changed"
	clone.History[0].ID = "changed"

	if string(state.Plan.Store.CredentialCiphertext) != "secret" ||
		state.Plan.RepositoryVerification.Status != BackupRepositoryVerificationVerified ||
		state.ActiveBackup.Slots[0].Status != BackupSlotStatusPending ||
		state.ActiveRestore.Slots[0].ReplicaNodeIDs[0] != 1 ||
		state.ActiveArchiveOperation.Token != "token" || state.History[0].ID != "history" {
		t.Fatalf("Clone() shared nested state: original=%+v clone=%+v", state, clone)
	}
}

func TestScheduledBackupStateAcceptsBoundedArchiveAndHistoryLifecycles(t *testing.T) {
	if err := validateScheduledBackup(nil); err != nil {
		t.Fatalf("nil scheduled backup: %v", err)
	}
	plan := validScheduledBackupPlanForTest(BackupStoreConfig{Kind: BackupStoreKindFile}, nil)
	base := ScheduledBackupState{Revision: 1, Plan: plan}
	if err := validateScheduledBackup(&base); err != nil {
		t.Fatalf("plan-only state: %v", err)
	}
	for _, kind := range []string{"verify", "hold", "delete", "retention", "restore"} {
		value := base.Clone()
		value.ActiveArchiveOperation = &BackupArchiveOperation{
			Token: "lease-1", Kind: kind, ArchiveID: "archive-1",
			CoordinatorNodeID: 1, CoordinatorTerm: 7,
			StartedUnixMillis: 10, ExpiresUnixMillis: 20,
		}
		if err := validateScheduledBackup(&value); err != nil {
			t.Fatalf("valid archive operation %q: %v", kind, err)
		}
	}
	value := base.Clone()
	value.History = []BackupTaskRecord{
		{ID: "backup-1", Kind: "backup", Status: "succeeded", StartedUnixMillis: 10, CompletedUnixMillis: 20},
		{ID: "restore-1", Kind: "restore", Initiator: "operator", Status: "failed", StartedUnixMillis: 21, CompletedUnixMillis: 22},
		{ID: "verification-1", Kind: "verification", Status: "succeeded", StartedUnixMillis: 23, CompletedUnixMillis: 24},
		{ID: "retention-1", Kind: "retention", Status: "succeeded", StartedUnixMillis: 25, CompletedUnixMillis: 26},
	}
	if err := validateScheduledBackup(&value); err != nil {
		t.Fatalf("valid bounded history: %v", err)
	}
	value = base.Clone()
	backup := validBackupJobContract()
	value.ActiveBackup = &backup
	if err := validateScheduledBackup(&value); err != nil {
		t.Fatalf("valid active backup: %v", err)
	}
	value = base.Clone()
	restore := validRestoreJobContract()
	value.ActiveRestore = &restore
	if err := validateScheduledBackup(&value); err != nil {
		t.Fatalf("valid active restore: %v", err)
	}
}

func TestScheduledBackupStateRejectsUnboundedOrAmbiguousLifecycle(t *testing.T) {
	base := func() ScheduledBackupState {
		return ScheduledBackupState{
			Revision: 1,
			Plan:     validScheduledBackupPlanForTest(BackupStoreConfig{Kind: BackupStoreKindFile}, nil),
		}
	}
	tests := []struct {
		name   string
		mutate func(*ScheduledBackupState)
	}{
		{name: "missing revision", mutate: func(s *ScheduledBackupState) { s.Revision = 0 }},
		{name: "history limit", mutate: func(s *ScheduledBackupState) { s.History = make([]BackupTaskRecord, MaxBackupTaskHistory+1) }},
		{name: "backup and restore active", mutate: func(s *ScheduledBackupState) {
			backup, restore := validBackupJobContract(), validRestoreJobContract()
			s.ActiveBackup, s.ActiveRestore = &backup, &restore
		}},
		{name: "unknown archive operation", mutate: func(s *ScheduledBackupState) {
			s.ActiveArchiveOperation = &BackupArchiveOperation{Token: "lease", Kind: "unknown", StartedUnixMillis: 1, ExpiresUnixMillis: 2}
		}},
		{name: "missing archive token", mutate: func(s *ScheduledBackupState) {
			s.ActiveArchiveOperation = &BackupArchiveOperation{Kind: "verify", StartedUnixMillis: 1, ExpiresUnixMillis: 2}
		}},
		{name: "oversized archive token", mutate: func(s *ScheduledBackupState) {
			s.ActiveArchiveOperation = &BackupArchiveOperation{Token: strings.Repeat("x", 129), Kind: "verify", StartedUnixMillis: 1, ExpiresUnixMillis: 2}
		}},
		{name: "oversized archive id", mutate: func(s *ScheduledBackupState) {
			s.ActiveArchiveOperation = &BackupArchiveOperation{Token: "lease", Kind: "verify", ArchiveID: strings.Repeat("x", 129), StartedUnixMillis: 1, ExpiresUnixMillis: 2}
		}},
		{name: "partial coordinator fence", mutate: func(s *ScheduledBackupState) {
			s.ActiveArchiveOperation = &BackupArchiveOperation{Token: "lease", Kind: "verify", CoordinatorNodeID: 1, StartedUnixMillis: 1, ExpiresUnixMillis: 2}
		}},
		{name: "missing archive start", mutate: func(s *ScheduledBackupState) {
			s.ActiveArchiveOperation = &BackupArchiveOperation{Token: "lease", Kind: "verify", StartedUnixMillis: 0, ExpiresUnixMillis: 2}
		}},
		{name: "archive expiry before start", mutate: func(s *ScheduledBackupState) {
			s.ActiveArchiveOperation = &BackupArchiveOperation{Token: "lease", Kind: "verify", StartedUnixMillis: 2, ExpiresUnixMillis: 2}
		}},
		{name: "active backup missing plan", mutate: func(s *ScheduledBackupState) {
			backup := validBackupJobContract()
			s.Plan, s.ActiveBackup = nil, &backup
		}},
		{name: "invalid active backup", mutate: func(s *ScheduledBackupState) {
			backup := validBackupJobContract()
			backup.Status = BackupJobStatusSucceeded
			s.ActiveBackup = &backup
		}},
		{name: "active restore missing plan", mutate: func(s *ScheduledBackupState) {
			restore := validRestoreJobContract()
			s.Plan, s.ActiveRestore = nil, &restore
		}},
		{name: "invalid active restore", mutate: func(s *ScheduledBackupState) {
			restore := validRestoreJobContract()
			restore.Status = "succeeded"
			s.ActiveRestore = &restore
		}},
		{name: "history missing id", mutate: func(s *ScheduledBackupState) {
			s.History = []BackupTaskRecord{{Kind: "backup", StartedUnixMillis: 1, CompletedUnixMillis: 2}}
		}},
		{name: "history unknown kind", mutate: func(s *ScheduledBackupState) {
			s.History = []BackupTaskRecord{{ID: "x", Kind: "unknown", StartedUnixMillis: 1, CompletedUnixMillis: 2}}
		}},
		{name: "restore history missing initiator", mutate: func(s *ScheduledBackupState) {
			s.History = []BackupTaskRecord{{ID: "x", Kind: "restore", Initiator: " ", StartedUnixMillis: 1, CompletedUnixMillis: 2}}
		}},
		{name: "history oversized initiator", mutate: func(s *ScheduledBackupState) {
			s.History = []BackupTaskRecord{{ID: "x", Kind: "backup", Initiator: strings.Repeat("x", 129), StartedUnixMillis: 1, CompletedUnixMillis: 2}}
		}},
		{name: "history missing start", mutate: func(s *ScheduledBackupState) {
			s.History = []BackupTaskRecord{{ID: "x", Kind: "backup", StartedUnixMillis: 0, CompletedUnixMillis: 2}}
		}},
		{name: "history completes before start", mutate: func(s *ScheduledBackupState) {
			s.History = []BackupTaskRecord{{ID: "x", Kind: "backup", StartedUnixMillis: 2, CompletedUnixMillis: 1}}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := base()
			test.mutate(&value)
			if err := validateScheduledBackup(&value); err == nil {
				t.Fatal("validateScheduledBackup() error = nil")
			}
		})
	}
}

func TestBackupPlanValidatesRepositorySpecificSafetyContract(t *testing.T) {
	stores := []BackupStoreConfig{
		{Kind: BackupStoreKindFile},
		{Kind: BackupStoreKindS3, Endpoint: "https://s3.example", Bucket: "backups"},
		{Kind: BackupStoreKindOSS, Region: "cn-hangzhou", Bucket: "backups", Prefix: "cluster-a", CredentialCiphertext: []byte("cipher")},
		{Kind: BackupStoreKindCOS, Region: "ap-shanghai", Bucket: "backups-1250000000", Prefix: "cluster-a", CredentialCiphertext: []byte("cipher")},
	}
	for _, store := range stores {
		if err := validateBackupPlan(*validScheduledBackupPlanForTest(store, nil)); err != nil {
			t.Fatalf("valid %q plan: %v", store.Kind, err)
		}
	}
	invalidStores := []BackupStoreConfig{
		{Kind: BackupStoreKindFile, Endpoint: "unexpected"},
		{Kind: BackupStoreKindS3, Endpoint: " ", Bucket: "backups"},
		{Kind: BackupStoreKindS3, Endpoint: "https://s3.example", Bucket: " "},
		{Kind: BackupStoreKindOSS, Region: "-cn", Bucket: "backups", Prefix: "cluster", CredentialCiphertext: []byte("cipher")},
		{Kind: BackupStoreKindOSS, Region: "cn-hangzhou", Bucket: "", Prefix: "cluster", CredentialCiphertext: []byte("cipher")},
		{Kind: BackupStoreKindOSS, Region: "cn-hangzhou", Bucket: "backups", Prefix: "", CredentialCiphertext: []byte("cipher")},
		{Kind: BackupStoreKindOSS, Region: "cn-hangzhou", Bucket: "backups", Prefix: "cluster", PathStyle: true, CredentialCiphertext: []byte("cipher")},
		{Kind: BackupStoreKindOSS, Region: "cn-hangzhou", Bucket: "backups", Prefix: "cluster"},
		{Kind: BackupStoreKindCOS, Region: "ap-shanghai", Bucket: "backups", Prefix: "cluster", CredentialCiphertext: []byte("cipher")},
		{Kind: "unknown"},
	}
	for _, store := range invalidStores {
		if err := validateBackupPlan(*validScheduledBackupPlanForTest(store, nil)); err == nil {
			t.Fatalf("invalid %q store accepted: %+v", store.Kind, store)
		}
	}

	for _, region := range []string{"cn-hangzhou", "a", "r1-east"} {
		if !validBackupCloudRegion(region) {
			t.Fatalf("valid region %q rejected", region)
		}
	}
	for _, region := range []string{"", "-east", "east-", "East", "east_1", strings.Repeat("a", 64)} {
		if validBackupCloudRegion(region) {
			t.Fatalf("invalid region %q accepted", region)
		}
	}
	for bucket, want := range map[string]bool{
		"backups-1250000000": true, "backups": false, "-123": false,
		"backups-": false, "backups-appid": false,
	} {
		if got := backupCOSBucketHasAPPID(bucket); got != want {
			t.Fatalf("backupCOSBucketHasAPPID(%q) = %v, want %v", bucket, got, want)
		}
	}
}
