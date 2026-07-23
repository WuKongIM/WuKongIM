if (.node_id == $node_id and .source == "effective_startup_config" and .requires_restart == true)
then .
else error("invalid effective startup config identity")
end |
[.groups[].items[]] |
(reduce .[] as $item ({}; .[$item.key] = $item)) as $items |
[
  "WK_CLUSTER_HASH_SLOT_COUNT",
  "WK_CLUSTER_INITIAL_SLOT_COUNT",
  "WK_CLUSTER_SLOT_REPLICA_N",
  "WK_CLUSTER_CHANNEL_REPLICA_N",
  "WK_CLUSTER_CHANNEL_REACTOR_COUNT",
  "WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS",
  "WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS",
  "WK_CLUSTER_CHANNEL_RPC_WORKERS",
  "WK_CLUSTER_CHANNEL_RPC_BATCH_MAX_ITEMS",
  "WK_GATEWAY_GNET_MULTICORE",
  "WK_GATEWAY_GNET_NUM_EVENT_LOOP",
  "WK_GATEWAY_RUNTIME_ASYNC_SEND_WORKERS",
  "WK_GATEWAY_RUNTIME_ASYNC_SEND_QUEUE_CAPACITY",
  "WK_DELIVERY_RECIPIENT_WORKER_CONCURRENCY",
  "WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS"
] as $keys |
def item($key):
  if $items[$key] == null then error("missing effective config " + $key) else $items[$key] end;
def integer($key): (item($key).value | tonumber);
def boolean($key):
  item($key).value as $value |
  if $value == "true" then true elif $value == "false" then false else error("invalid boolean " + $key) end;
def source($key):
  item($key).source as $value |
  if ($value == "toml" or $value == "env" or $value == "default" or $value == "derived") then $value else error("invalid source " + $key) end;
{
  schema: "wukongim/cloud-effective-node-runtime-contract/v1",
  scale: $scale,
  physical_hash_slot_count: integer("WK_CLUSTER_HASH_SLOT_COUNT"),
  logical_slot_group_count: integer("WK_CLUSTER_INITIAL_SLOT_COUNT"),
  slot_replica_count: integer("WK_CLUSTER_SLOT_REPLICA_N"),
  channel_replica_count: integer("WK_CLUSTER_CHANNEL_REPLICA_N"),
  channel_reactor_count: integer("WK_CLUSTER_CHANNEL_REACTOR_COUNT"),
  channel_store_append_workers: integer("WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS"),
  channel_store_apply_workers: integer("WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS"),
  channel_rpc_workers: integer("WK_CLUSTER_CHANNEL_RPC_WORKERS"),
  channel_rpc_batch_max_items: integer("WK_CLUSTER_CHANNEL_RPC_BATCH_MAX_ITEMS"),
  gateway_gnet_multicore: boolean("WK_GATEWAY_GNET_MULTICORE"),
  gateway_gnet_event_loops: integer("WK_GATEWAY_GNET_NUM_EVENT_LOOP"),
  gateway_async_send_workers: integer("WK_GATEWAY_RUNTIME_ASYNC_SEND_WORKERS"),
  gateway_async_send_queue_capacity: integer("WK_GATEWAY_RUNTIME_ASYNC_SEND_QUEUE_CAPACITY"),
  recipient_worker_concurrency: integer("WK_DELIVERY_RECIPIENT_WORKER_CONCURRENCY"),
  conversation_authority_cache_max_rows: integer("WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS"),
  value_sources: (reduce $keys[] as $key ({}; .[$key] = source($key)))
}
