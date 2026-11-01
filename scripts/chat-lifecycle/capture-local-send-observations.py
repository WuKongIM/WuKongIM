#!/usr/bin/env python3
"""Bounded local SEND evidence export after load exit, before node teardown.

This optional diagnostic never changes a workload verdict. It makes no discovery,
retry, remote request, or latest-trace query. Missing evidence remains unknown.
"""
import argparse
import datetime as dt
import hashlib
import http.client
import json
import os
from pathlib import Path
import re
import signal
import stat
import tempfile
import time

QUERY_PATH = '/debug/diagnostics/send-attempts/query'
# ChannelAppendRPCServiceID = clusternet.RPCChannelAuthoritySend, not replication86.
APPEND_SERVICE_ID = 21
# clusternet.RPCNodeSendPermissions, distinct from append and legacy Slot reads.
PERMISSION_SERVICE_ID = 91
MAX_REQUESTS = 18
MAX_ARTIFACT = 4 << 20
HEX = re.compile(r'[0-9a-f]{32}')
TRACE = re.compile(r'[0-9a-f]{32}/[0-9a-f]{1,16}')


class Unknown(Exception):
    """Closed diagnostic failure; never contains a response body or credential."""


def require(condition):
    if not condition:
        raise Unknown()


def integer(value, maximum=(1 << 64)-1):
    return type(value) is int and 0 <= value <= maximum


def stamp(value):
    require(type(value) is str)
    value = dt.datetime.fromisoformat(value.replace('Z', '+00:00'))
    require(value.utcoffset() is not None)
    return value


def read_regular(path, maximum):
    descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(descriptor, 'rb') as stream:
        info = os.fstat(stream.fileno())
        require(stat.S_ISREG(info.st_mode) and info.st_size <= maximum)
        body = stream.read(maximum+1)
    require(len(body) <= maximum)
    return body


def strict_json(body):
    def pairs(rows):
        out = {}
        for key, value in rows:
            require(key not in out)
            out[key] = value
        return out
    def constant(_):
        raise Unknown()
    return json.loads(body, object_pairs_hook=pairs, parse_constant=constant)


def bounded_json(path, maximum):
    body = read_regular(path, maximum)
    return strict_json(body), hashlib.sha256(body).hexdigest()


def key_valid(key):
    return (isinstance(key, dict) and set(key) == {'message_fingerprint', 'client_seq', 'connect_packet_fingerprint'}
            and integer(key['client_seq'], (1 << 32)-1) and key['client_seq'] > 0
            and all(isinstance(key[k], str) and HEX.fullmatch(key[k]) and key[k] != '0'*32
                    for k in ('message_fingerprint', 'connect_packet_fingerprint')))


def identity(key):
    return json.dumps(key, sort_keys=True, separators=(',', ':'))


def object_shape(required, optional=None):
    """Closed existing Go DTO shape; unknown fields never enter the artifact."""
    optional = optional or {}
    fields = {**required, **optional}
    return lambda value: (type(value) is dict and set(required) <= set(value) <= set(fields)
                          and all(fields[key](item) for key, item in value.items()))


def array_shape(item, maximum, exact=None):
    return lambda value: ((value is None and exact is None) or
                          (type(value) is list and len(value) <= maximum
                           and (exact is None or len(value) == exact) and all(item(v) for v in value)))


def timestamp_valid(value):
    try:
        return type(value) is str and stamp(value) is not None
    except (ValueError, TypeError, AttributeError, Unknown):
        return False


def one_of(*values):
    return lambda value: type(value) is str and value in values


def nullable(check):
    return lambda value: value is None or check(value)


