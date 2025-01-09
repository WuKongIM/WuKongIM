package trace

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/atomic"
)

type dbMetrics struct {
	wklog.Log
	ctx context.Context

	// ========== compact 压缩相关 ==========
	compactTotalCount       atomic.Int64
	compactDefaultCount     atomic.Int64
	compactDeleteOnlyCount  atomic.Int64
	compactElisionOnlyCount atomic.Int64
	compactMoveCount        atomic.Int64
	compactReadCount        atomic.Int64
	compactRewriteCount     atomic.Int64
	compactMultiLevelCount  atomic.Int64
	compactEstimatedDebt    atomic.Int64
	compactInProgressBytes  atomic.Int64
	compactNumInProgress    atomic.Int64
	compactMarkedFiles      atomic.Int64

	// ========== flush 相关 ==========
	flushCount              atomic.Int64
	flushBytes              atomic.Int64
	flushNumInProgress      atomic.Int64
	flushAsIngestCount      atomic.Int64
	flushAsIngestTableCount atomic.Int64
	flushAsIngestBytes      atomic.Int64

	// ========== memtable 内存表相关 ==========
	memTableSize        atomic.Int64
	memTableCount       atomic.Int64
	memTableZombieSize  atomic.Int64
	memTableZombieCount atomic.Int64

	// ========== Snapshots 镜像相关 ==========
	snapshotsCount atomic.Int64

	// ========== TableCache 相关 ==========
	tableCacheSize  atomic.Int64
	tableCacheCount atomic.Int64

	// ========== TableIters 相关 ==========
	tableItersCount atomic.Int64

	// ========== WAL 相关 ==========

	walFilesCount           atomic.Int64
	walSize                 atomic.Int64
	walPhysicalSize         atomic.Int64
	walObsoleteFilesCount   atomic.Int64
	walObsoletePhysicalSize atomic.Int64
	walBytesIn              atomic.Int64
	walBytesWritten         atomic.Int64
	logWriterBytes          atomic.Int64

	diskSpaceUsage atomic.Int64

	// ========== level 相关 ==========
	levelNumFiles        atomic.Int64
	levelFileSize        atomic.Int64
	levelCompactScore    atomic.Int64
	levelBytesIn         atomic.Int64
	levelBytesIngested   atomic.Int64
	levelBytesMoved      atomic.Int64
	levelBytesRead       atomic.Int64
	levelBytesCompacted  atomic.Int64
	levelBytesFlushed    atomic.Int64
	levelTablesCompacted atomic.Int64
	levelTablesFlushed   atomic.Int64
	levelTablesIngested  atomic.Int64
	levelTablesMoved     atomic.Int64

	// ========== message 相关 ==========
	messageAppendBatchCount atomic.Int64

	// ========== 基础 相关 ==========
	setCount         atomic.Int64
	deleteCount      atomic.Int64
	deleteRangeCount atomic.Int64
	commitCount      atomic.Int64

	// ========== 数据操作 ==========
	// 白名单
	addAllowlist       atomic.Int64
	getAllowlist       atomic.Int64
	hasAllowlist       atomic.Int64
	existAllowlist     atomic.Int64
	removeAllowlist    atomic.Int64
	removeAllAllowlist atomic.Int64

	// 分布式配置
	saveChannelClusterConfig        atomic.Int64
	saveChannelClusterConfigs       atomic.Int64
	getChannelClusterConfig         atomic.Int64
	getChannelClusterConfigVersion  atomic.Int64
	getChannelClusterConfigs        atomic.Int64
	searchChannelClusterConfig      atomic.Int64
	getChannelClusterConfigCount    atomic.Int64
	getChannelClusterConfigWithSlot atomic.Int64

	// 频道
	addChannel                atomic.Int64
	updateChannel             atomic.Int64
	getChannel                atomic.Int64
	searchChannels            atomic.Int64
	existChannel              atomic.Int64
	updateChannelAppliedIndex atomic.Int64
	getChannelAppliedIndex    atomic.Int64
	deleteChannel             atomic.Int64

	// 最近会话
	addOrUpdateConversations            atomic.Int64
	addOrUpdateConversationsAddWithUser atomic.Int64
	getConversations                    atomic.Int64
	getConversationsByType              atomic.Int64
	getLastConversations                atomic.Int64
	getConversation                     atomic.Int64
	existConversation                   atomic.Int64
	deleteConversation                  atomic.Int64
	deleteConversations                 atomic.Int64
	searchConversation                  atomic.Int64

	// 黑名单
	addDenylist       atomic.Int64
	getDenylist       atomic.Int64
	existDenylist     atomic.Int64
	removeDenylist    atomic.Int64
	removeAllDenylist atomic.Int64

	// 设备
	getDevice      atomic.Int64
	getDevices     atomic.Int64
	getDeviceCount atomic.Int64
	addDevice      atomic.Int64
	updateDevice   atomic.Int64
	searchDevice   atomic.Int64

	// 消息队列
	appendMessageOfNotifyQueue  atomic.Int64
	getMessagesOfNotifyQueue    atomic.Int64
	removeMessagesOfNotifyQueue atomic.Int64

	// 消息
	appendMessages           atomic.Int64
	appendMessagesBatch      atomic.Int64
	getMessage               atomic.Int64
	loadPrevRangeMsgs        atomic.Int64
	loadNextRangeMsgs        atomic.Int64
	loadMsg                  atomic.Int64
	loadLastMsgs             atomic.Int64
	loadLastMsgsWithEnd      atomic.Int64
	loadNextRangeMsgsForSize atomic.Int64
	truncateLogTo            atomic.Int64
	getChannelLastMessageSeq atomic.Int64
	setChannelLastMessageSeq atomic.Int64
	searchMessages           atomic.Int64

	// 订阅者
	addSubscribers      atomic.Int64
	getSubscribers      atomic.Int64
	removeSubscribers   atomic.Int64
	existSubscriber     atomic.Int64
	removeAllSubscriber atomic.Int64

	// 系统账号
	addSystemUids    atomic.Int64
	removeSystemUids atomic.Int64
	getSystemUids    atomic.Int64

	// 用户
	getUser    atomic.Int64
	existUser  atomic.Int64
	searchUser atomic.Int64
	addUser    atomic.Int64
	updateUser atomic.Int64

	// leader_term_sequence
	setLeaderTermStartIndex                   atomic.Int64
	leaderLastTerm                            atomic.Int64
	leaderTermStartIndex                      atomic.Int64
	leaderLastTermGreaterThan                 atomic.Int64
	deleteLeaderTermStartIndexGreaterThanTerm atomic.Int64
}

