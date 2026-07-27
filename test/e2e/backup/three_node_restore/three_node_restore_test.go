//go:build e2e

package three_node_restore

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/WuKongIM/WuKongIM/pkg/hashslot"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

type backupStatus struct {
	Health           string      `json:"health"`
	FailureCategory  string      `json:"failure_category"`
	LatestCheckpoint *checkpoint `json:"latest_checkpoint"`
	CaptureLeases    []struct {
		HashSlot                  uint16 `json:"hash_slot"`
		HolderNodeID              uint64 `json:"holder_node_id"`
		LeaseSequence             uint64 `json:"lease_sequence"`
		FrontierRevision          uint64 `json:"frontier_revision"`
		MetadataSourceWatermark   uint64 `json:"metadata_source_watermark"`
		MessageSourceWatermark    uint64 `json:"message_source_watermark"`
		FrontierUpdatedUnixMillis int64  `json:"frontier_updated_unix_millis"`
	} `json:"capture_leases"`
	CaptureStatuses []struct {
		HashSlot         uint16 `json:"hash_slot"`
		State            string `json:"state"`
		FailureCategory  string `json:"failure_category"`
		FrontierRevision uint64 `json:"frontier_revision"`
	} `json:"capture_statuses"`
}

type checkpoint struct {
	ID                    string `json:"id"`
	EffectiveAtUnixMillis int64  `json:"effective_at_unix_millis"`
	CreatedAtUnixMillis   int64  `json:"created_at_unix_millis"`
	Held                  bool   `json:"held"`
}

type checkpointPublication struct {
	Checkpoint       checkpoint `json:"checkpoint"`
	CheckpointSHA256 string     `json:"checkpoint_sha256"`
	CatalogHeadToken string     `json:"catalog_head_token"`
}

type restorePlan struct {
	ID                                  string                                    `json:"id"`
	CheckpointID                        string                                    `json:"checkpoint_id"`
	CheckpointSHA256                    string                                    `json:"checkpoint_sha256"`
	ErasureLedgerVersion                uint32                                    `json:"erasure_ledger_version"`
	ErasureEventCount                   uint64                                    `json:"erasure_event_count"`
	ErasureLedgerSHA256                 string                                    `json:"erasure_ledger_sha256"`
	SourceClusterID                     string                                    `json:"source_cluster_id"`
	SourceGeneration                    string                                    `json:"source_generation"`
	TargetClusterID                     string                                    `json:"target_cluster_id"`
	TargetGeneration                    string                                    `json:"target_generation"`
	HashSlotCount                       uint16                                    `json:"hash_slot_count"`
	Status                              string                                    `json:"status"`
	Partitions                          []restorePartition                        `json:"partitions"`
	StagingCleanupCompletedAtUnixMillis int64                                     `json:"staging_cleanup_completed_at_unix_millis"`
	Activation                          *backupartifact.RestoreActivationEvidence `json:"activation"`
}

type restorePartition struct {
	HashSlot   uint16 `json:"hash_slot"`
	Installed  bool   `json:"installed"`
	Verified   bool   `json:"verified"`
	PlainBytes uint64 `json:"plain_bytes"`
	Messages   uint64 `json:"message_count"`
}

type restoreStatus struct {
	Plan *restorePlan `json:"plan"`
}

type managerLogin struct {
	AccessToken string `json:"access_token"`
}

type retentionResponse struct {
	Status             string `json:"status"`
	AdvancedThroughSeq uint64 `json:"advanced_through_seq"`
}

type managerMessagePage struct {
	Items []struct {
		MessageSeq uint64 `json:"message_seq"`
	} `json:"items"`
}

type managerNodesResponse struct {
	ControllerLeaderID uint64 `json:"controller_leader_id"`
}

type managerSlotsResponse struct {
	Items []struct {
		HashSlots *struct {
			Items []uint16 `json:"items"`
		} `json:"hash_slots"`
		Runtime struct {
			LeaderID uint64 `json:"leader_id"`
		} `json:"runtime"`
	} `json:"items"`
}

type repositoryQualification struct {
	Endpoint       string
	Region         string
	Bucket         string
	Prefix         string
	AccessRoleARN  string
	RepairRoleARN  string
	GarbageRoleARN string
}

// storageQualification describes either the e2e-only file substitute or the
// production object-storage/KMS environment exercised by the same recovery drill.
type storageQualification struct {
	FileRoot         string
	CorruptionRoot   string
	Provider         string
	RepositoryID     string
	SourceGeneration string
	TargetGeneration string
	KMSKeyID         string
	SigningKeyID     string
	KMSRegion        string
	KMSEndpoint      string
	KMSRoleARN       string
	ObjectLockDays   int
	Primary          repositoryQualification
	Secondary        repositoryQualification
	ProductionRunID  string
	ProductionCommit string
}

type recoveryDrillResult struct {
	CheckpointID         string
	RestoredMessages     uint64
	ControllerFailover   bool
	SlotFailover         bool
	DataNodeFailover     bool
	RestoreFailover      bool
	RepositoryRepair     bool
	DualCorruptionRebase bool
	GarbageRoleProbe     bool
}

func TestThreeNodeBackupContinuousRestoresAndContinuesTraffic(t *testing.T) {
	runThreeNodeRecoveryDrill(t, localStorageQualification(t))
}

func TestProductionStorageQualification(t *testing.T) {
	if os.Getenv("WK_E2E_BACKUP_PRODUCTION") != "1" {
		t.Skip("set WK_E2E_BACKUP_PRODUCTION=1 to run the production storage/KMS recovery drill")
	}
	qualification := productionStorageQualification(t)
	result := runThreeNodeRecoveryDrill(t, qualification)
	evidence := struct {
		Schema           string `json:"schema"`
		Provider         string `json:"provider"`
		RunID            string `json:"run_id"`
		Commit           string `json:"commit"`
		PrimaryRegion    string `json:"primary_region"`
		SecondaryRegion  string `json:"secondary_region"`
		ObjectLockDays   int    `json:"object_lock_days"`
		CheckpointID     string `json:"checkpoint_id"`
		RestoredMessages uint64 `json:"restored_messages"`
		SourceStopped    bool   `json:"source_stopped"`
		FreshTarget      bool   `json:"fresh_target"`
		PostRestoreWrite bool   `json:"post_restore_write"`
		ControllerFault  bool   `json:"controller_fault"`
		SlotLeaderFault  bool   `json:"slot_leader_fault"`
		DataNodeFault    bool   `json:"data_node_fault"`
		RestoreFault     bool   `json:"restore_leader_fault"`
		RepositoryRepair bool   `json:"repository_repair"`
		DualRebase       bool   `json:"dual_corruption_rebase"`
		GarbageRoleProbe bool   `json:"garbage_role_probe"`
	}{
		Schema:           "wukongim/backup-production-qualification/v2",
		Provider:         qualification.Provider,
		RunID:            qualification.ProductionRunID,
		Commit:           qualification.ProductionCommit,
		PrimaryRegion:    qualification.Primary.Region,
		SecondaryRegion:  qualification.Secondary.Region,
		ObjectLockDays:   qualification.ObjectLockDays,
		CheckpointID:     result.CheckpointID,
		RestoredMessages: result.RestoredMessages,
		SourceStopped:    true,
		FreshTarget:      true,
		PostRestoreWrite: true,
		ControllerFault:  result.ControllerFailover,
		SlotLeaderFault:  result.SlotFailover,
		DataNodeFault:    result.DataNodeFailover,
		RestoreFault:     result.RestoreFailover,
		RepositoryRepair: result.RepositoryRepair,
		DualRebase:       result.DualCorruptionRebase,
		GarbageRoleProbe: result.GarbageRoleProbe,
	}
	encoded, err := json.Marshal(evidence)
	require.NoError(t, err)
	t.Logf("WK-BACKUP-PRODUCTION-EVIDENCE %s", encoded)
}

