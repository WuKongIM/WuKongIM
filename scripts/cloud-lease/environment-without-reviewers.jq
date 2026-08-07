{
  wait_timer: ([.protection_rules[]? | select(.type == "wait_timer") | .wait_timer][0] // 0),
  prevent_self_review: false,
  reviewers: [],
  deployment_branch_policy: .deployment_branch_policy
}
| if .deployment_branch_policy == null then del(.deployment_branch_policy) else . end
