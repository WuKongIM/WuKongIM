# Runtime configuration comparison

This is a static comparison of actual local TOML plus its executed launcher, the retained 79a cloud bundle service template/intent, and frozen source normalization. Cloud effective values are source-derived unless retained metrics independently confirm them. Address/identity/credential/path values are omitted by category. It is not a claim of runtime equivalence.

| Field | Local full-v2 | Frozen cloud template + defaults | Status |
| --- | --- | --- | --- |
| delivery.owner_push_batch_routes | 512 | 512 | same_fixed_default |
| delivery.owner_push_concurrency_per_plan | 4 | 4 | same_fixed_default |
| delivery.pending_ack_max_per_session | 1024 | 1024 | same_fixed_default |
| delivery.pending_ack_ttl_ms | 30000 | 30000 | same_fixed_default |
| delivery.plan_recipients | 512 | 512 | same_fixed_default |
| delivery.plan_timeout_ms | 5000 | 5000 | same_fixed_default |
| delivery.queue_plans | 1024 | 1024 | same_fixed_default |
| delivery.recipient_workers | 320 | 320 | same_fixed_default |
| delivery.retry_initial_ms | 10 | 10 | same_fixed_default |
| delivery.retry_max_ms | 10 | 10 | same_fixed_default |
| delivery.retry_total_attempts | 3 | 3 | same_fixed_default |
| person_directory.attempt_timeout_ms | 4000 | 4000 | same_fixed_default |
| person_directory.page_size | 64 | 64 | same_fixed_default |
| person_directory.queue_tasks | 512 | 512 | same_fixed_default |
| person_directory.repair_interval_ms | 1000 | 1000 | same_fixed_default |
| person_directory.workers | 8 | 8 | same_fixed_default |
| channel_append.advance_pool_size | 500 | 500 | same_fixed_default |
| channel_append.authority_shards_formula | 4 | unknown; 4 if effective GOMAXPROCS<=4 | cloud_runtime_unmeasured |
| channel_append.effect_pool_size | 2000 | 2000 | same_fixed_default |
| channel_append.reactor_append_batch_bytes | 262144 | 262144 | same_fixed_default |
| channel_append.reactor_append_batch_records | 128 | 128 | same_fixed_default |
| channel_append.reactor_append_batch_wait_us | 1000 | 1000 | same_fixed_default |
| channel_append.reactor_append_queue_bytes | 4194304 | 4194304 | same_fixed_default |
| channel_append.reactor_append_queue_requests | 1024 | 1024 | same_fixed_default |
| channel_append.reactor_append_store_retry_backoff_us | 1000 | 1000 | same_fixed_default |
| channel_append.reactor_count_formula | 4 | unknown; 4 if effective GOMAXPROCS<=4 | cloud_runtime_unmeasured |
| channel_append.reactor_mailbox_items | 1024 | 1024 | same_fixed_default |
| channel_append.reactor_tick_ms | 50 | 50 | same_fixed_default |
| channel_append.recipient_authority_dispatch_concurrency | 100 | 100 | same_fixed_default |
| channel_blocking_workers.rpc_batch_items | 8 | 8 | same_fixed_default |
| channel_blocking_workers.rpc_batch_wait_us | 250 | 250 | same_fixed_default |
| channel_blocking_workers.rpc_workers | 96 | 96 | same_fixed_default |
| channel_blocking_workers.store_append_batch_items | 64 | 64 | same_fixed_default |
| channel_blocking_workers.store_append_batch_wait_us | 250 | 250 | same_fixed_default |
| channel_blocking_workers.store_append_workers | 128 | 128 | same_fixed_default |
| channel_blocking_workers.store_apply_batch_items | 64 | 64 | same_fixed_default |
| channel_blocking_workers.store_apply_batch_wait_us | 250 | 250 | same_fixed_default |
| channel_blocking_workers.store_apply_workers | 8 | 8 | same_fixed_default |
| channel_blocking_workers.store_read_workers_formula | 4 | unknown; 4 if effective GOMAXPROCS<=4 | cloud_runtime_unmeasured |
| channel_blocking_workers.worker_queue_items_from_default_mailbox | 1024 | 1024 | same_fixed_default |
| cluster_transport_rpc.dial_failure_cooldown_ms | 50 | 50 | same_fixed_default |
| cluster_transport_rpc.dial_timeout_ms | 5000 | 5000 | same_fixed_default |
| cluster_transport_rpc.foreground_channel_mutation_concurrency | 512 | 512 | same_fixed_default |
| cluster_transport_rpc.frame_body_max_bytes | 67108864 | 67108864 | same_fixed_default |
| cluster_transport_rpc.ordered_raft_service_concurrency | 1 | 1 | same_fixed_default |
| cluster_transport_rpc.peer_connection_pool | 16 | 16 | same_fixed_default |
| cluster_transport_rpc.per_service_queue_bytes | 67108864 | 67108864 | same_fixed_default |
| cluster_transport_rpc.per_service_queue_items | 4096 | 4096 | same_fixed_default |
| cluster_transport_rpc.queued_bytes_per_connection | 67108864 | 67108864 | same_fixed_default |
| cluster_transport_rpc.queued_items_per_connection | 4096 | 4096 | same_fixed_default |
| cluster_transport_rpc.service_default_concurrency | 128 | 128 | same_fixed_default |
| cluster_transport_rpc.service_default_execution_timeout_ms | 30000 | 30000 | same_fixed_default |
| cluster_transport_rpc.service_queue_timeout_ms | 5000 | 5000 | same_fixed_default |
| cluster_transport_rpc.write_batch_max_bytes | 1048576 | 1048576 | same_fixed_default |
| cluster_transport_rpc.write_batch_max_frames | 64 | 64 | same_fixed_default |
| cluster_transport_rpc.write_batch_wait_ms | 0 | 0 | same_fixed_default |
| cluster_transport_rpc.write_timeout_ms | 5000 | 5000 | same_fixed_default |
| physical_commit.flush_window_us | 500 | 500 | same_fixed_default |
| physical_commit.max_bytes_zero_means_no_positive_limit | 0 | 0 | same_fixed_default |
| physical_commit.max_records_zero_means_no_positive_limit | 0 | 0 | same_fixed_default |
| physical_commit.max_requests_zero_means_no_positive_limit | 0 | 0 | same_fixed_default |
| physical_commit.queue_requests | 1024 | 1024 | same_fixed_default |
| physical_commit.shards | 1 | 1 | same_fixed_default |
| replication_runtime.batch_bytes | 4194304 | 4194304 | same_fixed_default |
| replication_runtime.batch_items | 256 | 256 | same_fixed_default |
| replication_runtime.close_timeout_ms | 5000 | 5000 | same_fixed_default |
| replication_runtime.exchange_timeout_ms | 5000 | 5000 | same_fixed_default |
| replication_runtime.local_timeout_ms | 5000 | 5000 | same_fixed_default |
| replication_runtime.local_workers | 32 | 32 | same_fixed_default |
| replication_runtime.max_channels | 100000 | 100000 | same_fixed_default |
| replication_runtime.max_voters | 3 | 3 | same_fixed_default |
| replication_runtime.peer_workers | 32 | 32 | same_fixed_default |
| replication_runtime.per_target_inflight | 16 | 16 | same_fixed_default |
| replication_runtime.per_target_queue_bytes | 67108864 | 67108864 | same_fixed_default |
| replication_runtime.per_target_queue_items | 2048 | 2048 | same_fixed_default |
| replication_runtime.queue_bytes | 268435456 | 268435456 | same_fixed_default |
| replication_runtime.queue_items | 8192 | 8192 | same_fixed_default |
| replication_runtime.recovery_page_bytes | 1048576 | 1048576 | same_fixed_default |
| replication_runtime.recovery_timeout_ms | 15000 | 15000 | same_fixed_default |
| replication_runtime.repair_retry_ms | 10 | 10 | same_fixed_default |
| replication_runtime.repair_workers | 8 | 8 | same_fixed_default |
| replication_runtime.replica_hedge_delay_ms | 25 | 25 | same_fixed_default |
| replication_runtime.retained_commands | 64 | 64 | same_fixed_default |
| replication_runtime.trailing_flush_ms | 100 | 100 | same_fixed_default |
| process.GOMAXPROCS | [2,2,2] | unset in systemd/template; actual runtime unknown | different_or_unknown |
| gateway.actor_shards | 2 | unknown; 4 if cloud effective GOMAXPROCS=4 | different_or_unknown |
| gateway.gnet_event_loops | 4 | 4 | same_source_default_or_explicit |
| gateway.gnet_multicore | True | True | same_source_default_or_explicit |
| gateway.async_send_workers | 1000 | 1000 | same_source_default_or_explicit |
| gateway.async_send_queue_records | 131072 | 131072 | same_source_default_or_explicit |
| gateway.session_batch_max_records | 1 | 128 | confirmed_configuration_difference |
| gateway.session_batch_max_wait_ms | 1 | 1 | same_source_default_or_explicit |
| gateway.session_batch_max_bytes | 524288 | 524288 | same_source_default_or_explicit |
| gateway.send_timeout_ms | 5000 | 5000 | same_source_default_or_explicit |
| slot.compaction_enabled | True | True | same_source_default_or_explicit |
| slot.compaction_trigger_entries | 1000 | 10000 | confirmed_configuration_difference |
| slot.compaction_check_interval_ms | 5000 | 30000 | confirmed_configuration_difference |
| slot.raft_tick_interval_ms | 50 | 50 | same_source_default_or_explicit |
| slot.raft_heartbeat_ticks | 2 | 2 | same_source_default_or_explicit |
| slot.raft_election_floor_ticks | 40 | 40 | same_source_default_or_explicit |
| topology.logical_slots_hashslots_replicas | [12,256,3] | [12,256,3] | same_explicit_values |
| channel.max_loaded_channels | 50000 | 50000 | same_explicit_values |
| append.authority_dispatch_concurrency | 100 | 100 | same_source_default |
| storage.sync_durability | True | True | same_required_semantics |
| storage.engine_BytesPerSync_argument | 16777216 | 0 (Pebble library default normalization) | platform_difference |
| presence.activation_timeout_ms | 3000 | 3000 | same_source_default_or_explicit |
| presence.touch_flush_interval_ms | 1000 | 1000 | same_source_default_or_explicit |
| presence.touch_batch_size | 512 | 512 | same_source_default_or_explicit |
| presence.touch_max_routes_per_flush | 65536 | 65536 | same_source_default_or_explicit |
| presence.route_ttl_ms | 90000 | 90000 | same_source_default_or_explicit |
| send_permission.cohort_window_ms | 1 | 1 | same_frozen_source |
| send_permission.cohort_max_calls | 64 | 64 | same_frozen_source |
| send_permission.cohort_max_active | 64 | 64 | same_frozen_source |
| send_permission.cohort_max_owned_calls | 1024 | 1024 | same_frozen_source |
| send_permission.cohort_max_owned_bytes | 16777216 | 16777216 | same_frozen_source |

Exact source lines, scalar-only raw declarations and all input hashes are in `runtime-config-comparison.json` and `source-defaults.json`.

Workload bytes and fixed major runtime capacities match, but runtime config and physical environment do not. These differences may change admission/queue pressure and cannot yet be assigned causal weight. No original-cloud root-cause equivalence or capacity acceptance is claimed.

The first controlled distinction worth testing is the explicit gateway record-batch limit1 versus128 while leaving GOMAXPROCS, compaction, all product capacities/timeouts, workload identities/rate and OS unchanged. A full cold-start run also contains unmatched Slot compaction1000/5s, which remains a confounder even if batching changes the outcome. A separate profile diagnostic of the unchanged full-v2 condition would expose blocking and CPU work without qualifying performance. Neither experiment has been started or authorized by this addendum.