# These are the existing rpctrace/sendtrace DTOs, not a new trace protocol.
# Future fields fail closed until this same-source diagnostic reader is updated.
BOOLEAN = lambda v: type(v) is bool
UINT = integer
DURATION = lambda v: integer(v, (1 << 63)-1)
OFFSET = lambda v: type(v) is int and -(1 << 63) <= v < (1 << 63)
FINGERPRINT = lambda v: type(v) is str and HEX.fullmatch(v) is not None
RPC_ID = lambda v: type(v) is str and TRACE.fullmatch(v) is not None and int(v.split('/')[1], 16) != 0
PARENT_ID = lambda v: RPC_ID(v) or (type(v) is str and v.startswith('local/') and RPC_ID(v[6:]))
RPC_RESULT = one_of('ok', 'error', 'timeout', 'canceled', 'busy', 'stopped', 'unfinished')
SEND_RESULT = one_of('', 'ok', 'error', 'timeout', 'canceled', 'partial', 'dropped', 'skipped')
RPC_STAGE = one_of(*'''peer_acquire capability client_queue request_write response_wait service_queue handler
response_encode response_queue response_write response_receive_observer response_dispatch response_resume
recovery_admission recovery_flight_wait recovery_owners_admission recovery_owner_read recovery_install
permission_admission permission_barrier permission_snapshot permission_authority slot_control_wait
slot_read_eligibility slot_raw_quorum slot_ready_to_accept slot_apply_wait slot_ready_save_entries
slot_ready_save_hardstate slot_ready_save_snapshot slot_ready_save_mixed slot_raft_send commit_admission
commit_queue commit_collect commit_build commit_durable commit_publish store_append_locks store_append_prepare
channel_submit channel_future_wait channel_prepare_wait channel_prepare channel_first_append_pending
channel_first_append_queue channel_first_append channel_after_first_append channel_meta_resolve channel_meta_apply
channel_runtime_append channel_runtime_reserve channel_runtime_submit channel_runtime_future_wait
channel_local_queue channel_local_sync channel_peer_wait channel_quorum_decision channel_reactor_complete'''.split())
PREPARE = object_shape(dict(known_mask=lambda v: integer(v, 3), frontier_wall=DURATION, frontier_calls=UINT,
                            item_prepare_wall=DURATION, item_prepare_calls=UINT))
SPAN = object_shape(dict(stage=RPC_STAGE, offset=OFFSET, duration=DURATION, result=RPC_RESULT),
                    dict(incomplete=BOOLEAN, store_prepare_detail=nullable(PREPARE)))
OVERLAY = object_shape(dict(duration=DURATION, count=UINT), dict(unfinished=BOOLEAN))
BUDGET_OWNER = object_shape(dict(duration=DURATION, count=UINT, drain_wait=DURATION, save_permit=DURATION,
                                 sync_work=DURATION, reject_mask=lambda v: integer(v, 15)), dict(unfinished=BOOLEAN))
OTHER_OWNER = object_shape(dict(duration=DURATION, count=UINT, cause_mask=lambda v: integer(v, 63)), dict(unfinished=BOOLEAN))
OWNER = object_shape(dict(budget=BUDGET_OWNER, other=OTHER_OWNER))
BARRIER_PHASE = object_shape(dict(stage=RPC_STAGE, observed=BOOLEAN, save=OVERLAY, send=OVERLAY))
BARRIER = object_shape(dict(phases=array_shape(BARRIER_PHASE, 5, 5)),
                       dict(owner_storage=nullable(object_shape(dict(phases=array_shape(OWNER, 5, 5)))),
                            storage_count_paused=nullable(object_shape(dict(phases=array_shape(OVERLAY, 5, 5))))))
RECEIVE = object_shape(dict(duration=DURATION, observer_duration=DURATION))
DISPATCH = object_shape(dict(known_mask=lambda v: integer(v, 65535), offsets=array_shape(OFFSET, 16, 16),
                             queue_deadline=OFFSET, queue_deadline_known=BOOLEAN),
                        dict(terminal_path=one_of('', 'handler', 'enqueue_rejected', 'context_expiry', 'queue_expiry',
                                                  'dequeue_rejected', 'submit_rejected', 'worker_rejected', 'stopped')))
