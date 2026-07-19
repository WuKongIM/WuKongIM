def has_legal_leader:
  (.runtime.leader_id // 0) as $leader_id
  | ($leader_id != 0 and ((.runtime.current_voters // []) | index($leader_id)) != null);

{
  healthy_slot_leaders: (
    [.items[]
      | select(.state.quorum == "ready" and .state.sync == "matched" and has_legal_leader)
      | (.hash_slots.count // 0)]
    | add // 0
  ),
  healthy_slot_replicas: (
    [.items[]
      | select(.state.quorum == "ready" and .state.sync == "matched" and ((.runtime.current_voters // []) | length) == 3)
      | (.hash_slots.count // 0)]
    | add // 0
  )
}
