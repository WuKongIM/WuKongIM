if (.node_id == $node_id and .source == "effective_startup_config" and .requires_restart == true)
then .
else error("invalid effective startup config identity")
end |
[.groups[].items[]] |
(reduce .[] as $item ({}; .[$item.key] = $item)) as $items |
def integer($key):
  $items[$key] as $item |
  if ($item == null or $item.sensitive == true or $item.redacted == true)
  then error("missing effective config " + $key)
  else ($item.value | tonumber)
  end;
{
  node_id: $node_id,
  physical_hash_slots: integer("WK_CLUSTER_HASH_SLOT_COUNT"),
  logical_slot_groups: integer("WK_CLUSTER_INITIAL_SLOT_COUNT"),
  slot_replicas: integer("WK_CLUSTER_SLOT_REPLICA_N"),
  channel_replicas: integer("WK_CLUSTER_CHANNEL_REPLICA_N")
}