COHORT_REF = object_shape(dict(instance=lambda v: type(v) is str and re.fullmatch(r'local/[0-9a-f]{32}/', v) is not None, attempt=UINT))
INTERVAL = object_shape(dict(observed=BOOLEAN, complete=BOOLEAN, offset=OFFSET, duration=DURATION))
NATIVE = object_shape(dict(available=BOOLEAN, **{key: DURATION for key in (
    'total_duration', 'semaphore_wait_duration', 'wal_queue_wait_duration', 'mem_table_write_stall_duration',
    'l0_read_amp_write_stall_duration', 'wal_rotation_duration', 'commit_wait_duration')}))
COHORT_LINK = object_shape(dict(cohort_ref=COHORT_REF, request_ordinal=lambda v: integer(v, (1 << 32)-1),
                                **{key: INTERVAL for key in ('physical_window', 'submit_window', 'build', 'durable', 'publish', 'terminal_wait', 'finalize')},
                                timed_sum=DURATION, native=NATIVE, outcome=one_of('committed', 'definitely_not_committed', 'unknown')),
                           dict(rebuild_of=nullable(COHORT_REF)))
COHORTS = object_shape(dict(links=array_shape(COHORT_LINK, 2), dropped=UINT), dict(owners_dropped=UINT))
RPC_SAMPLE = object_shape(dict(trace_id=RPC_ID, side=one_of('client', 'server'), service_id=lambda v: integer(v, 65535),
                               at=timestamp_valid, duration=DURATION, result=RPC_RESULT, spans=array_shape(SPAN, 32)),
                          dict(parent_trace_id=PARENT_ID, unbound=BOOLEAN, operation=lambda v: integer(v, 255),
                               dropped_spans=UINT, recorder_dropped=UINT, barrier_groups=UINT, dropped_barrier_groups=UINT,
                               barrier_coverage=nullable(BARRIER), receive_prefix=nullable(RECEIVE),
                               dispatch=nullable(DISPATCH), commit_cohorts=nullable(COHORTS)))
ATTEMPT_PHASE_IDENTITIES = (
    ('gateway.async_dispatch_wait', 'enqueue_to_dispatch'),
    ('gateway.messages_send', 'message_call_to_result'),
    ('gateway.write_sendack', 'write_frame_callback'),
    ('gateway.session_publication_wait', 'ready_to_publication_attempt'),
    ('message.send_durable', 'diagnostic_event_duration'),
    ('message.permission_batch', 'shared_permission_batch'),
    ('message.pre_append', 'shared_pre_append_until_submit'),
    ('message.permission_rpc', 'longest_origin_rpc91'))
PHASE = object_shape(dict(observed=BOOLEAN, stage=one_of(*(stage for stage, _ in ATTEMPT_PHASE_IDENTITIES)),
                         started_at=timestamp_valid, duration=DURATION, result=SEND_RESULT,
                         boundary=one_of(*(boundary for _, boundary in ATTEMPT_PHASE_IDENTITIES)), end_derived=BOOLEAN))
REFERENCE_SHAPE = object_shape(dict(state=one_of('bound', 'unbound', 'recorder_disabled', 'recorder_exhausted', 'local_untraced', 'remote_untraced'),
                                    trace_id=lambda v: type(v) is str))


def reference_valid(value):
    """Mirror sendtrace.RPCReference.Valid, including closed state/ID pairing."""
    if not REFERENCE_SHAPE(value):
        return False
    state, trace = value['state'], value['trace_id']
    if state == 'bound':
        return RPC_ID(trace)
    if state == 'unbound':
        return trace.startswith('local/') and RPC_ID(trace[6:])
    return trace == ''


def attempt_phases_valid(value):
    """Accept complete legacy/new fixed lists, with the same closed phase DTO."""
    return (type(value) is list and len(value) in (5, 7, 8)
            and all(PHASE(phase) and (phase['stage'], phase['boundary']) == identity
                    for phase, identity in zip(value, ATTEMPT_PHASE_IDENTITIES)))