func NewDBMetrics() *dbMetrics {
	m := &dbMetrics{
		Log: wklog.NewWKLog("dbMetrics"),
	}

	// ========== compact 压缩相关 ==========
	compactTotalCount := NewInt64ObservableGauge("db_compact_total_count")
	compactDefaultCount := NewInt64ObservableGauge("db_compact_default_count")
	compactDeleteOnlyCount := NewInt64ObservableGauge("db_compact_delete_only_count")
	compactElisionOnlyCount := NewInt64ObservableGauge("db_compact_elision_only_count")
	compactMoveCount := NewInt64ObservableGauge("db_compact_move_count")
	compactReadCount := NewInt64ObservableGauge("db_compact_read_count")
	compactRewriteCount := NewInt64ObservableGauge("db_compact_rewrite_count")
	compactMultiLevelCount := NewInt64ObservableGauge("db_compact_multi_level_count")
	compactEstimatedDebt := NewInt64ObservableGauge("db_compact_estimated_debt")
	compactInProgressBytes := NewInt64ObservableGauge("db_compact_in_progress_bytes")
	compactNumInProgress := NewInt64ObservableGauge("db_compact_num_in_progress")
	compactMarkedFiles := NewInt64ObservableGauge("db_compact_marked_files")

	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(compactTotalCount, m.compactTotalCount.Load())
		obs.ObserveInt64(compactDefaultCount, m.compactDefaultCount.Load())
		obs.ObserveInt64(compactDeleteOnlyCount, m.compactDeleteOnlyCount.Load())
		obs.ObserveInt64(compactElisionOnlyCount, m.compactElisionOnlyCount.Load())
		obs.ObserveInt64(compactMoveCount, m.compactMoveCount.Load())
		obs.ObserveInt64(compactReadCount, m.compactReadCount.Load())
		obs.ObserveInt64(compactRewriteCount, m.compactRewriteCount.Load())
		obs.ObserveInt64(compactMultiLevelCount, m.compactMultiLevelCount.Load())
		obs.ObserveInt64(compactEstimatedDebt, m.compactEstimatedDebt.Load())
		obs.ObserveInt64(compactInProgressBytes, m.compactInProgressBytes.Load())
		obs.ObserveInt64(compactNumInProgress, m.compactNumInProgress.Load())
		obs.ObserveInt64(compactMarkedFiles, m.compactMarkedFiles.Load())
		return nil
	}, compactTotalCount, compactDefaultCount, compactDeleteOnlyCount, compactElisionOnlyCount, compactMoveCount, compactReadCount, compactRewriteCount, compactMultiLevelCount, compactEstimatedDebt, compactInProgressBytes, compactNumInProgress, compactMarkedFiles)

	// ========== flush 相关 ==========
	flushCount := NewInt64ObservableGauge("db_flush_count")
	flushBytes := NewInt64ObservableGauge("db_flush_bytes")
	flushNumInProgress := NewInt64ObservableGauge("db_flush_num_in_progress")
	flushAsIngestCount := NewInt64ObservableGauge("db_flush_as_ingest_count")
	flushAsIngestTableCount := NewInt64ObservableGauge("db_flush_as_ingest_table_count")
	flushAsIngestBytes := NewInt64ObservableGauge("db_flush_as_ingest_bytes")

	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(flushCount, m.flushCount.Load())
		obs.ObserveInt64(flushBytes, m.flushBytes.Load())
		obs.ObserveInt64(flushNumInProgress, m.flushNumInProgress.Load())
		obs.ObserveInt64(flushAsIngestCount, m.flushAsIngestCount.Load())
		obs.ObserveInt64(flushAsIngestTableCount, m.flushAsIngestTableCount.Load())
		obs.ObserveInt64(flushAsIngestBytes, m.flushAsIngestBytes.Load())
		return nil
	}, flushCount, flushBytes, flushNumInProgress, flushAsIngestCount, flushAsIngestTableCount, flushAsIngestBytes)

	// ========== memtable 内存表相关 ==========
	memTableSize := NewInt64ObservableGauge("db_memtable_size")
	memTableCount := NewInt64ObservableGauge("db_memtable_count")
	memTableZombieSize := NewInt64ObservableGauge("db_memtable_zombie_size")
	memTableZombieCount := NewInt64ObservableGauge("db_memtable_zombie_count")

	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(memTableSize, m.memTableSize.Load())
		obs.ObserveInt64(memTableCount, m.memTableCount.Load())
		obs.ObserveInt64(memTableZombieSize, m.memTableZombieSize.Load())
		obs.ObserveInt64(memTableZombieCount, m.memTableZombieCount.Load())
		return nil
	}, memTableSize, memTableCount, memTableZombieSize, memTableZombieCount)

	// ========== Snapshots 镜像相关 ==========
	snapshotsCount := NewInt64ObservableGauge("db_snapshots_count")

	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(snapshotsCount, m.snapshotsCount.Load())
		return nil
	}, snapshotsCount)

	// ========== TableCache 相关 ==========
	tableCacheSize := NewInt64ObservableGauge("db_table_cache_size")
	tableCacheCount := NewInt64ObservableGauge("db_table_cache_count")

	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(tableCacheSize, m.tableCacheSize.Load())
		obs.ObserveInt64(tableCacheCount, m.tableCacheCount.Load())
		return nil
	}, tableCacheSize, tableCacheCount)

	// ========== TableIters 相关 ==========
	tableItersCount := NewInt64ObservableGauge("db_table_iters_count")

	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(tableItersCount, m.tableItersCount.Load())
		return nil
	}, tableItersCount)

	// ========== WAL 相关 ==========
	walFilesCount := NewInt64ObservableGauge("db_wal_files_count")
	walSize := NewInt64ObservableGauge("db_wal_size")
	walPhysicalSize := NewInt64ObservableGauge("db_wal_physical_size")
	walObsoleteFilesCount := NewInt64ObservableGauge("db_wal_obsolete_files_count")
	walObsoletePhysicalSize := NewInt64ObservableGauge("db_wal_obsolete_physical_size")
	walBytesIn := NewInt64ObservableGauge("db_wal_bytes_in")
	walBytesWritten := NewInt64ObservableGauge("db_wal_bytes_written")

	diskSpaceUsage := NewInt64ObservableGauge("db_disk_space_usage")

	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(walFilesCount, m.walFilesCount.Load())
		obs.ObserveInt64(walSize, m.walSize.Load())
		obs.ObserveInt64(walPhysicalSize, m.walPhysicalSize.Load())
		obs.ObserveInt64(walObsoleteFilesCount, m.walObsoleteFilesCount.Load())
		obs.ObserveInt64(walObsoletePhysicalSize, m.walObsoletePhysicalSize.Load())
		obs.ObserveInt64(walBytesIn, m.walBytesIn.Load())
		obs.ObserveInt64(walBytesWritten, m.walBytesWritten.Load())
		obs.ObserveInt64(diskSpaceUsage, m.diskSpaceUsage.Load())
		return nil
	}, walFilesCount, walSize, walPhysicalSize, walObsoleteFilesCount, walObsoletePhysicalSize, walBytesIn, walBytesWritten, diskSpaceUsage)

	// ========== Log Writer 相关 ==========
	logWriterBytes := NewInt64ObservableGauge("db_log_writer_bytes")
	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(logWriterBytes, m.logWriterBytes.Load())
		return nil
	}, logWriterBytes)

	// ========== level 相关 ==========

	levelNumFiles := NewInt64ObservableGauge("db_alllevel_num_files")
	levelFileSize := NewInt64ObservableGauge("db_alllevel_file_size")
	levelCompactScore := NewInt64ObservableGauge("db_alllevel_compact_score")
	levelBytesIn := NewInt64ObservableGauge("db_alllevel_bytes_in")
	levelBytesIngested := NewInt64ObservableGauge("db_alllevel_bytes_ingested")
	levelBytesMoved := NewInt64ObservableGauge("db_alllevel_bytes_moved")
	levelBytesRead := NewInt64ObservableGauge("db_alllevel_bytes_read")
	levelBytesCompacted := NewInt64ObservableGauge("db_alllevel_bytes_compacted")
	levelBytesFlushed := NewInt64ObservableGauge("db_alllevel_bytes_flushed")
	levelTablesCompacted := NewInt64ObservableGauge("db_alllevel_tables_compacted")
	levelTablesFlushed := NewInt64ObservableGauge("db_alllevel_tables_flushed")
	levelTablesIngested := NewInt64ObservableGauge("db_alllevel_tables_ingested")
	levelTablesMoved := NewInt64ObservableGauge("db_alllevel_tables_moved")

	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(levelNumFiles, m.levelNumFiles.Load())
		obs.ObserveInt64(levelFileSize, m.levelFileSize.Load())
		obs.ObserveInt64(levelCompactScore, m.levelCompactScore.Load())
		obs.ObserveInt64(levelBytesIn, m.levelBytesIn.Load())
		obs.ObserveInt64(levelBytesIngested, m.levelBytesIngested.Load())
		obs.ObserveInt64(levelBytesMoved, m.levelBytesMoved.Load())
		obs.ObserveInt64(levelBytesRead, m.levelBytesRead.Load())
		obs.ObserveInt64(levelBytesCompacted, m.levelBytesCompacted.Load())
		obs.ObserveInt64(levelBytesFlushed, m.levelBytesFlushed.Load())
		obs.ObserveInt64(levelTablesCompacted, m.levelTablesCompacted.Load())
		obs.ObserveInt64(levelTablesFlushed, m.levelTablesFlushed.Load())
		obs.ObserveInt64(levelTablesIngested, m.levelTablesIngested.Load())
		obs.ObserveInt64(levelTablesMoved, m.levelTablesMoved.Load())
		return nil
	}, levelNumFiles, levelFileSize,
		levelCompactScore, levelBytesIn,
		levelBytesIngested, levelBytesMoved,
		levelBytesRead, levelBytesCompacted,
		levelBytesFlushed, levelTablesCompacted,
		levelTablesFlushed, levelTablesIngested, levelTablesMoved,
	)

	// ========== message 相关 ==========

	messageAppendBatchCount := NewInt64ObservableCounter("db_message_append_batch_count")
	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(messageAppendBatchCount, m.messageAppendBatchCount.Load())
		return nil
	}, messageAppendBatchCount)

	// ========== 基础 相关 ==========
	setCount := NewInt64ObservableCounter("db_set_count")
	deleteCount := NewInt64ObservableCounter("db_delete_count")
	deleteRangeCount := NewInt64ObservableCounter("db_deleterange_count")
	commitCount := NewInt64ObservableCounter("db_commit_count")
	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(setCount, m.setCount.Load())
		obs.ObserveInt64(deleteCount, m.deleteCount.Load())
		obs.ObserveInt64(deleteRangeCount, m.deleteRangeCount.Load())
		obs.ObserveInt64(commitCount, m.commitCount.Load())
		return nil
	}, setCount, deleteCount, deleteRangeCount, commitCount)

	// ========== 数据操作 ==========
	// 白名单
	addAllowlist := NewInt64ObservableCounter("db_add_allowlist_count")
	getAllowlist := NewInt64ObservableCounter("db_get_allowlist_count")
	hasAllowlist := NewInt64ObservableCounter("db_has_allowlist_count")
	existAllowlist := NewInt64ObservableCounter("db_exist_allowlist_count")
	removeAllowlist := NewInt64ObservableCounter("db_remove_allowlist_count")
	removeAllAllowlist := NewInt64ObservableCounter("db_remove_all_allowlist_count")

	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(addAllowlist, m.addAllowlist.Load())
		obs.ObserveInt64(getAllowlist, m.getAllowlist.Load())
		obs.ObserveInt64(hasAllowlist, m.hasAllowlist.Load())
		obs.ObserveInt64(existAllowlist, m.existAllowlist.Load())
		obs.ObserveInt64(removeAllowlist, m.removeAllowlist.Load())
		obs.ObserveInt64(removeAllAllowlist, m.removeAllAllowlist.Load())

		return nil
	}, addAllowlist, getAllowlist, hasAllowlist, existAllowlist, removeAllowlist, removeAllAllowlist)

	// 分布式
	saveChannelClusterConfig := NewInt64ObservableCounter("db_save_channel_cluster_config_count")
	saveChannelClusterConfigs := NewInt64ObservableCounter("db_save_channel_cluster_configs_count")
	getChannelClusterConfig := NewInt64ObservableCounter("db_get_channel_cluster_config_count")
	getChannelClusterConfigVersion := NewInt64ObservableCounter("db_get_channel_cluster_config_version_count")
	getChannelClusterConfigs := NewInt64ObservableCounter("db_get_channel_cluster_configs_count")
	searchChannelClusterConfig := NewInt64ObservableCounter("db_search_channel_cluster_configs_count")
	getChannelClusterConfigCount := NewInt64ObservableCounter("db_get_channel_cluster_config_count_count")
	getChannelClusterConfigWithSlot := NewInt64ObservableCounter("db_get_channel_cluster_config_with_slot_count")

	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(saveChannelClusterConfig, m.saveChannelClusterConfig.Load())
		obs.ObserveInt64(getChannelClusterConfig, m.getChannelClusterConfig.Load())
		obs.ObserveInt64(getChannelClusterConfigVersion, m.getChannelClusterConfigVersion.Load())
		obs.ObserveInt64(getChannelClusterConfigs, m.getChannelClusterConfigs.Load())
		obs.ObserveInt64(searchChannelClusterConfig, m.searchChannelClusterConfig.Load())
		obs.ObserveInt64(getChannelClusterConfigCount, m.getChannelClusterConfigCount.Load())
		obs.ObserveInt64(getChannelClusterConfigWithSlot, m.getChannelClusterConfigWithSlot.Load())
		obs.ObserveInt64(saveChannelClusterConfigs, m.saveChannelClusterConfigs.Load())
		return nil
	}, saveChannelClusterConfig, getChannelClusterConfig, getChannelClusterConfigVersion, getChannelClusterConfigs, searchChannelClusterConfig, getChannelClusterConfigCount, getChannelClusterConfigWithSlot, saveChannelClusterConfigs)

	// 频道
	addChannel := NewInt64ObservableCounter("db_add_channel_count")
	updateChannel := NewInt64ObservableCounter("db_update_channel_count")
	getChannel := NewInt64ObservableCounter("db_get_channel_count")
	searchChannels := NewInt64ObservableCounter("db_search_channels_count")
	existChannel := NewInt64ObservableCounter("db_exist_channel_count")
	updateChannelAppliedIndex := NewInt64ObservableCounter("db_update_channel_applied_index_count")
	getChannelAppliedIndex := NewInt64ObservableCounter("db_get_channel_applied_index_count")
	deleteChannel := NewInt64ObservableCounter("db_delete_channel_count")

	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(addChannel, m.addChannel.Load())
		obs.ObserveInt64(updateChannel, m.updateChannel.Load())
		obs.ObserveInt64(getChannel, m.getChannel.Load())
		obs.ObserveInt64(searchChannels, m.searchChannels.Load())
		obs.ObserveInt64(existChannel, m.existChannel.Load())
		obs.ObserveInt64(updateChannelAppliedIndex, m.updateChannelAppliedIndex.Load())
		obs.ObserveInt64(getChannelAppliedIndex, m.getChannelAppliedIndex.Load())
		obs.ObserveInt64(deleteChannel, m.deleteChannel.Load())
		return nil
	}, addChannel, updateChannel, getChannel, searchChannels, existChannel, updateChannelAppliedIndex, getChannelAppliedIndex, deleteChannel)

	// 最近会话
	addOrUpdateConversations := NewInt64ObservableCounter("db_add_or_update_conversations_count")
	addOrUpdateConversationsAddWithUser := NewInt64ObservableCounter("db_add_or_update_conversations_add_with_user_count")
	getConversations := NewInt64ObservableCounter("db_get_conversations_count")
	getConversationsByType := NewInt64ObservableCounter("db_get_conversations_by_type_count")
	getLastConversations := NewInt64ObservableCounter("db_get_last_conversations_count")
	getConversation := NewInt64ObservableCounter("db_get_conversation_count")
	existConversation := NewInt64ObservableCounter("db_exist_conversation_count")
	deleteConversation := NewInt64ObservableCounter("db_delete_conversation_count")
	deleteConversations := NewInt64ObservableCounter("db_delete_conversations_count")
	searchConversation := NewInt64ObservableCounter("db_search_conversation_count")

	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(addOrUpdateConversations, m.addOrUpdateConversations.Load())
		obs.ObserveInt64(getConversations, m.getConversations.Load())
		obs.ObserveInt64(getConversationsByType, m.getConversationsByType.Load())
		obs.ObserveInt64(getLastConversations, m.getLastConversations.Load())
		obs.ObserveInt64(getConversation, m.getConversation.Load())
		obs.ObserveInt64(existConversation, m.existConversation.Load())
		obs.ObserveInt64(deleteConversation, m.deleteConversation.Load())
		obs.ObserveInt64(deleteConversations, m.deleteConversations.Load())
		obs.ObserveInt64(searchConversation, m.searchConversation.Load())
		obs.ObserveInt64(addOrUpdateConversationsAddWithUser, m.addOrUpdateConversationsAddWithUser.Load())
		return nil
	}, addOrUpdateConversations, getConversations, getConversationsByType, getLastConversations, getConversation, existConversation, deleteConversation, deleteConversations, searchConversation, addOrUpdateConversationsAddWithUser)

	// 黑名单
	addDenylist := NewInt64ObservableCounter("db_add_denylist_count")
	getDenylist := NewInt64ObservableCounter("db_get_denylist_count")
	existDenylist := NewInt64ObservableCounter("db_exist_denylist_count")
	removeDenylist := NewInt64ObservableCounter("db_remove_denylist_count")
	removeAllDenylist := NewInt64ObservableCounter("db_remove_all_denylist_count")

	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(addDenylist, m.addDenylist.Load())
		obs.ObserveInt64(getDenylist, m.getDenylist.Load())
		obs.ObserveInt64(existDenylist, m.existDenylist.Load())
		obs.ObserveInt64(removeDenylist, m.removeDenylist.Load())
		obs.ObserveInt64(removeAllDenylist, m.removeAllDenylist.Load())
		return nil
	}, addDenylist, getDenylist, existDenylist, removeDenylist, removeAllDenylist)

	// 设备
	getDevice := NewInt64ObservableCounter("db_get_device_count")
	getDevices := NewInt64ObservableCounter("db_get_devices_count")
	getDeviceCount := NewInt64ObservableCounter("db_get_device_count_count")
	addDevice := NewInt64ObservableCounter("db_add_device_count")
	updateDevice := NewInt64ObservableCounter("db_update_device_count")
	searchDevice := NewInt64ObservableCounter("db_search_devices_count")

	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(getDevice, m.getDevice.Load())
		obs.ObserveInt64(getDevices, m.getDevices.Load())
		obs.ObserveInt64(getDeviceCount, m.getDeviceCount.Load())
		obs.ObserveInt64(addDevice, m.addDevice.Load())
		obs.ObserveInt64(updateDevice, m.updateDevice.Load())
		obs.ObserveInt64(searchDevice, m.searchDevice.Load())
		return nil
	}, getDevice, getDevices, getDeviceCount, addDevice, updateDevice, searchDevice)

	// 消息队列
	appendMessageOfNotifyQueue := NewInt64ObservableCounter("db_append_message_of_notify_queue_count")
	getMessagesOfNotifyQueue := NewInt64ObservableCounter("db_get_messages_of_notify_queue_count")
	removeMessagesOfNotifyQueue := NewInt64ObservableCounter("db_remove_messages_of_notify_queue_count")

	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(appendMessageOfNotifyQueue, m.appendMessageOfNotifyQueue.Load())
		obs.ObserveInt64(getMessagesOfNotifyQueue, m.getMessagesOfNotifyQueue.Load())
		obs.ObserveInt64(removeMessagesOfNotifyQueue, m.removeMessagesOfNotifyQueue.Load())
		return nil
	}, appendMessageOfNotifyQueue, getMessagesOfNotifyQueue, removeMessagesOfNotifyQueue)

	// 消息
	appendMessages := NewInt64ObservableCounter("db_append_messages_count")
	appendMessagesBatch := NewInt64ObservableCounter("db_append_messages_batch_count")
	getMessage := NewInt64ObservableCounter("db_get_message_count")
	loadPrevRangeMsgs := NewInt64ObservableCounter("db_load_prev_range_msgs_count")
	loadNextRangeMsgs := NewInt64ObservableCounter("db_load_next_range_msgs_count")
	loadMsg := NewInt64ObservableCounter("db_load_msg_count")
	loadLastMsgs := NewInt64ObservableCounter("db_load_last_msgs_count")
	loadLastMsgsWithEnd := NewInt64ObservableCounter("db_load_last_msgs_with_end_count")
	loadNextRangeMsgsForSize := NewInt64ObservableCounter("db_load_next_range_msgs_for_size_count")
	truncateLogTo := NewInt64ObservableCounter("db_truncate_log_to_count")
	getChannelLastMessageSeq := NewInt64ObservableCounter("db_get_channel_last_message_seq_count")
	setChannelLastMessageSeq := NewInt64ObservableCounter("db_set_channel_last_message_seq_count")
	searchMessages := NewInt64ObservableCounter("db_search_messages_count")

	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(appendMessages, m.appendMessages.Load())
		obs.ObserveInt64(appendMessagesBatch, m.appendMessagesBatch.Load())
		obs.ObserveInt64(getMessage, m.getMessage.Load())
		obs.ObserveInt64(loadPrevRangeMsgs, m.loadPrevRangeMsgs.Load())
		obs.ObserveInt64(loadNextRangeMsgs, m.loadNextRangeMsgs.Load())
		obs.ObserveInt64(loadMsg, m.loadMsg.Load())
		obs.ObserveInt64(loadLastMsgs, m.loadLastMsgs.Load())
		obs.ObserveInt64(loadLastMsgsWithEnd, m.loadLastMsgsWithEnd.Load())
		obs.ObserveInt64(loadNextRangeMsgsForSize, m.loadNextRangeMsgsForSize.Load())
		obs.ObserveInt64(truncateLogTo, m.truncateLogTo.Load())
		obs.ObserveInt64(getChannelLastMessageSeq, m.getChannelLastMessageSeq.Load())
		obs.ObserveInt64(setChannelLastMessageSeq, m.setChannelLastMessageSeq.Load())
		obs.ObserveInt64(searchMessages, m.searchMessages.Load())
		return nil
	}, appendMessages, appendMessagesBatch, getMessage, loadPrevRangeMsgs, loadNextRangeMsgs, loadMsg, loadLastMsgs, loadLastMsgsWithEnd, loadNextRangeMsgsForSize, truncateLogTo, getChannelLastMessageSeq, setChannelLastMessageSeq, searchMessages)

	// 订阅者
	addSubscribers := NewInt64ObservableCounter("db_add_subscribers_count")
	getSubscribers := NewInt64ObservableCounter("db_get_subscribers_count")
	removeSubscribers := NewInt64ObservableCounter("db_remove_subscribers_count")
	existSubscriber := NewInt64ObservableCounter("db_exist_subscriber_count")
	removeAllSubscriber := NewInt64ObservableCounter("db_remove_all_subscriber_count")

	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(addSubscribers, m.addSubscribers.Load())
		obs.ObserveInt64(getSubscribers, m.getSubscribers.Load())
		obs.ObserveInt64(removeSubscribers, m.removeSubscribers.Load())
		obs.ObserveInt64(existSubscriber, m.existSubscriber.Load())
		obs.ObserveInt64(removeAllSubscriber, m.removeAllSubscriber.Load())
		return nil
	}, addSubscribers, getSubscribers, removeSubscribers, existSubscriber, removeAllSubscriber)

	// 系统账号
	addSystemUids := NewInt64ObservableCounter("db_add_system_uids_count")
	removeSystemUids := NewInt64ObservableCounter("db_remove_system_uids_count")
	getSystemUids := NewInt64ObservableCounter("db_get_system_uids_count")

	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(addSystemUids, m.addSystemUids.Load())
		obs.ObserveInt64(removeSystemUids, m.removeSystemUids.Load())
		obs.ObserveInt64(getSystemUids, m.getSystemUids.Load())
		return nil
	}, addSystemUids, removeSystemUids, getSystemUids)

	// 用户
	getUser := NewInt64ObservableCounter("db_get_user_count")
	existUser := NewInt64ObservableCounter("db_exist_user_count")
	searchUser := NewInt64ObservableCounter("db_search_user_count")
	addUser := NewInt64ObservableCounter("db_add_user_count")
	updateUser := NewInt64ObservableCounter("db_update_user_count")

	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(getUser, m.getUser.Load())
		obs.ObserveInt64(existUser, m.existUser.Load())
		obs.ObserveInt64(searchUser, m.searchUser.Load())
		obs.ObserveInt64(addUser, m.addUser.Load())
		obs.ObserveInt64(updateUser, m.updateUser.Load())
		return nil
	})

	// leader_term_sequence
	setLeaderTermStartIndex := NewInt64ObservableCounter("db_set_leader_term_start_index_count")
	leaderLastTerm := NewInt64ObservableCounter("db_leader_last_term_count")
	leaderTermStartIndex := NewInt64ObservableCounter("db_leader_term_start_index_count")
	leaderLastTermGreaterThan := NewInt64ObservableCounter("db_leader_last_term_greater_than_count")
	deleteLeaderTermStartIndexGreaterThanTerm := NewInt64ObservableCounter("db_delete_leader_term_start_index_greater_than_term_count")

	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(setLeaderTermStartIndex, m.setLeaderTermStartIndex.Load())
		obs.ObserveInt64(leaderLastTerm, m.leaderLastTerm.Load())
		obs.ObserveInt64(leaderTermStartIndex, m.leaderTermStartIndex.Load())
		obs.ObserveInt64(leaderLastTermGreaterThan, m.leaderLastTermGreaterThan.Load())
		obs.ObserveInt64(deleteLeaderTermStartIndexGreaterThanTerm, m.deleteLeaderTermStartIndexGreaterThanTerm.Load())
		return nil
	}, setLeaderTermStartIndex, leaderLastTerm, leaderTermStartIndex, leaderLastTermGreaterThan, deleteLeaderTermStartIndexGreaterThanTerm)

	return m
}