func runThreeNodeRecoveryDrill(t *testing.T, qualification storageQualification) recoveryDrillResult {
	t.Helper()
	options := []suite.Option{suite.WithManagerHTTP()}
	for nodeID := uint64(1); nodeID <= 3; nodeID++ {
		options = append(options,
			suite.WithNodeConfigOverrides(nodeID, sourceBackupConfig(qualification, nodeID)),
		)
		if qualification.FileRoot != "" {
			options = append(options, localBackupNodeEnvironment(nodeID, qualification.FileRoot))
		}
		options = append(
			options,
			suite.WithNodeEnv(
				nodeID,
				"WUKONGIM_BACKUP_E2E_CORRUPTION_DIR="+
					filepath.Join(qualification.CorruptionRoot, "faults"),
			),
		)
	}
	cluster := suite.New(t).StartThreeNodeCluster(options...)
	ctx, cancel := context.WithTimeout(context.Background(), suite.BackupClusterReadyTimeout)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())
	sourceToken := loginManager(
		t, cluster, "source-admin", "source-secret",
	)

	baseline := publishCheckpointEventually(
		t, cluster, sourceToken, 20*time.Second,
	)
	require.NotEmpty(t, baseline.Checkpoint.ID)
	require.Len(t, baseline.CheckpointSHA256, 64)
	exerciseRepositoryIntegrityRepair(
		t, cluster, sourceToken, qualification.CorruptionRoot,
	)
	exerciseSourceLeaderFailures(t, cluster, sourceToken)
	waitForBackupHealthy(t, cluster, sourceToken, 120*time.Second)
	_ = publishCheckpointEventually(
		t, cluster, sourceToken, 120*time.Second,
	)
	preDeltaFrontiers := backupDurableFrontiers(
		t, cluster, sourceToken,
	)
	conversationPersistedBaseline := conversationPersistedRows(t, cluster)

	const firstPayload = "message captured by continuous backup"
	messageCtx, cancelMessage := context.WithTimeout(context.Background(), 15*time.Second)
	message, err := suite.PostMessageSendEventually(messageCtx, cluster.MustNode(1).APIAddr(), map[string]any{
		"from_uid":      "backup-e2e-sender",
		"channel_id":    "backup-e2e-recipient",
		"channel_type":  frame.ChannelTypePerson,
		"client_msg_no": "backup-e2e-message-1",
		"payload":       base64.StdEncoding.EncodeToString([]byte(firstPayload)),
	})
	cancelMessage()
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equal(t, uint8(frame.ReasonSuccess), message.Reason)
	require.NotZero(t, message.MessageID)
	require.NotZero(t, message.MessageSeq)
	suite.RequireConversationEventuallyWithin(t, *cluster.MustNode(1), "backup-e2e-sender", "backup-e2e-recipient", 10*time.Second, func(item suite.ConversationListItem) error {
		if item.LastMessage == nil || item.LastMessage.MessageID != uint64(message.MessageID) || item.LastMessage.MessageSeq != message.MessageSeq {
			return fmt.Errorf("source conversation projection has not reached committed message")
		}
		return nil
	})
	const (
		erasureChannelID   = "backup-e2e-erasure-group"
		erasureChannelType = frame.ChannelTypeGroup
	)
	erasedMessages := appendGroupMessages(t, cluster, erasureChannelID, erasureChannelType, 2)
	waitForConversationDurable(
		t, cluster, conversationPersistedBaseline, 10*time.Second,
	)
	personalChannelID := channelid.EncodePersonChannel(
		"backup-e2e-sender", "backup-e2e-recipient",
	)
	waitForDurableFrontiers(
		t, cluster, sourceToken, preDeltaFrontiers,
		[]uint16{
			hashslot.HashSlotForKey("backup-e2e-sender", 16),
			hashslot.HashSlotForKey(personalChannelID, 16),
			hashslot.HashSlotForKey(erasureChannelID, 16),
		},
		[]uint16{
			hashslot.HashSlotForKey(personalChannelID, 16),
			hashslot.HashSlotForKey(erasureChannelID, 16),
		},
		120*time.Second,
	)

	delta := publishCheckpointEventually(
		t, cluster, sourceToken, 20*time.Second,
	)
	require.NotEqual(t, baseline.Checkpoint.ID, delta.Checkpoint.ID)
	require.GreaterOrEqual(
		t, delta.Checkpoint.EffectiveAtUnixMillis,
		baseline.Checkpoint.EffectiveAtUnixMillis,
	)

	erasure := advanceMessageRetentionEventually(
		t, cluster, sourceToken, erasureChannelID, erasureChannelType,
		erasedMessages[1].MessageSeq,
	)
	require.Equal(t, "advanced", erasure.Status)
	require.Equal(t, erasedMessages[1].MessageSeq, erasure.AdvancedThroughSeq)

	target := startRestoreCluster(t, qualification)
	token := loginManager(
		t, target, "restore-admin", "restore-secret",
	)
	plan := createRestorePlan(t, target, token, delta)
	require.Equal(t, delta.Checkpoint.ID, plan.CheckpointID)
	require.Equal(t, backupartifact.ErasureLedgerSnapshotVersion, plan.ErasureLedgerVersion)
	require.Equal(t, uint64(1), plan.ErasureEventCount)
	require.Len(t, plan.ErasureLedgerSHA256, 64)
	require.Equal(t, "wukongim-e2e-three", plan.SourceClusterID)
	require.Equal(t, qualification.SourceGeneration, plan.SourceGeneration)
	require.Equal(t, "wukongim-e2e-restore", plan.TargetClusterID)
	require.Equal(t, qualification.TargetGeneration, plan.TargetGeneration)
	require.Equal(t, uint16(16), plan.HashSlotCount)

	restoreControllerLeader := currentBackupControllerLeader(
		t, target, token, 20*time.Second,
	)
	plan = startRestore(t, target, token, plan.ID)
	require.Equal(t, "installing", plan.Status)
	exerciseRestoreLeaderFailure(
		t, target, token, restoreControllerLeader,
	)
	plan = waitForRestoreStatus(t, target, token, "installed", 45*time.Second)
	require.Len(t, plan.Partitions, 16)
	var restoredMessages uint64
	for hashSlot, partition := range plan.Partitions {
		require.Equal(t, uint16(hashSlot), partition.HashSlot)
		require.True(t, partition.Installed)
		restoredMessages += partition.Messages
	}
	require.Greater(t, restoredMessages, uint64(0))

	plan = verifyRestore(t, target, token, plan.ID)
	require.Equal(t, "verified", plan.Status)
	for _, partition := range plan.Partitions {
		require.True(t, partition.Verified)
	}
	requireRestoreTargetTrafficClosed(t, target)
	sourceClient, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	require.NoError(
		t,
		sourceClient.Connect(
			cluster.MustNode(2).GatewayAddr(),
			"backup-source-fence-client",
			"backup-source-fence-device",
		),
		cluster.DumpDiagnostics(),
	)
	defer func() { _ = sourceClient.Close() }()
	receipt := fenceSource(t, cluster, sourceToken, plan)
	requireSourceWritesFenced(t, cluster, sourceClient)
	stopCluster(t, cluster)
	plan = activateRestore(t, target, token, plan.ID, receipt)
	require.Equal(t, "activated", plan.Status)
	require.NotZero(t, plan.StagingCleanupCompletedAtUnixMillis)
	require.NotNil(t, plan.Activation)
	require.Equal(
		t, backupartifact.RestoreActivationSourceFence,
		plan.Activation.Kind,
	)

	restartActivatedCluster(t, target)
	restartedPlan := waitForRestoreStatus(
		t, target, token, "activated", 10*time.Second,
	)
	require.Equal(t, plan.ID, restartedPlan.ID)
	require.Equal(
		t, qualification.TargetGeneration, restartedPlan.TargetGeneration,
	)
	require.Equal(
		t, "wukongim-e2e-restore", restartedPlan.TargetClusterID,
	)
	requireConversationEventuallyAcrossNodes(t, target, "backup-e2e-sender", "backup-e2e-recipient", 30*time.Second, func(item suite.ConversationListItem) error {
		if item.LastMessage == nil {
			return fmt.Errorf("restored conversation has no last message")
		}
		if item.LastMessage.MessageID != uint64(message.MessageID) || item.LastMessage.MessageSeq != message.MessageSeq {
			return fmt.Errorf("restored last message id/seq=%d/%d want=%d/%d", item.LastMessage.MessageID, item.LastMessage.MessageSeq, message.MessageID, message.MessageSeq)
		}
		if item.LastMessage.ClientMsgNo != "backup-e2e-message-1" || string(item.LastMessage.Payload) != firstPayload {
			return fmt.Errorf("restored last message identity or payload mismatch")
		}
		return nil
	})
	requireManagerMessageSeqsEventually(t, target, token, erasureChannelID, erasureChannelType, nil)

	postErasure := appendGroupMessages(t, target, erasureChannelID, erasureChannelType, 1)[0]
	require.Equal(t, erasedMessages[1].MessageSeq+1, postErasure.MessageSeq)
	require.Greater(t, postErasure.MessageID, erasedMessages[1].MessageID)
	requireManagerMessageSeqsEventually(t, target, token, erasureChannelID, erasureChannelType, []uint64{postErasure.MessageSeq})

	const secondPayload = "message committed after restore activation"
	secondCtx, cancelSecond := context.WithTimeout(context.Background(), 15*time.Second)
	second, err := suite.PostMessageSendEventually(secondCtx, target.MustNode(1).APIAddr(), map[string]any{
		"from_uid":      "backup-e2e-sender",
		"channel_id":    "backup-e2e-recipient",
		"channel_type":  frame.ChannelTypePerson,
		"client_msg_no": "backup-e2e-message-2",
		"payload":       base64.StdEncoding.EncodeToString([]byte(secondPayload)),
	})
	cancelSecond()
	require.NoError(t, err, target.DumpDiagnostics())
	require.Equal(t, uint8(frame.ReasonSuccess), second.Reason)
	require.Greater(t, second.MessageID, message.MessageID)
	require.Equal(t, message.MessageSeq+1, second.MessageSeq)

	suite.RequireConversationEventuallyWithin(t, *target.MustNode(1), "backup-e2e-sender", "backup-e2e-recipient", 10*time.Second, func(item suite.ConversationListItem) error {
		if item.LastMessage == nil || item.LastMessage.MessageID != uint64(second.MessageID) || item.LastMessage.MessageSeq != second.MessageSeq {
			return fmt.Errorf("post-restore conversation tail does not match new message")
		}
		if item.LastMessage.ClientMsgNo != "backup-e2e-message-2" || string(item.LastMessage.Payload) != secondPayload {
			return fmt.Errorf("post-restore conversation identity or payload mismatch")
		}
		return nil
	})
	return recoveryDrillResult{
		CheckpointID:         delta.Checkpoint.ID,
		RestoredMessages:     restoredMessages,
		ControllerFailover:   true,
		SlotFailover:         true,
		DataNodeFailover:     true,
		RestoreFailover:      true,
		RepositoryRepair:     true,
		DualCorruptionRebase: true,
		GarbageRoleProbe:     true,
	}
}