CANDIDATE = object_shape(dict(token=object_shape(dict(epoch=FINGERPRINT, ordinal=UINT)), phases=attempt_phases_valid),
                         dict(upper_trace_id=FINGERPRINT, trace_ambiguous=BOOLEAN, rpc_overflow=BOOLEAN,
                              rpc_references=array_shape(reference_valid, 2),
                              permission_rpc_overflow=BOOLEAN, permission_rpc_references=array_shape(reference_valid, 2)))


def process_alive(pid):
    require(pid > 1)
    try:
        os.kill(pid, 0)
        return True
    except ProcessLookupError:
        return False


def anchors(args):
    """Bind terminal native fences before any network operation."""
    require(not process_alive(args.coordinator_pid))
    start, start_hash = bounded_json(args.report_dir/'run-start.json', 4096)
    final, final_hash = bounded_json(args.report_dir/'final.json', 1 << 20)
    side, side_hash = bounded_json(args.report_dir/'attempt-link-diagnostics.json', 64 << 10)
    require(start['schema'] == 'wukongim.chat_lifecycle.run_start/v1'
            and final['schema_version'] in ('wukongim/chat-lifecycle-report/v8', 'wukongim/chat-lifecycle-report/v9')
            and final['kind'] == 'final' and final['final'] is True
            and final['continue'] is False and final['verdict']['terminal'] is True)
    require(start['run_hash'] == 'sha256:'+hashlib.sha256(args.run_id.encode()).hexdigest())
    require(start['generation'] > 0 and integer(start['generation']))
    require(re.fullmatch(r'sha256:[0-9a-f]{64}', start['assignment_hash']))
    require(identity(final['fence']) == identity({k: start[k] for k in ('run_hash', 'assignment_hash', 'generation')}))
    begin, end = stamp(start['started_at']), stamp(final['window']['end'])
    require(begin == stamp(final['window']['start']) and begin <= end)
    require(-5 <= (dt.datetime.now(dt.timezone.utc)-end).total_seconds() <= 60)
    latency = final['thresholds']['latency']
    hot = latency['hot_sendack']
    percentile = 99
    if final['schema_version'] == 'wukongim/chat-lifecycle-report/v9':
        percentile = 95
        require(final['threshold_version'] == 'wukongim/chat-lifecycle-thresholds/v3'
                and final['profile'] == 'local' and final['stage'] == 'shakeout' and final['mode'] == 'soak'
                and final['qualification']['scope'] == 'local_diagnostic'
                and final['qualification']['external_linux_evidence_required'] is False
                and set(hot) == {'p95','p999'} and hot['p999'] == 1000000000)
        require(all('p95' not in latency.get(key,{}) for key in ('cold','sync')))
    else:
        require('p95' not in hot)
    limit = hot['p95' if percentile == 95 else 'p99']
    deadline = final['thresholds']['latency']['hot_attempt_timeout']
    if percentile == 95:
        require(all(integer(value) for value in (limit, hot['p999'], deadline)))
    require(limit == 600000000 and deadline == 2000000000)
    require(side['schema'] == 'wukongim/chat-lifecycle-attempt-link-diagnostics/v2'
            and side['run_id'] == args.run_id and stamp(side['at']) == end)
    workers, generations = side['workers'], final['worker_generations']
    require(len(workers) == len(generations) == 3)
    require({w['worker_id'] for w in workers} == {0, 1, 2}
            and {w['worker_index'] for w in generations} == {0, 1, 2})
    require(all(integer(w['worker_id'], 2) for w in workers)
            and all(integer(w['worker_index'], 2) for w in generations))
    by_id = {w['worker_index']: w for w in generations}
    selected, seen_keys, retention_policies = [], set(), []
    for worker in workers:
        peer = by_id[worker['worker_id']]
        require(worker['phase'] == peer['phase'] == 'final'
                and worker['generation'] == peer['generation'] == start['generation']
                and worker['snapshot_sequence'] == peer['snapshot_sequence']
                and integer(worker['generation']) and integer(peer['generation'])
                and integer(peer['snapshot_sequence'])
                and integer(worker['snapshot_sequence']) and worker['snapshot_sequence'] > 0
                and isinstance(worker['assignment_id'], str) and 0 < len(worker['assignment_id']) <= 128)
        require('sha256:'+hashlib.sha256(worker['assignment_id'].encode()).hexdigest() == start['assignment_hash'])
        diagnostic = worker['attempt_link_diagnostics']
        require(diagnostic['generation'] == worker['generation'])
        slow = diagnostic['slow_hot_success']
        require((type(slow.get('percentile')) is int and slow['percentile'] == 95)
                if percentile == 95 else 'percentile' not in slow)
        rows = slow['records']
        require(slow['limit_nanos'] == limit and integer(slow['seen'])
                and integer(slow['overwritten'])
                and len(rows) == min(slow['seen'], 16)
                and slow['overwritten'] == slow['seen']-len(rows))
        policy = slow.get('retention_policy', '')
        require(type(policy) is str)
        if policy:
            require(policy == 'p999_reserve8/v1' and integer(slow['tail_limit_nanos'])
                    and integer(hot['p999']) and slow['tail_limit_nanos'] == hot['p999']
                    and slow['tail_limit_nanos'] >= limit)
            retention_policies.append(dict(worker_id=worker['worker_id'], policy=policy,
                                           tail_limit_nanos=slow['tail_limit_nanos']))
        else:
            require('tail_limit_nanos' not in slow)
        for i, row in enumerate(rows):
            require(set(row) == {'ordinal', 'key', 'kind', 'attribution', 'deadline_after_registration',
                                'observed_after_registration', 'sdk_pending_to_write', 'sdk_write_to_observed', 'timing_mask', 'observed_at'})
            ordinal_valid = row['ordinal'] == slow['seen']-len(rows)+i+1
            if policy:
                ordinal_valid = (0 < row['ordinal'] <= slow['seen']
                                 and (i == 0 or row['ordinal'] > rows[i-1]['ordinal'])
                                 and (i != len(rows)-1 or row['ordinal'] == slow['seen']))
            require(integer(row['ordinal']) and ordinal_valid
                    and DURATION(row['sdk_pending_to_write']) and DURATION(row['sdk_write_to_observed'])
                    and integer(row['timing_mask'], 3))
            observed = stamp(row['observed_at'])
            require(begin <= observed <= end)
            require(integer(row['observed_after_registration'])
                    and row['deadline_after_registration'] == deadline
                    and limit < row['observed_after_registration'] < deadline
                    and row['kind'] == 'sendack_success'
                    and row['attribution'] in ('candidate', 'connection_unknown'))
            if (end-observed).total_seconds() > 300 or row['attribution'] != 'candidate':
                continue
            require(key_valid(row['key']))
            key = identity(row['key'])
            if key not in seen_keys:
                seen_keys.add(key)
                selected.append({'worker_id': worker['worker_id'], **row})
    verdict = final['verdict']
    require(verdict['outcome'] in ('pass', 'rehearsal_pass', 'passed_with_capacity_warning', 'product_failure',
                                  'insufficient_evidence', 'harness_invalid', 'infrastructure_failure', 'operator_stop'))
    require(verdict.get('cause', '') in '''completed rehearsal_completed message_loss message_duplicate message_corruption
sequence_regression terminal_send activation_rejection overall_first_attempt_rate minute_first_attempt_rate counter_regression
queue_saturation observer_gap server_crash disk_exhausted budget_exhausted lease_expiry operator_requested hot_latency cold_latency
sync_latency invalid_observation heap_growth goroutine_growth queue_recovery worker_product_failure worker_harness_invalid
lifecycle_product_failure lifecycle_harness_invalid meta_create_product_failure infrastructure_capacity capacity_headroom_latency
insufficient_evidence'''.split()+[''])
    return selected, dict(run_start_sha256=start_hash, final_sha256=final_hash, sidecar_sha256=side_hash,
                         fence=final['fence'], original_outcome={k: verdict[k] for k in ('terminal', 'outcome', 'cause') if k in verdict},
                         terminal_at=final['window']['end'], anchor_retention_policies=retention_policies)