// ========== compact 压缩相关 ==========
func (m *dbMetrics) CompactTotalCountSet(shardId uint32, v int64) {
	m.compactTotalCount.Store(v)
}
func (m *dbMetrics) CompactDefaultCountSet(shardId uint32, v int64) {
	m.compactDefaultCount.Store(v)
}
func (m *dbMetrics) CompactDeleteOnlyCountSet(shardId uint32, v int64) {
	m.compactDeleteOnlyCount.Store(v)
}
func (m *dbMetrics) CompactElisionOnlyCountSet(shardId uint32, v int64) {
	m.compactElisionOnlyCount.Store(v)
}
func (m *dbMetrics) CompactMoveCountSet(shardId uint32, v int64) {
	m.compactMoveCount.Store(v)
}
func (m *dbMetrics) CompactReadCountSet(shardId uint32, v int64) {
	m.compactReadCount.Store(v)
}
func (m *dbMetrics) CompactRewriteCountSet(shardId uint32, v int64) {
	m.compactRewriteCount.Store(v)
}
func (m *dbMetrics) CompactMultiLevelCount(shardId uint32, v int64) {
	m.compactMultiLevelCount.Store(v)
}
func (m *dbMetrics) CompactEstimatedDebtSet(shardId uint32, v int64) {
	m.compactEstimatedDebt.Store(v)
}
func (m *dbMetrics) CompactInProgressBytesSet(shardId uint32, v int64) {
	m.compactInProgressBytes.Store(v)
}
func (m *dbMetrics) CompactNumInProgressSet(shardId uint32, v int64) {
	m.compactNumInProgress.Store(v)
}
func (m *dbMetrics) CompactMarkedFilesSet(shardId uint32, v int64) {
	m.compactMarkedFiles.Store(v)
}

