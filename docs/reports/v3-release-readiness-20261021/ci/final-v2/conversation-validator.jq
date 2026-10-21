def positive_number: type == "number" and . > 0 and . < 1e100;
def nonnegative_number: type == "number" and . >= 0 and . < 1e100;
def seconds: sub("\\.[0-9]+Z$"; "Z") | fromdateiso8601;
def phase_ok($workers; $duration; $nodes):
  .nodes == $nodes and
  .driver_workers == $workers and
  .queue_capacity == ([$workers, (.case.offered_qps * $profile[0].max_p99_ms / 1000 | ceil)] | max) and
  .verdict == "pass" and .errors == 0 and .unexpected_errors == 0 and .dropped == 0 and
  .runtime_loads == 0 and .membership_writes == 0 and
  .active_runtimes_before == 0 and .active_runtimes_after == 0 and
  .duration_seconds == $duration and
  .scheduled == (.case.offered_qps * $duration) and .completed == .scheduled and
  (.completed_in_window | nonnegative_number) and .completed_in_window <= .completed and
  .actual_qps == (.completed_in_window / $duration) and
  .actual_qps >= (.case.offered_qps * $profile[0].min_completion_ratio) and
  (.p50_ms | positive_number) and .p95_ms >= .p50_ms and .p99_ms >= .p95_ms and
  (.p99_ms | positive_number) and .p99_ms <= $profile[0].max_p99_ms;
def expected_windows:
  [{name:"mixed",page:0,cohort:0},
   {name:"hidden_50_percent",page:1,cohort:0}, {name:"hidden_50_percent",page:2,cohort:0},
   {name:"hidden_90_percent",page:1,cohort:1}, {name:"hidden_90_percent",page:2,cohort:1},
   {name:"hidden_after_99_visible",page:1,cohort:2}, {name:"hidden_after_99_visible",page:2,cohort:2}];

def stress_ok($cfg):
  . as $window |
  (.start | type == "string") and (.end | type == "string") and
  ((.end | seconds) - (.start | seconds)) == (if .name == "mixed" then $cfg.mixed_seconds else $cfg.hidden_seconds end) and
  (.cpu_seconds | positive_number) and (.heap_bytes | positive_number) and (.allocated_bytes | positive_number) and
  .runtime_loads == 0 and .membership_writes == 0 and
  .active_runtimes_before == 0 and .active_runtimes_after == 0 and
  (.allocated_bytes / ([.phases[].completed] | add)) <= $cfg.max_allocated_bytes_per_request[.name] and
  (if .name == "mixed" then
    [.phases[].case] == [{endpoint:"/conversation/list",page_size:100,offered_qps:$cfg.mixed_list_qps},
                         {endpoint:"/conversation/sync",page_size:100,offered_qps:$cfg.mixed_sync_qps}] and
    all(.phases[]; phase_ok($cfg.mixed_workers_per_endpoint; $cfg.mixed_seconds; 3))
   else
    [.phases[].case] == [{endpoint:"/conversation/sync",page_size:(if .page == 1 then 100 else 50 end),offered_qps:$cfg.hidden_qps}] and
    all(.phases[]; phase_ok($cfg.hidden_workers; $cfg.hidden_seconds; 3))
   end) and
  all(.phases[]; .cpu_seconds == null and .allocated_bytes == 0 and .heap_bytes == 0);

.schema == "wukongim/conversation-qps-report/v2" and
$profile[0].schema == "wukongim/conversation-qps-profile/v2" and
.source_sha == $sha and .source_dirty == false and
.profile_sha256 == $profile_sha and (.binary_sha256 | test("^[0-9a-f]{64}$")) and
.os == "linux" and .arch == "amd64" and .node_gomaxprocs == 2 and
.passed == true and (.phases | length == 12) and
([.phases[] | [.nodes, .case.endpoint, .case.page_size]] | unique | length == 12) and
all(.phases[];
  (.nodes == 1 or .nodes == 3) and (.case as $case | $profile[0].cases | index($case) != null) and
  phase_ok($profile[0].workers; $profile[0].duration_seconds; .nodes) and
  (.cpu_seconds | nonnegative_number) and (.heap_bytes | positive_number) and (.allocated_bytes | positive_number) and
  (.allocated_bytes / .completed) <= .case.max_allocated_bytes_per_request[(.nodes | tostring)]) and
.stress_config == $profile[0].stress_gate and
([.stress[] | {name, page:(.page // 0), cohort:(.cohort // 0)}] == expected_windows) and
all(.stress[]; stress_ok($profile[0].stress_gate))