def response_valid(data, body, node):
    """Keep exact typed selector echoes; a sole candidate is still a candidate."""
    require(set(data) <= {'version', 'node_id', 'status', 'results', 'rpc_results'})
    require(type(data['version']) is int and data['version'] == 1 and data['status'] in ('ok', 'unsupported', 'unknown'))
    require('node_id' not in data or (integer(data['node_id'], 3) and data['node_id'] in (0, node)))
    rows = data['results']
    require(isinstance(rows, list) and len(rows) <= 16)
    if body.get('keys'):
        require(data.get('rpc_results') in (None, []))
        require(data.get('node_id', node) == node)
        if data['status'] == 'ok':
            require(len(rows) == len(body['keys']))
        for i, row in enumerate(rows):
            require(set(row) == {'key', 'status', 'candidates'} and key_valid(row['key']) and identity(row['key']) == identity(body['keys'][i])
                    and row['status'] in ('candidate', 'ambiguous', 'not_retained', 'unknown')
                    and isinstance(row['candidates'], list) and len(row['candidates']) <= 2)
            for candidate in row['candidates']:
                require(CANDIDATE(candidate))
    else:
        require(not rows)
        rpc = data['rpc_results']
        require(len(rpc) == len(body['rpc_selectors']))
        for row, selector in zip(rpc, body['rpc_selectors']):
            require(set(row) <= {'selector', 'status', 'sample'} and identity(row['selector']) == identity(selector)
                    and row['status'] in ('retained', 'not_retained', 'unknown', 'ambiguous', 'unsupported'))
            if row['status'] == 'retained':
                sample = row['sample']
                require(RPC_SAMPLE(sample) and sample['trace_id'] == selector['trace_id'] and sample['side'] == selector['side']
                        and sample['service_id'] == selector['service_id'] and not sample.get('unbound', False)
                        and len(sample['spans'] or []) <= 32
                        and len((sample.get('commit_cohorts') or {}).get('links') or []) <= 2)
            else:
                require(row.get('sample') is None)