// ========== flush 相关 ==========
func (m *dbMetrics) FlushCountAdd(shardId uint32, v int64) {
	m.flushCount.Store(v)
}
func (m *dbMetrics) FlushBytesAdd(shardId uint32, v int64) {
	m.flushBytes.Store(v)
}
func (m *dbMetrics) FlushNumInProgressAdd(shardId uint32, v int64) {
	m.flushNumInProgress.Store(v)
}
func (m *dbMetrics) FlushAsIngestCountAdd(shardId uint32, v int64) {
	m.flushAsIngestCount.Store(v)
}
func (m *dbMetrics) FlushAsIngestTableCountAdd(shardId uint32, v int64) {
	m.flushAsIngestTableCount.Store(v)
}
func (m *dbMetrics) FlushAsIngestBytesAdd(shardId uint32, v int64) {
	m.flushAsIngestBytes.Store(v)
}

// ========== memtable 内存表相关 ==========
func (m *dbMetrics) MemTableSizeSet(shardId uint32, v int64) {

	m.memTableSize.Store(v)

}
func (m *dbMetrics) MemTableCountSet(shardId uint32, v int64) {

	m.memTableCount.Store(v)
}
func (m *dbMetrics) MemTableZombieSizeSet(shardId uint32, v int64) {

	m.memTableZombieSize.Store(v)

}
func (m *dbMetrics) MemTableZombieCountSet(shardId uint32, v int64) {

	m.memTableZombieCount.Store(v)
}

