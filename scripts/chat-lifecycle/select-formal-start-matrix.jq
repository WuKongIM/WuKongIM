. as $artifacts |
[ $artifacts[] | select(.name | startswith("chat-lifecycle-formal-transition-")) |
  . + {request_id:(.name | ltrimstr("chat-lifecycle-formal-transition-"))} |
  select(($requested == "" or .request_id == $requested) and
         (.request_id | test("^[A-Za-z0-9][A-Za-z0-9._-]{0,95}$"))) ] |
group_by(.request_id) | map(max_by(.created_at)) |
sort_by(.created_at) | reverse |
[ .[] as $transition |
  ([ $artifacts[] | select(.name == ("chat-lifecycle-formal-handoff-" + $transition.request_id) and .created_at >= $transition.created_at) ] |
    max_by(.created_at)) as $handoff |
  ([ $artifacts[] | select(.name == ("chat-lifecycle-formal-cleanup-" + $transition.request_id) and .created_at >= $transition.created_at) ] |
    max_by(.created_at)) as $cleanup |
  select($handoff == null and $cleanup == null) |
  {request_id:$transition.request_id,transition_run_id:$transition.workflow_run.id} ] |
{include:.[0:1]}