class QueryBudget:
    """A fixed wall/byte/request budget, with no retries or redirects."""
    def __init__(self, ports, token):
        self.ports, self.token = ports, token
        self.requests = 0
        self.deadline = time.monotonic()+8
        self.rows = []

    def query(self, node, body):
        require(self.requests < MAX_REQUESTS and time.monotonic() < self.deadline)
        self.requests += 1
        result = {'node_id': node, 'request': body, 'status': 'unknown'}
        self.rows.append(result)
        connection = http.client.HTTPConnection('127.0.0.1', self.ports[node-1], timeout=min(0.35, self.deadline-time.monotonic()))
        maximum = (256 if body.get('rpc_selectors') else 64) << 10
        try:
            encoded = json.dumps(body, separators=(',', ':')).encode()
            require(len(encoded) <= 8192)
            connection.request('POST', QUERY_PATH, encoded, {'Authorization': 'Bearer '+self.token, 'Content-Type': 'application/json'})
            response = connection.getresponse()
            require(response.status == 200)
            raw = response.read(maximum+1)
            require(len(raw) <= maximum and self.token.encode() not in raw)
            data = strict_json(raw)
            response_valid(data, body, node)
            result.update(status='captured', response=data)
            return data
        except (Unknown, OSError, ValueError, KeyError, TypeError, http.client.HTTPException):
            return None
        finally:
            connection.close()