// ========== Snapshots 镜像相关 ==========
func (m *dbMetrics) SnapshotsCountSet(shardId uint32, v int64) {

	m.snapshotsCount.Store(v)
}

// ========== TableCache 相关 ==========
func (m *dbMetrics) TableCacheSizeSet(shardId uint32, v int64) {
	m.tableCacheSize.Store(v)
}
func (m *dbMetrics) TableCacheCountSet(shardId uint32, v int64) {
	m.tableCacheCount.Store(v)
}

// ========== TableIters 相关 ==========
func (m *dbMetrics) TableItersCountSet(shardId uint32, v int64) {
	m.tableItersCount.Store(v)
}

// ========== WAL 相关 ==========

func (m *dbMetrics) WALFilesCountSet(shardId uint32, v int64) {
	m.walFilesCount.Store(v)
}
func (m *dbMetrics) WALSizeSet(shardId uint32, v int64) {
	m.walFilesCount.Store(v)
}
func (m *dbMetrics) WALPhysicalSizeSet(shardId uint32, v int64) {
	m.walPhysicalSize.Store(v)
}
func (m *dbMetrics) WALObsoleteFilesCountSet(shardId uint32, v int64) {
	m.walObsoleteFilesCount.Store(v)
}
func (m *dbMetrics) WALObsoletePhysicalSizeSet(shardId uint32, v int64) {
	m.walObsoletePhysicalSize.Store(v)
}
func (m *dbMetrics) WALBytesInSet(shardId uint32, v int64) {
	m.walBytesIn.Store(v)
}
func (m *dbMetrics) WALBytesWrittenSet(shardId uint32, v int64) {
	m.walBytesWritten.Store(v)
}

