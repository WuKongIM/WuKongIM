. as $artifacts |
[ $artifacts[] | select(.name | startswith($prefix)) |
  . + {request_id:(.name | ltrimstr($prefix))} |
  select(($requested == "" or .request_id == $requested) and
         (.request_id | test("^[A-Za-z0-9][A-Za-z0-9._-]{0,95}$"))) ] |
group_by(.request_id) | map(max_by(.created_at)) |
sort_by(.created_at) | reverse |
[ .[] as $handoff |
  ([ $artifacts[] | select(.name == ($final_prefix + $handoff.request_id) and .created_at >= $handoff.created_at) ] |
    max_by(.created_at)) as $final |
  ([ $artifacts[] | select(.name == ($cleanup_prefix + $handoff.request_id) and .created_at >= $handoff.created_at) ] |
    max_by(.created_at)) as $cleanup |
  {request_id:$handoff.request_id,handoff_run_id:$handoff.workflow_run.id,
   final_exists:($final != null),final_run_id:($final.workflow_run.id // 0),
   cleanup_run_id:($cleanup.workflow_run.id // 0)} ] |
{include:.}