def collect(budget, selected):
    keys = [row['key'] for row in selected]
    categories = {APPEND_SERVICE_ID: [], PERMISSION_SERVICE_ID: []}
    source_nodes = {}
    for node in (1, 2, 3):
        for offset in range(0, len(keys), 16):
            data = budget.query(node, dict(version=1, keys=keys[offset:offset+16]))
            if data is None:
                continue
            for item in data['results']:
                if item['status'] != 'candidate' or len(item['candidates']) != 1:
                    continue
                candidate = item['candidates'][0]
                if candidate.get('trace_ambiguous'):
                    continue
                for field, overflow, service in (
                        ('rpc_references', 'rpc_overflow', APPEND_SERVICE_ID),
                        ('permission_rpc_references', 'permission_rpc_overflow', PERMISSION_SERVICE_ID)):
                    if candidate.get(overflow):
                        continue
                    for ref in candidate.get(field) or []:
                        trace = ref.get('trace_id', '')
                        if ref.get('state') != 'bound' or not TRACE.fullmatch(trace) or int(trace.split('/')[1], 16) == 0:
                            continue
                        key = (service, trace)
                        if key not in source_nodes:
                            # Keep at most16 candidates per category before the shared16 cut.
                            if len(categories[service]) == 16:
                                continue
                            categories[service].append(key)
                            source_nodes[key] = set()
                        source_nodes[key].add(node)
    # Round-robin closed categories; a missing category lends its unused budget.
    references = []
    for i in range(16):
        for service in (APPEND_SERVICE_ID, PERMISSION_SERVICE_ID):
            if i < len(categories[service]) and len(references) < 16:
                references.append(categories[service][i])
    for node in (1, 2, 3):
        selectors = [dict(trace_id=trace, side='client', service_id=service)
                     for service, trace in references if node in source_nodes[(service, trace)]]
        selectors += [dict(trace_id=trace, side='server', service_id=service) for service, trace in references]
        for offset in range(0, len(selectors), 16):
            budget.query(node, dict(version=1, rpc_selectors=selectors[offset:offset+16]))


def atomic_create(path, document):
    raw = json.dumps(document, separators=(',', ':'), ensure_ascii=True).encode()+b'\n'
    require(len(raw) <= MAX_ARTIFACT)
    descriptor, temporary = tempfile.mkstemp(prefix='.send-observation-', dir=path.parent)
    try:
        with os.fdopen(descriptor, 'wb') as stream:
            stream.write(raw)
            stream.flush()
            os.fsync(stream.fileno())
        os.link(temporary, path)  # Atomic create, never overwrite an earlier capture.
    finally:
        os.unlink(temporary)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--report-dir', type=Path, required=True)
    parser.add_argument('--run-id', required=True)
    parser.add_argument('--coordinator-pid', type=int, required=True)
    parser.add_argument('--coordinator-exit-status', type=Path, required=True)
    parser.add_argument('--api-ports', required=True, help='Three distinct literal loopback ports for node1,node2,node3')
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    if args.output.exists():
        return
    document = dict(schema='wukongim/local-send-observations/v1', run_id=args.run_id,
                    status='unknown', causal_verdict='unverified', anchor_count=0, queries=[])
    def expired(*_):
        raise Unknown()
    try:
        raw_exit = read_regular(args.coordinator_exit_status, 4)
        require(len(raw_exit) <= 4 and raw_exit.strip().isdigit())
        original = int(raw_exit)
        require(0 <= original <= 255)
        document['original_exit_status'] = original
        token = os.environ.get('WK_LOCAL_SEND_OBSERVATION_TOKEN', '')
        require(0 < len(token) <= 4096 and all(32 < ord(c) < 127 for c in token))
        ports = [int(p) for p in args.api_ports.split(',')]
        require(len(ports) == len(set(ports)) == 3 and all(1024 <= p <= 65535 for p in ports))
        selected, binding = anchors(args)
        document.update(anchor_count=len(selected), anchors=selected, binding=binding)
        budget = QueryBudget(ports, token)
        document['queries'] = budget.rows
        signal.signal(signal.SIGALRM, expired)
        signal.setitimer(signal.ITIMER_REAL, 8)
        collect(budget, selected)
        document['status'] = 'captured' if all(q['status'] == 'captured' for q in budget.rows) else 'partial'
    except (Unknown, OSError, ValueError, KeyError, TypeError, OverflowError):
        if document['queries']:
            document['status'] = 'partial'
    finally:
        signal.setitimer(signal.ITIMER_REAL, 0)
    try:
        atomic_create(args.output, document)
    except (Unknown, OSError, ValueError):
        pass  # Parent owns the workload exit code and cleanup even on export failure.


if __name__ == '__main__':
    main()