func (m *dbMetrics) DiskSpaceUsageSet(shardId uint32, v int64) {
	m.diskSpaceUsage.Store(v)
}

// ========== Log Writer 相关 ==========
func (m *dbMetrics) LogWriterBytesSet(shardId uint32, v int64) {
	m.logWriterBytes.Store(v)
}

// ========== level 相关 ==========

func (m *dbMetrics) LevelNumFilesSet(shardId uint32, v int64) {
	m.levelNumFiles.Store(v)
}

func (m *dbMetrics) LevelFileSizeSet(shardId uint32, v int64) {
	m.levelFileSize.Store(v)

}
func (m *dbMetrics) LevelCompactScoreSet(shardId uint32, v int64) {
	m.levelCompactScore.Store(v)
}

func (m *dbMetrics) LevelBytesInSet(shardId uint32, v int64) {
	m.levelBytesIn.Store(v)
}
func (m *dbMetrics) LevelBytesIngestedSet(shardId uint32, v int64) {
	m.levelBytesIngested.Store(v)
}
func (m *dbMetrics) LevelBytesMovedSet(shardId uint32, v int64) {
	m.levelBytesMoved.Store(v)
}
func (m *dbMetrics) LevelBytesReadSet(shardId uint32, v int64) {
	m.levelBytesRead.Store(v)
}
func (m *dbMetrics) LevelBytesCompactedSet(shardId uint32, v int64) {
	m.levelBytesCompacted.Store(v)
}
func (m *dbMetrics) LevelBytesFlushedSet(shardId uint32, v int64) {
	m.levelBytesFlushed.Store(v)
}
func (m *dbMetrics) LevelTablesCompactedSet(shardId uint32, v int64) {
	m.levelTablesCompacted.Store(v)
}
func (m *dbMetrics) LevelTablesFlushedSet(shardId uint32, v int64) {
	m.levelTablesFlushed.Store(v)
}
func (m *dbMetrics) LevelTablesIngestedSet(shardId uint32, v int64) {
	m.levelTablesIngested.Store(v)
}
func (m *dbMetrics) LevelTablesMovedSet(shardId uint32, v int64) {
	m.levelTablesMoved.Store(v)
}

