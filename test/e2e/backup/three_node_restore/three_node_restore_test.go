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
	"path/filepath"
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
	Checkpoint     checkpoint                          `json:"checkpoint"`
	ManifestSHA256 string                              `json:"manifest_sha256"`
	CatalogHead    backupartifact.CatalogPageReference `json:"catalog_head"`
}

type restorePlan struct {
	ID                                  string                                    `json:"id"`
	RestorePointID                      string                                    `json:"restore_point_id"`
	ManifestSHA256                      string                                    `json:"manifest_sha256"`
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

func TestThreeNodeBackupIncrementalRestoresAndContinuesTraffic(t *testing.T) {
	repositoryRoot := t.TempDir()
	options := []suite.Option{suite.WithManagerHTTP()}
	for nodeID := uint64(1); nodeID <= 3; nodeID++ {
		options = append(options,
			suite.WithNodeConfigOverrides(nodeID, sourceBackupConfig(repositoryRoot, nodeID)),
			suite.WithNodeEnv(nodeID,
				"WUKONGIM_BACKUP_E2E_FILE_ROOT="+repositoryRoot,
				"AWS_ACCESS_KEY_ID=e2e",
				"AWS_SECRET_ACCESS_KEY=e2e-secret",
				"AWS_EC2_METADATA_DISABLED=true",
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
	require.Len(t, baseline.ManifestSHA256, 64)
	baselineFrontiers := backupDurableFrontiers(
		t, cluster, sourceToken,
	)

	const firstPayload = "message captured by incremental backup"
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
	waitForConversationDurable(t, cluster, 10*time.Second)
	personalChannelID := channelid.EncodePersonChannel(
		"backup-e2e-sender", "backup-e2e-recipient",
	)
	waitForDurableFrontiers(
		t, cluster, sourceToken, baselineFrontiers,
		[]uint16{
			hashslot.HashSlotForKey("backup-e2e-sender", 16),
			hashslot.HashSlotForKey(personalChannelID, 16),
			hashslot.HashSlotForKey(erasureChannelID, 16),
		},
		[]uint16{
			hashslot.HashSlotForKey(personalChannelID, 16),
			hashslot.HashSlotForKey(erasureChannelID, 16),
		},
		20*time.Second,
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

	target := startRestoreCluster(t, repositoryRoot)
	token := loginManager(
		t, target, "restore-admin", "restore-secret",
	)
	plan := createRestorePlan(t, target, token, delta)
	require.Equal(t, delta.Checkpoint.ID, plan.RestorePointID)
	require.Equal(t, backupartifact.ErasureLedgerSnapshotVersion, plan.ErasureLedgerVersion)
	require.Equal(t, uint64(1), plan.ErasureEventCount)
	require.Len(t, plan.ErasureLedgerSHA256, 64)
	require.Equal(t, "wukongim-e2e-three", plan.SourceClusterID)
	require.Equal(t, "source-generation", plan.SourceGeneration)
	require.Equal(t, "wukongim-e2e-restore", plan.TargetClusterID)
	require.Equal(t, "target-generation", plan.TargetGeneration)
	require.Equal(t, uint16(16), plan.HashSlotCount)

	plan = startRestore(t, target, token, plan.ID)
	require.Equal(t, "installing", plan.Status)
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
		t, "target-generation", restartedPlan.TargetGeneration,
	)
	require.Equal(
		t, "wukongim-e2e-restore", restartedPlan.TargetClusterID,
	)
	suite.RequireConversationEventuallyWithin(t, *target.MustNode(1), "backup-e2e-sender", "backup-e2e-recipient", 10*time.Second, func(item suite.ConversationListItem) error {
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

func sourceBackupConfig(root string, nodeID uint64) map[string]string {
	return map[string]string{
		"WK_BACKUP_ENABLED":                    "true",
		"WK_BACKUP_REPOSITORY_ID":              "e2e-repository",
		"WK_BACKUP_SOURCE_GENERATION":          "source-generation",
		"WK_BACKUP_STAGING_DIR":                filepath.Join(root, fmt.Sprintf("source-staging-%d", nodeID)),
		"WK_BACKUP_KMS_KEY_ID":                 "e2e-encryption-key",
		"WK_BACKUP_SIGNING_KEY_ID":             "e2e-signing-key",
		"WK_BACKUP_KMS_REGION":                 "e2e-kms",
		"WK_BACKUP_KMS_ENDPOINT":               "https://kms.e2e.invalid",
		"WK_BACKUP_CAPTURE_RECONCILE_INTERVAL": "200ms",
		"WK_BACKUP_CHECKPOINT_INTERVAL":        "1h",
		"WK_BACKUP_BASELINE_CHUNK_BYTES":       "1048576",
		"WK_BACKUP_TARGET_SEGMENT_BYTES":       "1048576",
		"WK_BACKUP_MAX_SEGMENT_OPEN_DURATION":  "500ms",
		"WK_BACKUP_STAGING_MAX_BYTES":          "67108864",
		"WK_BACKUP_WORKER_COUNT":               "2",
		"WK_BACKUP_OBJECT_LOCK_DAYS":           "7",
		"WK_BACKUP_PRIMARY_ENDPOINT":           "https://primary.e2e.invalid",
		"WK_BACKUP_PRIMARY_REGION":             "e2e-primary",
		"WK_BACKUP_PRIMARY_BUCKET":             "primary",
		"WK_BACKUP_PRIMARY_PREFIX":             "cluster",
		"WK_BACKUP_SECONDARY_ENDPOINT":         "https://secondary.e2e.invalid",
		"WK_BACKUP_SECONDARY_REGION":           "e2e-secondary",
		"WK_BACKUP_SECONDARY_BUCKET":           "secondary",
		"WK_BACKUP_SECONDARY_PREFIX":           "cluster",
		"WK_MANAGER_AUTH_ON":                   "true",
		"WK_MANAGER_JWT_SECRET":                "e2e-source-jwt-secret",
		"WK_MANAGER_JWT_ISSUER":                "wukongim-e2e",
		"WK_MANAGER_JWT_EXPIRE":                "1h",
		"WK_MANAGER_USERS":                     `[{"username":"source-admin","password":"source-secret","permissions":[{"resource":"*","actions":["*"]},{"resource":"cluster.backup.source_fence","actions":["w"]}]}]`,
	}
}

func targetRestoreConfig(root string, nodeID uint64) map[string]string {
	return map[string]string{
		"WK_CLUSTER_ID":                  "wukongim-e2e-restore",
		"WK_BACKUP_RESTORE_MODE":         "true",
		"WK_BACKUP_REPOSITORY_ID":        "e2e-repository",
		"WK_BACKUP_TARGET_GENERATION":    "target-generation",
		"WK_BACKUP_STAGING_DIR":          filepath.Join(root, fmt.Sprintf("target-staging-%d", nodeID)),
		"WK_BACKUP_KMS_KEY_ID":           "e2e-encryption-key",
		"WK_BACKUP_SIGNING_KEY_ID":       "e2e-signing-key",
		"WK_BACKUP_KMS_REGION":           "e2e-kms",
		"WK_BACKUP_KMS_ENDPOINT":         "https://kms.e2e.invalid",
		"WK_BACKUP_BASELINE_CHUNK_BYTES": "1048576",
		"WK_BACKUP_STAGING_MAX_BYTES":    "67108864",
		"WK_BACKUP_WORKER_COUNT":         "2",
		"WK_BACKUP_PRIMARY_ENDPOINT":     "https://primary.e2e.invalid",
		"WK_BACKUP_PRIMARY_REGION":       "e2e-primary",
		"WK_BACKUP_PRIMARY_BUCKET":       "primary",
		"WK_BACKUP_PRIMARY_PREFIX":       "cluster",
		"WK_BACKUP_SECONDARY_ENDPOINT":   "https://secondary.e2e.invalid",
		"WK_BACKUP_SECONDARY_REGION":     "e2e-secondary",
		"WK_BACKUP_SECONDARY_BUCKET":     "secondary",
		"WK_BACKUP_SECONDARY_PREFIX":     "cluster",
		"WK_MANAGER_AUTH_ON":             "true",
		"WK_MANAGER_JWT_SECRET":          "e2e-restore-jwt-secret",
		"WK_MANAGER_JWT_ISSUER":          "wukongim-e2e",
		"WK_MANAGER_JWT_EXPIRE":          "1h",
		"WK_MANAGER_USERS":               `[{"username":"restore-admin","password":"restore-secret","permissions":[{"resource":"cluster.backup","actions":["r","w"]},{"resource":"cluster.restore.activation","actions":["w"]},{"resource":"cluster.channel","actions":["r"]}]}]`,
	}
}

func stopCluster(t *testing.T, cluster *suite.StartedCluster) {
	t.Helper()
	for index := len(cluster.Nodes) - 1; index >= 0; index-- {
		require.NoError(t, cluster.Nodes[index].Stop(), cluster.DumpDiagnostics())
	}
}

func waitForConversationDurable(t *testing.T, cluster *suite.StartedCluster, timeout time.Duration) {
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
		if persisted > 0 && allClean {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("conversation active rows were not durably flushed: persisted=%v dirty=%v err=%v\n%s", lastPersisted, lastDirty, lastErr, cluster.DumpDiagnostics())
}

func startRestoreCluster(t *testing.T, repositoryRoot string) *suite.StartedCluster {
	t.Helper()
	options := []suite.Option{suite.WithManagerHTTP()}
	for nodeID := uint64(1); nodeID <= 3; nodeID++ {
		options = append(options,
			suite.WithNodeConfigOverrides(nodeID, targetRestoreConfig(repositoryRoot, nodeID)),
			suite.WithNodeEnv(nodeID,
				"WUKONGIM_BACKUP_E2E_FILE_ROOT="+repositoryRoot,
				"AWS_ACCESS_KEY_ID=e2e",
				"AWS_SECRET_ACCESS_KEY=e2e-secret",
				"AWS_EC2_METADATA_DISABLED=true",
			),
		)
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
		"restore_point_id":  publication.Checkpoint.ID,
		"catalog_head":      publication.CatalogHead,
		"repository":        "primary",
		"invalidate_tokens": false,
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
	var receipt backupartifact.SourceFenceReceipt
	managerRequest(
		t, cluster, token, http.MethodPost,
		"/manager/backups/source-fence",
		map[string]any{
			"restore_plan_id":   plan.ID,
			"restore_point_id":  plan.RestorePointID,
			"target_cluster_id": plan.TargetClusterID,
			"target_generation": plan.TargetGeneration,
		},
		&receipt,
	)
	require.Equal(t, plan.ID, receipt.RestorePlanID)
	require.Equal(t, plan.ManifestSHA256, receipt.ManifestSHA256)
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
	var reader io.Reader = http.NoBody
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, "http://"+cluster.MustNode(1).ManagerAddr()+path, reader)
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
	defer func() { _ = resp.Body.Close() }()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return &suite.HTTPStatusError{Method: method, URL: req.URL.String(), StatusCode: resp.StatusCode, Body: string(responseBody)}
	}
	if out != nil {
		if err := json.Unmarshal(responseBody, out); err != nil {
			return fmt.Errorf("decode %s %s: %w body=%s", method, path, err, strings.TrimSpace(string(responseBody)))
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
			last.CatalogHead.LatestCheckpointID == last.Checkpoint.ID {
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