func requireConversationEventuallyAcrossNodes(
	t *testing.T,
	cluster *suite.StartedCluster,
	uid, channelID string,
	timeout time.Duration,
	check func(suite.ConversationListItem) error,
) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastPage suite.ConversationListPage
	var lastErr error
	for time.Now().Before(deadline) {
		for index := range cluster.Nodes {
			node := &cluster.Nodes[index]
			if node.Process == nil || !node.Process.Running() {
				continue
			}
			remaining := time.Until(deadline)
			if remaining <= 0 {
				break
			}
			attemptTimeout := min(2*time.Second, remaining)
			ctx, cancel := context.WithTimeout(
				context.Background(), attemptTimeout,
			)
			page, err := suite.PostConversationList(
				ctx, node.APIAddr(), uid, 10,
			)
			cancel()
			if err != nil {
				lastErr = err
				continue
			}
			lastPage = page
			item, found := suite.FindConversation(page, channelID)
			if !found {
				lastErr = fmt.Errorf(
					"conversation %s not found on node %d",
					channelID, node.Spec.ID,
				)
				continue
			}
			if err := check(item); err != nil {
				lastErr = err
				continue
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf(
		"conversation %s for uid %s did not converge across nodes: lastPage=%#v lastErr=%v\n%s",
		channelID, uid, lastPage, lastErr, cluster.DumpDiagnostics(),
	)
}

func exerciseSourceLeaderFailures(t *testing.T, cluster *suite.StartedCluster, token string) {
	t.Helper()
	controllerLeader := currentControllerLeader(t, cluster, token)
	require.NoError(t, cluster.MustNode(controllerLeader).Stop(), cluster.DumpDiagnostics())
	survivors := otherNodeIDs(controllerLeader)
	waitForManagerTCPs(t, cluster, survivors, 20*time.Second)
	_ = publishCheckpointEventually(t, cluster, token, 20*time.Second)
	require.NoError(t, cluster.StartStoppedNode(controllerLeader), cluster.DumpDiagnostics())
	cluster.WaitNodesReady(t, []uint64{controllerLeader}, 30*time.Second)

	const faultChannelID = "backup-e2e-slot-leader-fault"
	prepareFailureChannel(t, cluster, faultChannelID)
	hashSlot := hashslot.HashSlotForKey(faultChannelID, 16)
	slotLeader := currentHashSlotLeader(t, cluster, token, hashSlot)
	require.NoError(t, cluster.MustNode(slotLeader).Stop(), cluster.DumpDiagnostics())
	survivors = otherNodeIDs(slotLeader)
	waitForManagerTCPs(t, cluster, survivors, 20*time.Second)
	require.NotEqual(
		t,
		slotLeader,
		waitForHashSlotLeaderChange(
			t, cluster, token, hashSlot, slotLeader, 20*time.Second,
		),
	)
	appendMessageDuringFailure(t, cluster, survivors[0], faultChannelID, "slot-leader")
	require.NoError(t, cluster.StartStoppedNode(slotLeader), cluster.DumpDiagnostics())
	cluster.WaitNodesReady(t, []uint64{slotLeader}, 30*time.Second)

	currentController := currentControllerLeader(t, cluster, token)
	currentSlotLeader := currentHashSlotLeader(t, cluster, token, hashSlot)
	dataNode := uint64(0)
	for nodeID := uint64(1); nodeID <= 3; nodeID++ {
		if nodeID != currentController && nodeID != currentSlotLeader {
			dataNode = nodeID
			break
		}
	}
	if dataNode == 0 {
		for nodeID := uint64(1); nodeID <= 3; nodeID++ {
			if nodeID != currentSlotLeader {
				dataNode = nodeID
				break
			}
		}
	}
	require.NotZero(t, dataNode)
	require.NoError(t, cluster.MustNode(dataNode).Stop(), cluster.DumpDiagnostics())
	survivors = otherNodeIDs(dataNode)
	waitForManagerTCPs(t, cluster, survivors, 20*time.Second)
	appendMessageDuringFailure(t, cluster, survivors[0], faultChannelID, "data-node")
	require.NoError(t, cluster.StartStoppedNode(dataNode), cluster.DumpDiagnostics())
	cluster.WaitNodesReady(t, []uint64{dataNode}, 30*time.Second)
}

func waitForManagerTCPs(
	t *testing.T,
	cluster *suite.StartedCluster,
	nodeIDs []uint64,
	timeout time.Duration,
) {
	t.Helper()
	for _, nodeID := range nodeIDs {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		err := suite.WaitTCPReady(ctx, cluster.MustNode(nodeID).ManagerAddr())
		cancel()
		require.NoError(t, err, cluster.DumpDiagnostics())
	}
}

func exerciseRestoreLeaderFailure(
	t *testing.T,
	cluster *suite.StartedCluster,
	token string,
	leader uint64,
) {
	t.Helper()
	require.NoError(t, cluster.MustNode(leader).Stop(), cluster.DumpDiagnostics())
	waitForManagerTCPs(t, cluster, otherNodeIDs(leader), 20*time.Second)
	waitForSurvivorRestoreStatus(t, cluster, token, 2*time.Second, 20*time.Second)
	require.NoError(t, cluster.StartStoppedNode(leader), cluster.DumpDiagnostics())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(
		t,
		suite.WaitTCPReady(ctx, cluster.MustNode(leader).ManagerAddr()),
		cluster.DumpDiagnostics(),
	)
}

func waitForSurvivorRestoreStatus(
	t *testing.T,
	cluster *suite.StartedCluster,
	token string,
	stableFor time.Duration,
	timeout time.Duration,
) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	stableSince := time.Time{}
	var last restoreStatus
	var lastErr error
	for time.Now().Before(deadline) {
		last = restoreStatus{}
		lastErr = managerRequestError(
			cluster, token, http.MethodGet, "/manager/restore/status", nil, &last,
		)
		if lastErr == nil && last.Plan != nil &&
			(last.Plan.Status == "installing" || last.Plan.Status == "installed") {
			if stableSince.IsZero() {
				stableSince = time.Now()
			}
			if time.Since(stableSince) >= stableFor {
				return
			}
		} else {
			stableSince = time.Time{}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf(
		"surviving restore Managers did not retain progress: status=%+v err=%v\n%s",
		last.Plan, lastErr, cluster.DumpDiagnostics(),
	)
}

func exerciseRepositoryIntegrityRepair(
	t *testing.T,
	cluster *suite.StartedCluster,
	token string,
	root string,
) {
	t.Helper()
	beforeCorruptions := backupMetricTotal(
		t, cluster, "wukongim_backup_audit_corruptions_total",
		nil,
	)
	beforeRepairBytes := backupMetricTotal(
		t, cluster, "wukongim_backup_audit_repair_bytes_total",
		nil,
	)
	setRepositoryCorruption(t, root, "primary", "sticky")
	waitForBackupMetricIncrease(
		t, cluster, "wukongim_backup_audit_corruptions_total",
		nil, beforeCorruptions, 30*time.Second,
	)
	waitForBackupMetricIncrease(
		t, cluster, "wukongim_backup_audit_repair_bytes_total",
		nil, beforeRepairBytes, 30*time.Second,
	)
	waitForBackupHealthy(t, cluster, token, 30*time.Second)
	_ = publishCheckpointEventually(t, cluster, token, 30*time.Second)

	beforeCorruptions = backupMetricTotal(
		t, cluster, "wukongim_backup_audit_corruptions_total",
		nil,
	)
	beforeRepairBytes = backupMetricTotal(
		t, cluster, "wukongim_backup_audit_repair_bytes_total",
		nil,
	)
	setRepositoryCorruption(t, root, "secondary", "sticky")
	waitForBackupMetricIncrease(
		t, cluster, "wukongim_backup_audit_corruptions_total",
		nil, beforeCorruptions, 30*time.Second,
	)
	waitForBackupMetricIncrease(
		t, cluster, "wukongim_backup_audit_repair_bytes_total",
		nil, beforeRepairBytes, 30*time.Second,
	)
	waitForBackupHealthy(t, cluster, token, 30*time.Second)
	_ = publishCheckpointEventually(t, cluster, token, 30*time.Second)

	frontiers := backupDurableFrontiers(t, cluster, token)
	const dualCorruptionChannel = "backup-e2e-dual-corruption"
	_ = appendGroupMessages(
		t, cluster, dualCorruptionChannel, frame.ChannelTypeGroup, 1,
	)
	hashSlot := hashslot.HashSlotForKey(dualCorruptionChannel, 16)
	waitForDurableFrontiers(
		t, cluster, token, frontiers,
		[]uint16{hashSlot}, []uint16{hashSlot}, 20*time.Second,
	)
	_ = publishCheckpointEventually(t, cluster, token, 20*time.Second)
	beforeRebases := backupMetricTotal(
		t, cluster, "wukongim_backup_slot_rebases_total",
		map[string]string{
			"reason": "audit_corruption", "outcome": "success",
		},
	)
	allRepositoriesTrigger := setRepositoryCorruption(
		t, root, "all", "sticky",
	)
	waitForBackupMetricIncrease(
		t, cluster, "wukongim_backup_slot_rebases_total",
		map[string]string{
			"reason": "audit_corruption", "outcome": "success",
		},
		beforeRebases, 60*time.Second,
	)
	clearRepositoryCorruption(t, allRepositoriesTrigger)
	waitForBackupHealthy(t, cluster, token, 60*time.Second)
}

func setRepositoryCorruption(
	t *testing.T,
	root string,
	repository string,
	mode string,
) string {
	t.Helper()
	faultDir := filepath.Join(root, "faults")
	require.NoError(t, os.MkdirAll(faultDir, 0o700))
	trigger := filepath.Join(faultDir, repository+".corrupt")
	require.NoError(t, os.WriteFile(trigger, []byte(mode), 0o600))
	return trigger
}

func clearRepositoryCorruption(t *testing.T, trigger string) {
	t.Helper()
	for _, path := range []string{
		trigger,
		filepath.Join(filepath.Dir(trigger), "sticky.key"),
	} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			t.Fatalf("clear repository corruption trigger %s: %v", path, err)
		}
	}
}

func waitForBackupMetricIncrease(
	t *testing.T,
	cluster *suite.StartedCluster,
	name string,
	labels map[string]string,
	before float64,
	timeout time.Duration,
) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last float64
	for time.Now().Before(deadline) {
		last = backupMetricTotal(t, cluster, name, labels)
		if last > before {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf(
		"backup metric %s%v=%v, want >%v\n%s",
		name, labels, last, before, cluster.DumpDiagnostics(),
	)
}

func backupMetricTotal(
	t *testing.T,
	cluster *suite.StartedCluster,
	name string,
	labels map[string]string,
) float64 {
	t.Helper()
	var total float64
	for _, node := range cluster.Nodes {
		if node.Process == nil || !node.Process.Running() {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		samples, err := suite.FetchMetricSamples(ctx, node.APIAddr())
		cancel()
		if err != nil {
			continue
		}
		for _, sample := range samples {
			if sample.Name != name || !metricLabelsContain(sample.Labels, labels) {
				continue
			}
			total += sample.Value
		}
	}
	return total
}

func metricLabelsContain(actual, required map[string]string) bool {
	for key, value := range required {
		if actual[key] != value {
			return false
		}
	}
	return true
}

func waitForBackupHealthy(
	t *testing.T,
	cluster *suite.StartedCluster,
	token string,
	timeout time.Duration,
) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	stableSince := time.Time{}
	var last backupStatus
	var lastErr error
	for time.Now().Before(deadline) {
		allHealthy := true
		running := 0
		for index := range cluster.Nodes {
			node := &cluster.Nodes[index]
			if node.Process == nil || !node.Process.Running() {
				continue
			}
			running++
			last = backupStatus{}
			lastErr = managerNodeRequestError(
				node, token, http.MethodGet,
				"/manager/backups/status", nil, &last,
			)
			if lastErr != nil || last.Health != "healthy" {
				allHealthy = false
				break
			}
		}
		if allHealthy && running > 0 {
			if stableSince.IsZero() {
				stableSince = time.Now()
			} else if time.Since(stableSince) >= time.Second {
				return
			}
		} else {
			stableSince = time.Time{}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf(
		"backup did not recover healthy state: status=%+v err=%v\n%s",
		last, lastErr, cluster.DumpDiagnostics(),
	)
}

func currentControllerLeader(t *testing.T, cluster *suite.StartedCluster, token string) uint64 {
	t.Helper()
	var response managerNodesResponse
	managerRequest(t, cluster, token, http.MethodGet, "/manager/nodes", nil, &response)
	require.NotZero(t, response.ControllerLeaderID)
	return response.ControllerLeaderID
}

func currentBackupControllerLeader(
	t *testing.T,
	cluster *suite.StartedCluster,
	token string,
	timeout time.Duration,
) uint64 {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastLeader uint64
	var stableSince time.Time
	var lastErr error
	for time.Now().Before(deadline) {
		var response managerNodesResponse
		lastErr = managerRequestError(
			cluster, token, http.MethodGet, "/manager/nodes", nil, &response,
		)
		if lastErr == nil && response.ControllerLeaderID != 0 {
			if response.ControllerLeaderID != lastLeader {
				lastLeader = response.ControllerLeaderID
				stableSince = time.Now()
			} else if time.Since(stableSince) >= 500*time.Millisecond {
				return response.ControllerLeaderID
			}
		} else {
			lastLeader = 0
			stableSince = time.Time{}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf(
		"restore Controller leader did not converge through Manager nodes: leader=%d err=%v\n%s",
		lastLeader, lastErr, cluster.DumpDiagnostics(),
	)
	return 0
}

func currentHashSlotLeader(
	t *testing.T,
	cluster *suite.StartedCluster,
	token string,
	hashSlot uint16,
) uint64 {
	t.Helper()
	var response managerSlotsResponse
	managerRequest(t, cluster, token, http.MethodGet, "/manager/slots", nil, &response)
	for _, slot := range response.Items {
		if slot.HashSlots == nil {
			continue
		}
		for _, item := range slot.HashSlots.Items {
			if item == hashSlot {
				require.NotZero(t, slot.Runtime.LeaderID)
				return slot.Runtime.LeaderID
			}
		}
	}
	t.Fatalf("logical Hash Slot %d has no observed Slot Leader\n%s", hashSlot, cluster.DumpDiagnostics())
	return 0
}

func waitForHashSlotLeaderChange(
	t *testing.T,
	cluster *suite.StartedCluster,
	token string,
	hashSlot uint16,
	previous uint64,
	timeout time.Duration,
) uint64 {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last uint64
	for time.Now().Before(deadline) {
		var response managerSlotsResponse
		if err := managerRequestError(
			cluster, token, http.MethodGet, "/manager/slots", nil, &response,
		); err == nil {
			for _, slot := range response.Items {
				if slot.HashSlots == nil {
					continue
				}
				for _, item := range slot.HashSlots.Items {
					if item == hashSlot {
						last = slot.Runtime.LeaderID
					}
				}
			}
			if last != 0 && last != previous {
				return last
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf(
		"logical Hash Slot %d Leader did not change from %d: last=%d\n%s",
		hashSlot, previous, last, cluster.DumpDiagnostics(),
	)
	return 0
}

func prepareFailureChannel(
	t *testing.T,
	cluster *suite.StartedCluster,
	channelID string,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	require.NoError(
		t,
		suite.PostChannel(
			ctx, cluster.MustNode(1).APIAddr(),
			map[string]any{
				"channel_id": channelID, "channel_type": frame.ChannelTypeGroup,
				"subscribers": []string{
					"backup-e2e-fault-sender", "backup-e2e-fault-recipient",
				},
			},
		),
		cluster.DumpDiagnostics(),
	)
	appendMessageDuringFailure(
		t, cluster, 1, channelID, "warmup",
	)
}

func appendMessageDuringFailure(
	t *testing.T,
	cluster *suite.StartedCluster,
	nodeID uint64,
	channelID string,
	phase string,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	message, err := suite.PostMessageSendEventually(
		ctx,
		cluster.MustNode(nodeID).APIAddr(),
		map[string]any{
			"from_uid":      "backup-e2e-fault-sender",
			"channel_id":    channelID,
			"channel_type":  frame.ChannelTypeGroup,
			"client_msg_no": "backup-e2e-fault-" + phase,
			"payload": base64.StdEncoding.EncodeToString(
				[]byte("foreground SEND during " + phase + " failure"),
			),
		},
	)
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equal(t, uint8(frame.ReasonSuccess), message.Reason)
}

func otherNodeIDs(excluded uint64) []uint64 {
	result := make([]uint64, 0, 2)
	for nodeID := uint64(1); nodeID <= 3; nodeID++ {
		if nodeID != excluded {
			result = append(result, nodeID)
		}
	}
	return result
}

func appendGroupMessages(t *testing.T, cluster *suite.StartedCluster, channelID string, channelType uint8, count int) []suite.MessageSendResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	require.NoError(t, suite.PostChannel(ctx, cluster.MustNode(1).APIAddr(), map[string]any{
		"channel_id": channelID, "channel_type": channelType,
		"subscribers": []string{"backup-erasure-sender", "backup-erasure-member"},
	}), cluster.DumpDiagnostics())
	messages := make([]suite.MessageSendResponse, 0, count)
	for index := 0; index < count; index++ {
		message, err := suite.PostMessageSendEventually(ctx, cluster.MustNode(1).APIAddr(), map[string]any{
			"from_uid": "backup-erasure-sender", "channel_id": channelID, "channel_type": channelType,
			"client_msg_no": fmt.Sprintf("backup-erasure-%d-%d", time.Now().UnixNano(), index),
			"payload":       base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("erasure payload %d", index))),
		})
		require.NoError(t, err, cluster.DumpDiagnostics())
		require.Equal(t, uint8(frame.ReasonSuccess), message.Reason)
		messages = append(messages, message)
	}
	return messages
}

func advanceMessageRetentionEventually(
	t *testing.T,
	cluster *suite.StartedCluster,
	token, channelID string,
	channelType uint8,
	throughSeq uint64,
) retentionResponse {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var last retentionResponse
	var lastErr error
	for time.Now().Before(deadline) {
		lastErr = managerRequestError(
			cluster, token, http.MethodPost, "/manager/messages/retention",
			map[string]any{
				"channel_id": channelID, "channel_type": channelType,
				"through_seq": throughSeq,
			},
			&last,
		)
		if lastErr == nil && last.Status == "advanced" {
			return last
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("permanent retention did not advance: last=%+v err=%v\n%s", last, lastErr, cluster.DumpDiagnostics())
	return retentionResponse{}
}

func requireManagerMessageSeqsEventually(t *testing.T, cluster *suite.StartedCluster, token, channelID string, channelType uint8, want []uint64) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	query := url.Values{}
	query.Set("channel_id", channelID)
	query.Set("channel_type", fmt.Sprintf("%d", channelType))
	query.Set("limit", "10")
	var last managerMessagePage
	var lastErr error
	for time.Now().Before(deadline) {
		last = managerMessagePage{}
		lastErr = managerRequestError(cluster, token, http.MethodGet, "/manager/messages?"+query.Encode(), nil, &last)
		got := make([]uint64, 0, len(last.Items))
		for _, item := range last.Items {
			got = append(got, item.MessageSeq)
		}
		if lastErr == nil && equalUint64s(got, want) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("manager message sequence mismatch: items=%+v want=%v err=%v\n%s", last.Items, want, lastErr, cluster.DumpDiagnostics())
}

func equalUint64s(left, right []uint64) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func localStorageQualification(t *testing.T) storageQualification {
	t.Helper()
	root := t.TempDir()
	return storageQualification{
		FileRoot:         root,
		CorruptionRoot:   root,
		Provider:         "aliyun",
		RepositoryID:     "e2e-repository",
		SourceGeneration: "source-generation",
		TargetGeneration: "target-generation",
		KMSKeyID:         "e2e-encryption-key",
		SigningKeyID:     "e2e-signing-key",
		KMSRegion:        "e2e-kms",
		KMSEndpoint:      "https://kms.e2e.invalid",
		KMSRoleARN:       "acs:ram::e2e:role/backup-kms",
		ObjectLockDays:   7,
		Primary: repositoryQualification{
			Endpoint:       "https://primary.e2e.invalid",
			Region:         "e2e-primary",
			Bucket:         "primary",
			Prefix:         "cluster",
			AccessRoleARN:  "acs:ram::e2e:role/backup-primary",
			RepairRoleARN:  "arn:e2e:primary:repair",
			GarbageRoleARN: "arn:e2e:primary:garbage",
		},
		Secondary: repositoryQualification{
			Endpoint:       "https://secondary.e2e.invalid",
			Region:         "e2e-secondary",
			Bucket:         "secondary",
			Prefix:         "cluster",
			AccessRoleARN:  "acs:ram::e2e:role/backup-secondary",
			RepairRoleARN:  "arn:e2e:secondary:repair",
			GarbageRoleARN: "arn:e2e:secondary:garbage",
		},
	}
}

func productionStorageQualification(t *testing.T) storageQualification {
	t.Helper()
	required := func(name string) string {
		t.Helper()
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			t.Fatalf("%s is required for production backup qualification", name)
		}
		return value
	}
	objectLockDays, err := strconv.Atoi(required("WK_E2E_BACKUP_OBJECT_LOCK_DAYS"))
	require.NoError(t, err, "WK_E2E_BACKUP_OBJECT_LOCK_DAYS must be an integer")
	qualification := storageQualification{
		CorruptionRoot:   t.TempDir(),
		Provider:         required("WK_E2E_BACKUP_PROVIDER"),
		RepositoryID:     required("WK_E2E_BACKUP_REPOSITORY_ID"),
		SourceGeneration: required("WK_E2E_BACKUP_SOURCE_GENERATION"),
		TargetGeneration: required("WK_E2E_BACKUP_TARGET_GENERATION"),
		KMSKeyID:         required("WK_E2E_BACKUP_KMS_KEY_ID"),
		SigningKeyID:     required("WK_E2E_BACKUP_SIGNING_KEY_ID"),
		KMSRegion:        required("WK_E2E_BACKUP_KMS_REGION"),
		KMSEndpoint:      strings.TrimSpace(os.Getenv("WK_E2E_BACKUP_KMS_ENDPOINT")),
		KMSRoleARN:       required("WK_E2E_BACKUP_KMS_ROLE_ARN"),
		ObjectLockDays:   objectLockDays,
		ProductionRunID:  required("WK_E2E_BACKUP_RUN_ID"),
		ProductionCommit: required("WK_E2E_BACKUP_COMMIT_SHA"),
		Primary: repositoryQualification{
			Endpoint:       required("WK_E2E_BACKUP_PRIMARY_ENDPOINT"),
			Region:         required("WK_E2E_BACKUP_PRIMARY_REGION"),
			Bucket:         required("WK_E2E_BACKUP_PRIMARY_BUCKET"),
			Prefix:         required("WK_E2E_BACKUP_PRIMARY_PREFIX"),
			AccessRoleARN:  required("WK_E2E_BACKUP_PRIMARY_ACCESS_ROLE_ARN"),
			RepairRoleARN:  required("WK_E2E_BACKUP_PRIMARY_REPAIR_ROLE_ARN"),
			GarbageRoleARN: required("WK_E2E_BACKUP_PRIMARY_GARBAGE_ROLE_ARN"),
		},
		Secondary: repositoryQualification{
			Endpoint:       required("WK_E2E_BACKUP_SECONDARY_ENDPOINT"),
			Region:         required("WK_E2E_BACKUP_SECONDARY_REGION"),
			Bucket:         required("WK_E2E_BACKUP_SECONDARY_BUCKET"),
			Prefix:         required("WK_E2E_BACKUP_SECONDARY_PREFIX"),
			AccessRoleARN:  required("WK_E2E_BACKUP_SECONDARY_ACCESS_ROLE_ARN"),
			RepairRoleARN:  required("WK_E2E_BACKUP_SECONDARY_REPAIR_ROLE_ARN"),
			GarbageRoleARN: required("WK_E2E_BACKUP_SECONDARY_GARBAGE_ROLE_ARN"),
		},
	}
	require.Empty(t, os.Getenv("WUKONGIM_BACKUP_E2E_FILE_ROOT"), "production qualification cannot use the file repository substitute")
	require.Equal(t, "aliyun", qualification.Provider)
	require.GreaterOrEqual(t, qualification.ObjectLockDays, 7)
	require.NotEqual(t, strings.ToLower(qualification.Primary.Region), strings.ToLower(qualification.Secondary.Region))
	require.NotEqual(
		t,
		strings.ToLower(qualification.Primary.Endpoint)+"\x00"+qualification.Primary.Bucket,
		strings.ToLower(qualification.Secondary.Endpoint)+"\x00"+qualification.Secondary.Bucket,
	)
	return qualification
}

func localBackupNodeEnvironment(nodeID uint64, root string) suite.Option {
	return suite.WithNodeEnv(nodeID,
		"WUKONGIM_BACKUP_E2E_FILE_ROOT="+root,
	)
}

func sourceBackupConfig(qualification storageQualification, nodeID uint64) map[string]string {
	return map[string]string{
		"WK_BACKUP_ENABLED":                             "true",
		"WK_BACKUP_PROVIDER":                            qualification.Provider,
		"WK_BACKUP_QUALIFICATION_GATE":                  "backup-vnext-production-v2",
		"WK_BACKUP_REPOSITORY_ID":                       qualification.RepositoryID,
		"WK_BACKUP_SOURCE_GENERATION":                   qualification.SourceGeneration,
		"WK_BACKUP_STAGING_DIR":                         qualificationStagingDir(qualification, "source", nodeID),
		"WK_BACKUP_KMS_KEY_ID":                          qualification.KMSKeyID,
		"WK_BACKUP_SIGNING_KEY_ID":                      qualification.SigningKeyID,
		"WK_BACKUP_KMS_REGION":                          qualification.KMSRegion,
		"WK_BACKUP_KMS_ENDPOINT":                        qualification.KMSEndpoint,
		"WK_BACKUP_KMS_ROLE_ARN":                        qualification.KMSRoleARN,
		"WK_BACKUP_CAPTURE_RECONCILE_INTERVAL":          "200ms",
		"WK_BACKUP_CHECKPOINT_INTERVAL":                 "1h",
		"WK_BACKUP_BASELINE_CHUNK_BYTES":                "1048576",
		"WK_BACKUP_TARGET_SEGMENT_BYTES":                "1048576",
		"WK_BACKUP_MAX_SEGMENT_OPEN_DURATION":           "500ms",
		"WK_BACKUP_STAGING_MAX_BYTES":                   "67108864",
		"WK_BACKUP_WORKER_COUNT":                        "2",
		"WK_BACKUP_AUDIT_INTERVAL":                      "2s",
		"WK_BACKUP_AUDIT_SCRUB_INTERVAL":                "24h",
		"WK_BACKUP_GARBAGE_COLLECTION_INTERVAL":         "1h",
		"WK_BACKUP_GARBAGE_SAFETY_WINDOW":               "168h",
		"WK_BACKUP_GARBAGE_MAX_REQUESTS_PER_REPOSITORY": "256",
		"WK_BACKUP_GARBAGE_MAX_BYTES_PER_REPOSITORY":    "1073741824",
		"WK_BACKUP_RETENTION_MONTHLY_MONTHS":            "0",
		"WK_BACKUP_OBJECT_LOCK_DAYS":                    strconv.Itoa(qualification.ObjectLockDays),
		"WK_BACKUP_PRIMARY_ENDPOINT":                    qualification.Primary.Endpoint,
		"WK_BACKUP_PRIMARY_REGION":                      qualification.Primary.Region,
		"WK_BACKUP_PRIMARY_BUCKET":                      qualification.Primary.Bucket,
		"WK_BACKUP_PRIMARY_PREFIX":                      qualification.Primary.Prefix,
		"WK_BACKUP_PRIMARY_ACCESS_ROLE_ARN":             qualification.Primary.AccessRoleARN,
		"WK_BACKUP_PRIMARY_REPAIR_ROLE_ARN":             qualification.Primary.RepairRoleARN,
		"WK_BACKUP_PRIMARY_GARBAGE_ROLE_ARN":            qualification.Primary.GarbageRoleARN,
		"WK_BACKUP_SECONDARY_ENDPOINT":                  qualification.Secondary.Endpoint,
		"WK_BACKUP_SECONDARY_REGION":                    qualification.Secondary.Region,
		"WK_BACKUP_SECONDARY_BUCKET":                    qualification.Secondary.Bucket,
		"WK_BACKUP_SECONDARY_PREFIX":                    qualification.Secondary.Prefix,
		"WK_BACKUP_SECONDARY_ACCESS_ROLE_ARN":           qualification.Secondary.AccessRoleARN,
		"WK_BACKUP_SECONDARY_REPAIR_ROLE_ARN":           qualification.Secondary.RepairRoleARN,
		"WK_BACKUP_SECONDARY_GARBAGE_ROLE_ARN":          qualification.Secondary.GarbageRoleARN,
		"WK_MANAGER_AUTH_ON":                            "true",
		"WK_MANAGER_JWT_SECRET":                         "e2e-source-jwt-secret",
		"WK_MANAGER_JWT_ISSUER":                         "wukongim-e2e",
		"WK_MANAGER_JWT_EXPIRE":                         "1h",
		"WK_MANAGER_USERS":                              `[{"username":"source-admin","password":"source-secret","permissions":[{"resource":"*","actions":["*"]},{"resource":"cluster.backup.source_fence","actions":["w"]}]}]`,
	}
}

func targetRestoreConfig(qualification storageQualification, nodeID uint64) map[string]string {
	return map[string]string{
		"WK_CLUSTER_ID":                       "wukongim-e2e-restore",
		"WK_BACKUP_RESTORE_MODE":              "true",
		"WK_BACKUP_PROVIDER":                  qualification.Provider,
		"WK_BACKUP_REPOSITORY_ID":             qualification.RepositoryID,
		"WK_BACKUP_TARGET_GENERATION":         qualification.TargetGeneration,
		"WK_BACKUP_STAGING_DIR":               qualificationStagingDir(qualification, "target", nodeID),
		"WK_BACKUP_KMS_KEY_ID":                qualification.KMSKeyID,
		"WK_BACKUP_SIGNING_KEY_ID":            qualification.SigningKeyID,
		"WK_BACKUP_KMS_REGION":                qualification.KMSRegion,
		"WK_BACKUP_KMS_ENDPOINT":              qualification.KMSEndpoint,
		"WK_BACKUP_KMS_ROLE_ARN":              qualification.KMSRoleARN,
		"WK_BACKUP_BASELINE_CHUNK_BYTES":      "1048576",
		"WK_BACKUP_STAGING_MAX_BYTES":         "67108864",
		"WK_BACKUP_WORKER_COUNT":              "2",
		"WK_BACKUP_PRIMARY_ENDPOINT":          qualification.Primary.Endpoint,
		"WK_BACKUP_PRIMARY_REGION":            qualification.Primary.Region,
		"WK_BACKUP_PRIMARY_BUCKET":            qualification.Primary.Bucket,
		"WK_BACKUP_PRIMARY_PREFIX":            qualification.Primary.Prefix,
		"WK_BACKUP_PRIMARY_ACCESS_ROLE_ARN":   qualification.Primary.AccessRoleARN,
		"WK_BACKUP_SECONDARY_ENDPOINT":        qualification.Secondary.Endpoint,
		"WK_BACKUP_SECONDARY_REGION":          qualification.Secondary.Region,
		"WK_BACKUP_SECONDARY_BUCKET":          qualification.Secondary.Bucket,
		"WK_BACKUP_SECONDARY_PREFIX":          qualification.Secondary.Prefix,
		"WK_BACKUP_SECONDARY_ACCESS_ROLE_ARN": qualification.Secondary.AccessRoleARN,
		"WK_MANAGER_AUTH_ON":                  "true",
		"WK_MANAGER_JWT_SECRET":               "e2e-restore-jwt-secret",
		"WK_MANAGER_JWT_ISSUER":               "wukongim-e2e",
		"WK_MANAGER_JWT_EXPIRE":               "1h",
		"WK_MANAGER_USERS":                    `[{"username":"restore-admin","password":"restore-secret","permissions":[{"resource":"cluster.backup","actions":["r","w"]},{"resource":"cluster.restore.activation","actions":["w"]},{"resource":"cluster.channel","actions":["r"]}]}]`,
	}
}

func qualificationStagingDir(qualification storageQualification, role string, nodeID uint64) string {
	root := qualification.FileRoot
	if root == "" {
		root = os.TempDir()
	}
	return filepath.Join(
		root,
		fmt.Sprintf(
			"wukongim-backup-%s-%s-%d",
			qualification.ProductionRunID,
			role,
			nodeID,
		),
	)
}

func stopCluster(t *testing.T, cluster *suite.StartedCluster) {
	t.Helper()
	for index := len(cluster.Nodes) - 1; index >= 0; index-- {
		require.NoError(t, cluster.Nodes[index].Stop(), cluster.DumpDiagnostics())
	}
}

func conversationPersistedRows(t *testing.T, cluster *suite.StartedCluster) float64 {
	t.Helper()
	var persisted float64
	for _, node := range cluster.Nodes {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		value, err := suite.FetchMetricValue(ctx, node.APIAddr(), "wukongim_conversation_active_flush_rows_total", map[string]string{
			"result": "ok", "stage": "persisted", "reason": "none",
		})
		cancel()
		require.NoError(t, err, cluster.DumpDiagnostics())
		persisted += value
	}
	return persisted
}

func waitForConversationDurable(
	t *testing.T,
	cluster *suite.StartedCluster,
	persistedBaseline float64,
	timeout time.Duration,
) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastPersisted float64
	lastDirty := make(map[uint64]float64, len(cluster.Nodes))
	var lastErr error
	for time.Now().Before(deadline) {
		persisted := float64(0)
		allClean := true
		for _, node := range cluster.Nodes {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			value, err := suite.FetchMetricValue(ctx, node.APIAddr(), "wukongim_conversation_active_flush_rows_total", map[string]string{
				"result": "ok", "stage": "persisted", "reason": "none",
			})
			cancel()
			if err == nil {
				persisted += value
			} else {
				lastErr = err
			}
			ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
			dirty, err := suite.FetchMetricValue(ctx, node.APIAddr(), "wukongim_conversation_active_cache_dirty_rows", nil)
			cancel()
			if err != nil {
				lastErr = err
				allClean = false
				continue
			}
			lastDirty[node.Spec.ID] = dirty
			if dirty != 0 {
				allClean = false
			}
		}
		lastPersisted = persisted
		if persisted > persistedBaseline && allClean {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf(
		"conversation active rows were not durably flushed after baseline: baseline=%v persisted=%v dirty=%v err=%v\n%s",
		persistedBaseline, lastPersisted, lastDirty, lastErr,
		cluster.DumpDiagnostics(),
	)
}

func startRestoreCluster(t *testing.T, qualification storageQualification) *suite.StartedCluster {
	t.Helper()
	options := []suite.Option{suite.WithManagerHTTP()}
	for nodeID := uint64(1); nodeID <= 3; nodeID++ {
		options = append(options,
			suite.WithNodeConfigOverrides(nodeID, targetRestoreConfig(qualification, nodeID)),
		)
		if qualification.FileRoot != "" {
			options = append(options, localBackupNodeEnvironment(nodeID, qualification.FileRoot))
		}
	}
	target := suite.New(t).StartThreeNodeCluster(options...)
	ctx, cancel := context.WithTimeout(context.Background(), suite.BackupClusterReadyTimeout)
	defer cancel()
	for nodeID := uint64(1); nodeID <= 3; nodeID++ {
		require.NoError(t, suite.WaitTCPReady(ctx, target.MustNode(nodeID).ManagerAddr()), target.DumpDiagnostics())
	}
	return target
}

func restartActivatedCluster(t *testing.T, cluster *suite.StartedCluster) {
	t.Helper()
	stopCluster(t, cluster)
	overrides := make(map[uint64]map[string]string, len(cluster.Nodes))
	for _, node := range cluster.Nodes {
		overrides[node.Spec.ID] = map[string]string{"WK_BACKUP_RESTORE_MODE": "false"}
	}
	require.NoError(t, cluster.ReconfigureStoppedNodes(overrides), cluster.DumpDiagnostics())
	for nodeID := uint64(1); nodeID <= 3; nodeID++ {
		require.NoError(t, cluster.StartStoppedNode(nodeID), cluster.DumpDiagnostics())
	}
	ctx, cancel := context.WithTimeout(context.Background(), suite.BackupClusterReadyTimeout)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())
}

func loginManager(
	t *testing.T,
	cluster *suite.StartedCluster,
	username, password string,
) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var login managerLogin
	_, err := suite.PostJSON(ctx, "http://"+cluster.MustNode(1).ManagerAddr()+"/manager/login", map[string]any{
		"username": username,
		"password": password,
	}, &login)
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.NotEmpty(t, login.AccessToken)
	return login.AccessToken
}

func createRestorePlan(
	t *testing.T,
	cluster *suite.StartedCluster,
	token string,
	publication checkpointPublication,
) restorePlan {
	t.Helper()
	var plan restorePlan
	managerRequest(t, cluster, token, http.MethodPost, "/manager/restore/plan", map[string]any{
		"checkpoint_id":      publication.Checkpoint.ID,
		"catalog_head_token": publication.CatalogHeadToken,
		"invalidate_tokens":  false,
	}, &plan)
	require.Equal(t, "planned", plan.Status)
	return plan
}

func startRestore(t *testing.T, cluster *suite.StartedCluster, token, planID string) restorePlan {
	t.Helper()
	var plan restorePlan
	managerRequest(t, cluster, token, http.MethodPost, "/manager/restore/"+planID+"/start", map[string]any{}, &plan)
	return plan
}

func verifyRestore(t *testing.T, cluster *suite.StartedCluster, token, planID string) restorePlan {
	t.Helper()
	var plan restorePlan
	managerRequest(t, cluster, token, http.MethodPost, "/manager/restore/"+planID+"/verify", map[string]any{}, &plan)
	return plan
}

func activateRestore(
	t *testing.T,
	cluster *suite.StartedCluster,
	token, planID string,
	receipt backupartifact.SourceFenceReceipt,
) restorePlan {
	t.Helper()
	var plan restorePlan
	managerRequest(t, cluster, token, http.MethodPost, "/manager/restore/"+planID+"/activate", map[string]any{
		"source_fence_receipt": receipt,
	}, &plan)
	return plan
}

func fenceSource(
	t *testing.T,
	cluster *suite.StartedCluster,
	token string,
	plan restorePlan,
) backupartifact.SourceFenceReceipt {
	t.Helper()
	request := map[string]any{
		"restore_plan_id":   plan.ID,
		"checkpoint_id":     plan.CheckpointID,
		"target_cluster_id": plan.TargetClusterID,
		"target_generation": plan.TargetGeneration,
	}
	deadline := time.Now().Add(20 * time.Second)
	var receipt backupartifact.SourceFenceReceipt
	var lastErr error
	for time.Now().Before(deadline) {
		receipt = backupartifact.SourceFenceReceipt{}
		lastErr = managerRequestError(
			cluster, token, http.MethodPost,
			"/manager/backups/source-fence", request, &receipt,
		)
		if lastErr == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.NoError(t, lastErr, cluster.DumpDiagnostics())
	require.Equal(t, plan.ID, receipt.RestorePlanID)
	require.Equal(t, plan.CheckpointSHA256, receipt.CheckpointSHA256)
	require.NotNil(t, receipt.Signature)
	require.NotZero(t, receipt.ConvergedAtUnixMillis)
	return receipt
}

func requireSourceWritesFenced(
	t *testing.T,
	cluster *suite.StartedCluster,
	connected *suite.WKProtoClient,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	response, err := suite.PostMessageSendEventually(
		ctx, cluster.MustNode(2).APIAddr(), map[string]any{
			"from_uid":      "backup-e2e-sender",
			"channel_id":    "backup-e2e-recipient",
			"channel_type":  frame.ChannelTypePerson,
			"client_msg_no": "must-be-rejected-after-source-fence",
			"payload": base64.StdEncoding.EncodeToString(
				[]byte("must not commit"),
			),
		},
	)
	require.True(
		t, err != nil || response.Reason != uint8(frame.ReasonSuccess),
		"source accepted a write after fence: response=%+v err=%v\n%s",
		response, err, cluster.DumpDiagnostics(),
	)
	sendErr := connected.SendFrame(&frame.SendPacket{
		ChannelID:   "backup-e2e-recipient",
		ChannelType: frame.ChannelTypePerson,
		ClientSeq:   99,
		ClientMsgNo: "wkproto-must-be-rejected-after-source-fence",
		Payload:     []byte("must not commit"),
	})
	if sendErr == nil {
		ack, ackErr := connected.ReadSendAck()
		require.True(
			t,
			ackErr != nil ||
				ack == nil ||
				ack.ReasonCode != frame.ReasonSuccess,
			"source accepted WKProto write after fence: ack=%+v err=%v\n%s",
			ack, ackErr, cluster.DumpDiagnostics(),
		)
	}
	for nodeID := uint64(1); nodeID <= 3; nodeID++ {
		readyCtx, readyCancel := context.WithTimeout(
			context.Background(), 3*time.Second,
		)
		request, requestErr := http.NewRequestWithContext(
			readyCtx, http.MethodGet,
			"http://"+cluster.MustNode(nodeID).APIAddr()+"/readyz",
			http.NoBody,
		)
		require.NoError(t, requestErr)
		readyResponse, requestErr := http.DefaultClient.Do(request)
		readyCancel()
		require.NoError(t, requestErr, cluster.DumpDiagnostics())
		_ = readyResponse.Body.Close()
		require.Equal(
			t, http.StatusServiceUnavailable, readyResponse.StatusCode,
			"node %d remained ready after source fence\n%s",
			nodeID, cluster.DumpDiagnostics(),
		)
	}
	rejected, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	defer func() { _ = rejected.Close() }()
	connectCtx, connectCancel := context.WithTimeout(
		context.Background(), 3*time.Second,
	)
	_, err = rejected.ConnectContext(
		connectCtx, cluster.MustNode(1).GatewayAddr(),
		"backup-source-fence-new-client",
		"backup-source-fence-new-device",
	)
	connectCancel()
	require.Error(
		t, err,
		"source accepted a new WKProto session after fence\n%s",
		cluster.DumpDiagnostics(),
	)
}

func requireRestoreTargetTrafficClosed(
	t *testing.T,
	cluster *suite.StartedCluster,
) {
	t.Helper()
	for nodeID := uint64(1); nodeID <= 3; nodeID++ {
		readyCtx, readyCancel := context.WithTimeout(
			context.Background(), 3*time.Second,
		)
		request, requestErr := http.NewRequestWithContext(
			readyCtx, http.MethodGet,
			"http://"+cluster.MustNode(nodeID).APIAddr()+"/readyz",
			http.NoBody,
		)
		require.NoError(t, requestErr)
		response, requestErr := http.DefaultClient.Do(request)
		readyCancel()
		if requestErr == nil {
			_ = response.Body.Close()
			require.Equal(
				t, http.StatusServiceUnavailable, response.StatusCode,
				"restore target node %d reported ordinary readiness before activation\n%s",
				nodeID, cluster.DumpDiagnostics(),
			)
		}
	}
	writeCtx, writeCancel := context.WithTimeout(
		context.Background(), 3*time.Second,
	)
	response, err := suite.PostMessageSendEventually(
		writeCtx, cluster.MustNode(1).APIAddr(), map[string]any{
			"from_uid":      "restore-target-pre-activation",
			"channel_id":    "restore-target-recipient",
			"channel_type":  frame.ChannelTypePerson,
			"client_msg_no": "must-be-rejected-before-activation",
			"payload": base64.StdEncoding.EncodeToString(
				[]byte("must not commit"),
			),
		},
	)
	writeCancel()
	require.True(
		t, err != nil || response.Reason != uint8(frame.ReasonSuccess),
		"restore target accepted HTTP write before activation: response=%+v err=%v\n%s",
		response, err, cluster.DumpDiagnostics(),
	)
	client, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	defer func() { _ = client.Close() }()
	connectCtx, connectCancel := context.WithTimeout(
		context.Background(), 3*time.Second,
	)
	_, err = client.ConnectContext(
		connectCtx, cluster.MustNode(1).GatewayAddr(),
		"restore-target-pre-activation",
		"restore-target-pre-activation-device",
	)
	connectCancel()
	require.Error(
		t, err,
		"restore target accepted WKProto connection before activation\n%s",
		cluster.DumpDiagnostics(),
	)
}

func waitForRestoreStatus(t *testing.T, cluster *suite.StartedCluster, token, want string, timeout time.Duration) restorePlan {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last restoreStatus
	var lastErr error
	for time.Now().Before(deadline) {
		var current restoreStatus
		lastErr = managerRequestError(cluster, token, http.MethodGet, "/manager/restore/status", nil, &current)
		last = current
		if lastErr == nil && current.Plan != nil && current.Plan.Status == want {
			return *current.Plan
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("restore did not reach %s: status=%+v err=%v\n%s", want, last.Plan, lastErr, cluster.DumpDiagnostics())
	return restorePlan{}
}

func managerRequest(t *testing.T, cluster *suite.StartedCluster, token, method, path string, body, out any) {
	t.Helper()
	require.NoError(t, managerRequestError(cluster, token, method, path, body, out), cluster.DumpDiagnostics())
}

func managerRequestError(cluster *suite.StartedCluster, token, method, path string, body, out any) error {
	var lastErr error
	for index := range cluster.Nodes {
		node := &cluster.Nodes[index]
		if node.Process == nil || !node.Process.Running() {
			continue
		}
		if err := managerNodeRequestError(
			node, token, method, path, body, out,
		); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no running Manager node is available")
	}
	return lastErr
}

func managerNodeRequestError(
	node *suite.StartedNode,
	token string,
	method string,
	path string,
	body any,
	out any,
) error {
	if node == nil || node.Process == nil || !node.Process.Running() {
		return fmt.Errorf("Manager node is not running")
	}
	var requestBody []byte
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		requestBody = data
	}
	var reader io.Reader = http.NoBody
	if requestBody != nil {
		reader = bytes.NewReader(requestBody)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(
		ctx, method, "http://"+node.ManagerAddr()+path, reader,
	)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	responseBody, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		return readErr
	}
	if resp.StatusCode/100 != 2 {
		return &suite.HTTPStatusError{
			Method: method, URL: req.URL.String(),
			StatusCode: resp.StatusCode, Body: string(responseBody),
		}
	}
	if out != nil {
		if err := json.Unmarshal(responseBody, out); err != nil {
			return fmt.Errorf(
				"decode %s %s: %w body=%s",
				method, path, err,
				strings.TrimSpace(string(responseBody)),
			)
		}
	}
	return nil
}

func publishCheckpointEventually(
	t *testing.T,
	cluster *suite.StartedCluster,
	token string,
	timeout time.Duration,
) checkpointPublication {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last checkpointPublication
	var lastErr error
	var status backupStatus
	for time.Now().Before(deadline) {
		last = checkpointPublication{}
		lastErr = managerRequestError(
			cluster, token, http.MethodPost, "/manager/backups/checkpoints",
			map[string]any{}, &last,
		)
		if lastErr == nil && last.Checkpoint.ID != "" &&
			last.CatalogHeadToken != "" {
			return last
		}
		_ = managerRequestError(
			cluster, token, http.MethodGet, "/manager/backups/status",
			nil, &status,
		)
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf(
		"checkpoint not published: publication=%+v status=%+v leases=%d err=%v\n%s",
		last, status, len(status.CaptureLeases), lastErr,
		cluster.DumpDiagnostics(),
	)
	return checkpointPublication{}
}

type backupDurableFrontier struct {
	metadata uint64
	messages uint64
	updated  int64
}

func backupDurableFrontiers(
	t *testing.T,
	cluster *suite.StartedCluster,
	token string,
) map[uint16]backupDurableFrontier {
	t.Helper()
	var status backupStatus
	managerRequest(
		t, cluster, token, http.MethodGet, "/manager/backups/status",
		nil, &status,
	)
	result := make(
		map[uint16]backupDurableFrontier, len(status.CaptureLeases),
	)
	for _, lease := range status.CaptureLeases {
		result[lease.HashSlot] = backupDurableFrontier{
			metadata: lease.MetadataSourceWatermark,
			messages: lease.MessageSourceWatermark,
			updated:  lease.FrontierUpdatedUnixMillis,
		}
	}
	require.Len(t, result, 16)
	return result
}

func waitForDurableFrontiers(
	t *testing.T,
	cluster *suite.StartedCluster,
	token string,
	previous map[uint16]backupDurableFrontier,
	metadataHashSlots []uint16,
	messageHashSlots []uint16,
	timeout time.Duration,
) {
	t.Helper()
	requiredMetadata := make(
		map[uint16]struct{}, len(metadataHashSlots),
	)
	for _, hashSlot := range metadataHashSlots {
		requiredMetadata[hashSlot] = struct{}{}
	}
	requiredMessages := make(
		map[uint16]struct{}, len(messageHashSlots),
	)
	for _, hashSlot := range messageHashSlots {
		requiredMessages[hashSlot] = struct{}{}
	}
	deadline := time.Now().Add(timeout)
	var last map[uint16]backupDurableFrontier
	var lastErr error
	for time.Now().Before(deadline) {
		var status backupStatus
		lastErr = managerRequestError(
			cluster, token, http.MethodGet, "/manager/backups/status",
			nil, &status,
		)
		if lastErr == nil {
			last = make(
				map[uint16]backupDurableFrontier,
				len(status.CaptureLeases),
			)
			for _, lease := range status.CaptureLeases {
				last[lease.HashSlot] = backupDurableFrontier{
					metadata: lease.MetadataSourceWatermark,
					messages: lease.MessageSourceWatermark,
					updated:  lease.FrontierUpdatedUnixMillis,
				}
			}
			advanced := true
			for hashSlot := range requiredMetadata {
				if last[hashSlot].metadata <=
					previous[hashSlot].metadata {
					advanced = false
					break
				}
			}
			if advanced {
				for hashSlot := range requiredMessages {
					if last[hashSlot].messages <=
						previous[hashSlot].messages {
						advanced = false
						break
					}
				}
			}
			if advanced {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf(
		"backup capture frontiers did not advance: metadata_slots=%v message_slots=%v previous=%v last=%v err=%v\n%s",
		metadataHashSlots, messageHashSlots, previous, last, lastErr,
		cluster.DumpDiagnostics(),
	)
}