// ========== message 相关 ==========
func (m *dbMetrics) MessageAppendBatchCountAdd(v int64) {
	m.messageAppendBatchCount.Add(v)
}

// ========== 基础 相关 ==========

func (m *dbMetrics) SetAdd(v int64) {

	m.setCount.Add(v)

}
func (m *dbMetrics) DeleteAdd(v int64) {
	m.deleteCount.Add(1)
}
func (m *dbMetrics) DeleteRangeAdd(v int64) {
	m.deleteRangeCount.Add(v)
}

func (m *dbMetrics) CommitAdd(v int64) {
	m.commitCount.Add(v)
}

// ========== 数据操作 ==========
// 白名单
func (m *dbMetrics) AddAllowlistAdd(v int64) {
	m.addAllowlist.Add(v)
}
func (m *dbMetrics) GetAllowlistAdd(v int64) {
	m.getAllowlist.Add(v)
}
func (m *dbMetrics) HasAllowlistAdd(v int64) {
	m.hasAllowlist.Add(v)
}
func (m *dbMetrics) ExistAllowlistAdd(v int64) {
	m.existAllowlist.Add(v)
}
func (m *dbMetrics) RemoveAllowlistAdd(v int64) {
	m.removeAllowlist.Add(v)
}
func (m *dbMetrics) RemoveAllAllowlistAdd(v int64) {
	m.removeAllAllowlist.Add(v)
}

// 分布式配置
func (m *dbMetrics) SaveChannelClusterConfigAdd(v int64) {
	m.saveChannelClusterConfig.Add(v)
}
func (m *dbMetrics) SaveChannelClusterConfigsAdd(v int64) {
	m.saveChannelClusterConfigs.Add(v)
}
func (m *dbMetrics) GetChannelClusterConfigAdd(v int64) {
	m.getChannelClusterConfig.Add(v)
}
func (m *dbMetrics) GetChannelClusterConfigVersionAdd(v int64) {
	m.getChannelClusterConfigVersion.Add(v)
}
func (m *dbMetrics) GetChannelClusterConfigsAdd(v int64) {
	m.getChannelClusterConfigs.Add(v)
}
func (m *dbMetrics) SearchChannelClusterConfigAdd(v int64) {
	m.searchChannelClusterConfig.Add(v)
}
func (m *dbMetrics) GetChannelClusterConfigCountWithSlotIdAdd(v int64) {
	m.getChannelClusterConfigCount.Add(v)
}
func (m *dbMetrics) GetChannelClusterConfigWithSlotIdAdd(v int64) {
	m.getChannelClusterConfigWithSlot.Add(v)
}

// 频道
func (m *dbMetrics) AddChannelAdd(v int64) {
	m.addChannel.Add(v)
}
func (m *dbMetrics) UpdateChannelAdd(v int64) {
	m.updateChannel.Add(v)
}
func (m *dbMetrics) GetChannelAdd(v int64) {
	m.getChannel.Add(v)
}
func (m *dbMetrics) SearchChannelsAdd(v int64) {
	m.searchChannels.Add(v)
}
func (m *dbMetrics) ExistChannelAdd(v int64) {
	m.existChannel.Add(v)
}
func (m *dbMetrics) UpdateChannelAppliedIndexAdd(v int64) {
	m.updateChannelAppliedIndex.Add(v)
}
func (m *dbMetrics) GetChannelAppliedIndexAdd(v int64) {
	m.getChannelAppliedIndex.Add(v)
}

func (m *dbMetrics) DeleteChannelAdd(v int64) {
	m.deleteChannel.Add(v)
}

// 最近会话
func (m *dbMetrics) AddOrUpdateConversationsAdd(v int64) {
	m.addOrUpdateConversations.Add(v)
}

func (m *dbMetrics) AddOrUpdateConversationsAddWithUser(v int64) {
	m.addOrUpdateConversationsAddWithUser.Add(v)
}
func (m *dbMetrics) GetConversationsAdd(v int64) {
	m.getConversations.Add(v)
}
func (m *dbMetrics) GetConversationsByTypeAdd(v int64) {
	m.getConversationsByType.Add(v)
}
func (m *dbMetrics) GetLastConversationsAdd(v int64) {
	m.getLastConversations.Add(v)
}
func (m *dbMetrics) GetConversationAdd(v int64) {
	m.getConversation.Add(v)
}
func (m *dbMetrics) ExistConversationAdd(v int64) {
	m.existConversation.Add(v)
}
func (m *dbMetrics) DeleteConversationAdd(v int64) {
	m.deleteConversation.Add(v)
}
func (m *dbMetrics) DeleteConversationsAdd(v int64) {
	m.deleteConversations.Add(v)
}
func (m *dbMetrics) SearchConversationAdd(v int64) {
	m.searchConversation.Add(v)
}

// 黑名单

func (m *dbMetrics) AddDenylistAdd(v int64) {
	m.addDenylist.Add(v)
}
func (m *dbMetrics) GetDenylistAdd(v int64) {
	m.getDenylist.Add(v)
}
func (m *dbMetrics) ExistDenylistAdd(v int64) {
	m.existDenylist.Add(v)
}
func (m *dbMetrics) RemoveDenylistAdd(v int64) {
	m.removeDenylist.Add(v)

}
func (m *dbMetrics) RemoveAllDenylistAdd(v int64) {
	m.removeAllDenylist.Add(v)
}

// 设备
func (m *dbMetrics) GetDeviceAdd(v int64) {
	m.getDevice.Add(v)
}
func (m *dbMetrics) GetDevicesAdd(v int64) {
	m.getDevices.Add(v)
}
func (m *dbMetrics) GetDeviceCountAdd(v int64) {
	m.getDeviceCount.Add(v)
}
func (m *dbMetrics) AddDeviceAdd(v int64) {
	m.addDevice.Add(v)
}
func (m *dbMetrics) UpdateDeviceAdd(v int64) {
	m.updateDevice.Add(v)
}
func (m *dbMetrics) SearchDeviceAdd(v int64) {
	m.searchDevice.Add(v)
}

// 消息队列
func (m *dbMetrics) AppendMessageOfNotifyQueueAdd(v int64) {
	m.appendMessageOfNotifyQueue.Add(v)
}
func (m *dbMetrics) GetMessagesOfNotifyQueueAdd(v int64) {
	m.getMessagesOfNotifyQueue.Add(v)
}
func (m *dbMetrics) RemoveMessagesOfNotifyQueueAdd(v int64) {
	m.removeMessagesOfNotifyQueue.Add(v)
}

// 消息
func (m *dbMetrics) AppendMessagesAdd(v int64) {
	m.appendMessages.Add(v)
}
func (m *dbMetrics) AppendMessagesBatchAdd(v int64) {
	m.appendMessagesBatch.Add(v)
}
func (m *dbMetrics) GetMessageAdd(v int64) {
	m.getMessage.Add(v)
}
func (m *dbMetrics) LoadPrevRangeMsgsAdd(v int64) {
	m.loadPrevRangeMsgs.Add(v)
}
func (m *dbMetrics) LoadNextRangeMsgsAdd(v int64) {
	m.loadNextRangeMsgs.Add(v)
}
func (m *dbMetrics) LoadMsgAdd(v int64) {
	m.loadMsg.Add(v)
}
func (m *dbMetrics) LoadLastMsgsAdd(v int64) {
	m.loadLastMsgs.Add(v)
}
func (m *dbMetrics) LoadLastMsgsWithEndAdd(v int64) {
	m.loadLastMsgsWithEnd.Add(v)
}
func (m *dbMetrics) LoadNextRangeMsgsForSizeAdd(v int64) {
	m.loadNextRangeMsgsForSize.Add(v)
}
func (m *dbMetrics) TruncateLogToAdd(v int64) {
	m.truncateLogTo.Add(v)
}
func (m *dbMetrics) GetChannelLastMessageSeqAdd(v int64) {
	m.getChannelLastMessageSeq.Add(v)
}
func (m *dbMetrics) SetChannelLastMessageSeqAdd(v int64) {
	m.setChannelLastMessageSeq.Add(v)
}
func (m *dbMetrics) SearchMessagesAdd(v int64) {
	m.searchMessages.Add(v)
}

// 订阅者
func (m *dbMetrics) AddSubscribersAdd(v int64) {
	m.addSubscribers.Add(v)
}
func (m *dbMetrics) GetSubscribersAdd(v int64) {
	m.getSubscribers.Add(v)
}
func (m *dbMetrics) RemoveSubscribersAdd(v int64) {
	m.removeSubscribers.Add(v)
}
func (m *dbMetrics) ExistSubscriberAdd(v int64) {
	m.existSubscriber.Add(v)
}
func (m *dbMetrics) RemoveAllSubscriberAdd(v int64) {
	m.removeAllSubscriber.Add(v)
}

// 系统账号
func (m *dbMetrics) AddSystemUidsAdd(v int64) {
	m.addSystemUids.Add(v)
}
func (m *dbMetrics) RemoveSystemUidsAdd(v int64) {
	m.removeSystemUids.Add(v)
}
func (m *dbMetrics) GetSystemUidsAdd(v int64) {
	m.getSystemUids.Add(v)
}

// 用户
func (m *dbMetrics) GetUserAdd(v int64) {
	m.getUser.Add(v)
}
func (m *dbMetrics) ExistUserAdd(v int64) {
	m.existUser.Add(v)
}
func (m *dbMetrics) SearchUserAdd(v int64) {
	m.searchUser.Add(v)
}
func (m *dbMetrics) AddUserAdd(v int64) {
	m.addUser.Add(v)
}
func (m *dbMetrics) UpdateUserAdd(v int64) {
	m.updateUser.Add(v)
}

// leader_term_sequence
func (m *dbMetrics) SetLeaderTermStartIndexAdd(v int64) {
	m.setLeaderTermStartIndex.Add(v)
}
func (m *dbMetrics) LeaderLastTermAdd(v int64) {
	m.leaderLastTerm.Add(v)
}
func (m *dbMetrics) LeaderTermStartIndexAdd(v int64) {
	m.leaderTermStartIndex.Add(v)
}
func (m *dbMetrics) LeaderLastTermGreaterThanAdd(v int64) {
	m.leaderLastTermGreaterThan.Add(v)
}
func (m *dbMetrics) DeleteLeaderTermStartIndexGreaterThanTermAdd(v int64) {
	m.deleteLeaderTermStartIndexGreaterThanTerm.Add(v)
}
