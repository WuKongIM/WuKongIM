#!/usr/bin/env python3
"""Black-box postload capture contracts; local HTTP fixtures, no product load."""
import copy
import datetime as dt
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import threading
import time
import unittest

ROOT = Path(os.environ.get('LOCAL_SEND_CAPTURE_REPO', Path(__file__).resolve().parents[2]))
SCRIPT = ROOT / 'scripts/chat-lifecycle/capture-local-send-observations.py'
TOKEN = 'fixture-secret-never-retain'
RUN = 'local-send-capture-fixture'
NOW = dt.datetime.now(dt.timezone.utc)
START = (NOW-dt.timedelta(seconds=1200)).isoformat()
END = NOW.isoformat()
TRACE = '1'*32+'/1'
KEY = dict(message_fingerprint='1'*32, client_seq=1, connect_packet_fingerprint='2'*32)


def legacy_attempt_phases():
    pairs = [('gateway.async_dispatch_wait','enqueue_to_dispatch'),
             ('gateway.messages_send','message_call_to_result'),
             ('gateway.write_sendack','write_frame_callback'),
             ('gateway.session_publication_wait','ready_to_publication_attempt'),
             ('message.send_durable','diagnostic_event_duration')]
    return [dict(observed=i != 4,stage=stage,started_at=START if i != 4 else '0001-01-01T00:00:00Z',
                 duration=700000000 if i == 1 else 0,result='ok' if i != 4 else '',
                 boundary=boundary,end_derived=i != 4) for i,(stage,boundary) in enumerate(pairs)]


class CaptureTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.calls = []
        self.mode = 'ok'
        self.servers = []
        fixture = self
        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def do_POST(self):
                body = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
                fixture.calls.append((self.server.node, self.path, self.headers.get('Authorization'), body))
                if fixture.mode == 'redirect':
                    self.send_response(302)
                    self.send_header('Location', 'http://127.0.0.1:1/never-follow')
                    self.end_headers()
                    return
                if fixture.mode == 'auth':
                    self.send_response(403)
                    self.end_headers()
                    self.wfile.write(TOKEN.encode())
                    return
                if fixture.mode == 'timeout':
                    time.sleep(0.7)
                response = dict(version=1, status='ok', results=[])
                if body.get('keys'):
                    response['node_id'] = self.server.node
                    for key in body['keys']:
                        candidate = dict(token=dict(epoch='3'*32, ordinal=1), phases=legacy_attempt_phases(),
                                         rpc_references=[dict(state='bound', trace_id=TRACE)])
                        if fixture.mode.startswith('preparation'):
                            candidate['phases'] += [dict(observed=True, stage='message.permission_batch', started_at=START, duration=300000000, result='ok', boundary='shared_permission_batch', end_derived=True),
                                                    dict(observed=True, stage='message.pre_append', started_at=START, duration=2000000, result='ok', boundary='shared_pre_append_until_submit', end_derived=True)]
                            phase = candidate['phases'][-1]
                            if fixture.mode == 'preparation-private':
                                phase['raw_uid'] = 'raw-user-fixture'
                            if fixture.mode == 'preparation-unknown':
                                phase['stage'] = 'private-unbounded-stage'
                            if fixture.mode == 'preparation-bool-duration':
                                phase['duration'] = True
                            if fixture.mode == 'preparation-missing-start':
                                del phase['started_at']
                            if fixture.mode == 'preparation-over-cap':
                                candidate['phases'].append(copy.deepcopy(phase))
                        if fixture.mode == 'legacy-duplicate':
                            candidate['phases'][1] = copy.deepcopy(candidate['phases'][0])
                        if fixture.mode == 'legacy-reordered':
                            candidate['phases'][0],candidate['phases'][1] = candidate['phases'][1],candidate['phases'][0]
                        if fixture.mode == 'preparation-duplicate':
                            candidate['phases'][-1] = copy.deepcopy(candidate['phases'][-2])
                        if fixture.mode == 'preparation-missing-legacy':
                            candidate['phases'] = candidate['phases'][:3]+candidate['phases'][5:]
                        if fixture.mode == 'preparation-wrong-boundary':
                            candidate['phases'][-1]['boundary'] = 'message_call_to_result'
                        if fixture.mode == 'preparation-unknown-values':
                            for phase in candidate['phases'][5:]:
                                phase.update(observed=False,started_at='0001-01-01T00:00:00Z',duration=0,result='',end_derived=True)
                        if fixture.mode.startswith('preparation-envelope'):
                            phase = dict(observed=True,stage='message.permission_rpc',started_at=START,duration=250000000,
                                         result='ok',boundary='longest_origin_rpc91',end_derived=True)
                            candidate['phases'].append(phase)
                            if fixture.mode == 'preparation-envelope-unknown':
                                phase.update(observed=False,started_at='0001-01-01T00:00:00Z',duration=0,result='')
                            if fixture.mode == 'preparation-envelope-private':
                                phase['raw_uid'] = 'raw-user-fixture'
                            if fixture.mode == 'preparation-envelope-bool':
                                phase['duration'] = True
                            if fixture.mode == 'preparation-envelope-negative':
                                phase['duration'] = -1
                            if fixture.mode == 'preparation-envelope-wrong-boundary':
                                phase['boundary'] = 'shared_permission_batch'
                            if fixture.mode == 'preparation-envelope-unsupported':
                                phase['stage'] = 'permission.service999'
                            if fixture.mode == 'preparation-envelope-missing':
                                del phase['started_at']
                            if fixture.mode == 'preparation-envelope-over-cap':
                                candidate['phases'].append(copy.deepcopy(phase))
                            if fixture.mode == 'preparation-envelope-reordered':
                                candidate['phases'][-2],candidate['phases'][-1] = candidate['phases'][-1],candidate['phases'][-2]
                        if fixture.mode.startswith('permission'):
                            suffix = key['client_seq']*100+self.server.node*10
                            candidate['permission_rpc_references'] = [dict(state='bound', trace_id='6'*32+'/'+format(suffix+1,'x'))]
                            if fixture.mode == 'permission-many':
                                candidate['rpc_references'] = [dict(state='bound',trace_id='1'*32+'/'+format(suffix+i,'x')) for i in (2,3)]
                                candidate['permission_rpc_references'] += [dict(state='bound',trace_id='6'*32+'/'+format(suffix+4,'x'))]
                            if fixture.mode == 'permission-same-trace':
                                candidate['permission_rpc_references'][0]['trace_id'] = TRACE
                            if fixture.mode == 'permission-overflow':
                                candidate['permission_rpc_overflow'] = True
                            if fixture.mode == 'permission-over-cap':
                                candidate['permission_rpc_references'] *= 3
                            if fixture.mode == 'permission-private':
                                candidate['permission_rpc_references'][0]['raw_uid'] = 'raw-user-fixture'
                            if fixture.mode == 'permission-invalid-state':
                                candidate['permission_rpc_references'][0]['state'] = 'latest'
                            if fixture.mode == 'permission-bool-overflow':
                                candidate['permission_rpc_overflow'] = 1
                            reference_cases = {
                                'permission-ref-bound-empty': ('bound',''),
                                'permission-ref-bound-local': ('bound','local/'+TRACE),
                                'permission-ref-unbound-remote': ('unbound',TRACE),
                                'permission-ref-unbound-empty': ('unbound',''),
                                'permission-ref-disabled-id': ('recorder_disabled',TRACE),
                                'permission-ref-exhausted-id': ('recorder_exhausted',TRACE),
                                'permission-ref-local-id': ('local_untraced',TRACE),
                                'permission-ref-remote-id': ('remote_untraced',TRACE),
                                'permission-ref-zero-request': ('bound','6'*32+'/0'),
                                'permission-ref-valid-unbound': ('unbound','local/'+TRACE),
                                'permission-ref-valid-disabled': ('recorder_disabled',''),
                                'permission-ref-valid-exhausted': ('recorder_exhausted',''),
                                'permission-ref-valid-local': ('local_untraced',''),
                                'permission-ref-valid-remote': ('remote_untraced',''),
                            }
                            if fixture.mode in reference_cases:
                                state, trace = reference_cases[fixture.mode]
                                candidate['permission_rpc_references'] = [dict(state=state,trace_id=trace)]
                        response['results'].append(dict(key=key, status='candidate', candidates=[candidate]))
                else:
                    response['rpc_results'] = [dict(selector=s, status='retained', sample=dict(trace_id=s['trace_id'], side=s['side'], service_id=s['service_id'], at=END, duration=650000000, result='ok', spans=[dict(stage='service_queue', offset=0, duration=620000000, result='ok')])) for s in body['rpc_selectors']]
                if fixture.mode == 'ambiguous':
                    for item in response['results']:
                        item['status'] = 'ambiguous'
                        item['candidates'] *= 2
                if fixture.mode == 'unsupported':
                    response['status'] = 'unsupported'
                    response['results'] = []
                if fixture.mode == 'wrong-key' and response['results']:
                    response['results'][0]['key'] = dict(KEY, client_seq=100)
                if fixture.mode == 'wrong-rpc' and response.get('rpc_results'):
                    response['rpc_results'][0]['sample']['service_id'] = 86
                if fixture.mode == 'unknown-field':
                    response['credential'] = TOKEN
                if fixture.mode == 'version-bool':
                    response['version'] = True
                if fixture.mode == 'node-bool':
                    response['node_id'] = True
                if fixture.mode == 'key-bool' and response['results']:
                    response['results'][0]['key'] = dict(body['keys'][0],client_seq=True)
                if fixture.mode == 'key-float' and response['results']:
                    response['results'][0]['key'] = dict(body['keys'][0],client_seq=float(body['keys'][0]['client_seq']))
                if fixture.mode == 'unused-rpc' and body.get('keys'):
                    response['rpc_results'] = [dict(raw_uid='raw-user-fixture')]
                for row in response.get('rpc_results', []):
                    if fixture.mode == 'unused-sample':
                        row.update(status='not_retained',sample=dict(raw_uid='raw-user-fixture'))
                    if fixture.mode == 'selector-float':
                        row['selector'] = dict(row['selector'],service_id=21.0)
                if fixture.mode == 'phase-private' and response['results']:
                    response['results'][0]['candidates'][0]['phases'][0]['raw_uid'] = 'raw-user-fixture'
                if fixture.mode == 'phase-string' and response['results']:
                    response['results'][0]['candidates'][0]['phases'][0] = 'bad'
                if fixture.mode == 'ref-integer' and response['results']:
                    response['results'][0]['candidates'][0]['rpc_references'] = [42]
                for row in response.get('rpc_results', []):
                    if fixture.mode == 'full-cohort':
                        interval = dict(observed=True,complete=True,offset=0,duration=600000000)
                        native = dict(available=True,**{key:1 for key in ('total_duration','semaphore_wait_duration','wal_queue_wait_duration','mem_table_write_stall_duration','l0_read_amp_write_stall_duration','wal_rotation_duration','commit_wait_duration')})
                        link = dict(cohort_ref=dict(instance='local/'+'4'*32+'/',attempt=1),request_ordinal=1,
                                    **{key:interval for key in ('physical_window','submit_window','build','durable','publish','terminal_wait','finalize')},
                                    timed_sum=600000000,native=native,outcome='committed')
                        row['sample']['commit_cohorts'] = dict(links=[link],dropped=0,owners_dropped=0)
                        row['sample']['parent_trace_id'] = 'local/'+'5'*32+'/1'
                    if fixture.mode == 'spans-string':
                        row['sample']['spans'] = 'oops'
                    if fixture.mode == 'sample-private':
                        row['sample']['raw_uid'] = 'raw-user-fixture'
                    if fixture.mode == 'duration-bool':
                        row['sample']['duration'] = True
                    if fixture.mode == 'cohort-private':
                        row['sample']['commit_cohorts'] = dict(links=[dict(raw_uid='raw-user-fixture')],dropped=0)
                data = json.dumps(response).encode()
                if fixture.mode == 'oversize':
                    data += b' '*(270<<10)
                if fixture.mode == 'malformed':
                    data = b'{'
                self.send_response(200)
                self.send_header('Content-Type', 'application/json')
                self.end_headers()
                try:
                    self.wfile.write(data)
                except (BrokenPipeError, ConnectionResetError):
                    pass
        for i in range(3):
            server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
            server.node = i+1
            server.daemon_threads = True
            self.servers.append(server)
            # Bound fixture teardown latency without changing exporter deadlines.
            thread = threading.Thread(target=server.serve_forever, kwargs={'poll_interval':0.01}, daemon=True)
            thread.start()
            self.addCleanup(server.server_close)
            self.addCleanup(server.shutdown)
        fence = dict(run_hash='sha256:'+hashlib.sha256(RUN.encode()).hexdigest(), assignment_hash='sha256:'+hashlib.sha256(b'assignment-fixture').hexdigest(), generation=1)
        self.start = dict(schema='wukongim.chat_lifecycle.run_start/v1', stage='shakeout', started_at=START, expected_end_at=(NOW+dt.timedelta(days=3)).isoformat(), **fence)
        self.final = dict(schema_version='wukongim/chat-lifecycle-report/v8', kind='final', final=True, continuous=False, **{'continue':False},
                          fence=fence, window=dict(start=START, end=END), thresholds=dict(latency=dict(hot_sendack=dict(p99=600000000), hot_attempt_timeout=2000000000)),
                          verdict=dict(terminal=True, outcome='product_failure', cause='hot_latency'),
                          worker_generations=[dict(worker_index=i,generation=1,snapshot_sequence=8,phase='final') for i in range(3)])
        self.side = dict(schema='wukongim/chat-lifecycle-attempt-link-diagnostics/v2', run_id=RUN, at=END, workers=[])
        for i in range(3):
            record = dict(ordinal=1, key=dict(KEY, client_seq=i+1), kind='sendack_success', attribution='candidate',
                          deadline_after_registration=2000000000, observed_after_registration=700000000, sdk_pending_to_write=1000000,
                          sdk_write_to_observed=699000000, timing_mask=3, observed_at=(NOW-dt.timedelta(seconds=1)).isoformat())
            self.side['workers'].append(dict(worker_id=i,assignment_id='assignment-fixture',generation=1,snapshot_sequence=8,phase='final',
                                            attempt_link_diagnostics=dict(generation=1,dropped=0,records=[],slow_hot_success=dict(limit_nanos=600000000,seen=1,overwritten=0,records=[record]))))
        self.pid = 99999999

    def capture(self, mutate=None, token=TOKEN, ports=None, preexisting=False, prepare=None, timeout=12, expected_original=7):
        if mutate:
            mutate()
        for name, value in [('run-start.json',self.start),('final.json',self.final),('attempt-link-diagnostics.json',self.side)]:
            (self.root/name).write_text(json.dumps(value))
        (self.root/'coordinator-exit-status').write_text('7\n')
        if prepare:
            prepare()
        output = self.root/'observations.json'
        if preexisting:
            output.write_text('ORIGINAL')
        env = dict(os.environ, WK_LOCAL_SEND_OBSERVATION_TOKEN=token)
        command = [sys.executable,str(SCRIPT),'--report-dir',str(self.root),'--run-id',RUN,'--coordinator-pid',str(self.pid),
                   '--coordinator-exit-status',str(self.root/'coordinator-exit-status'),'--api-ports',ports or ','.join(str(s.server_port) for s in self.servers),'--output',str(output)]
        before = time.monotonic()
        result = subprocess.run(command, env=env, capture_output=True, timeout=timeout)
        self.assertNotIn(TOKEN.encode(), result.stdout+result.stderr)
        self.assertEqual(result.returncode, 0, result.stderr.decode())
        if preexisting:
            self.assertEqual(output.read_text(), 'ORIGINAL')
            return None
        raw = output.read_bytes()
        self.assertLessEqual(len(raw), 4<<20)
        self.assertNotIn(TOKEN.encode(), raw)
        self.assertNotIn(b'raw-user-fixture', raw)
        self.assertEqual(output.stat().st_mode & 0o777, 0o600)
        data = json.loads(raw)
        if expected_original is not None:
            self.assertEqual(data['original_exit_status'],expected_original)
        self.assertLess(time.monotonic()-before, 10.5)
        return data

    def test_p999_priority_retention_preserves_gapped_keys_and_rejects_bad_policy(self):
        original = copy.deepcopy(self.side)
        self.final['thresholds']['latency']['hot_sendack']['p999'] = 1000000000
        for case in ['valid', 'unsupported', 'wrong-tail', 'bool-tail', 'bool-seen', 'bool-overwritten', 'duplicate', 'regressing', 'above-seen', 'missing-newest', 'legacy-gap']:
            with self.subTest(case=case):
                self.side = copy.deepcopy(original)
                for worker in self.side['workers']:
                    slow = worker['attempt_link_diagnostics']['slow_hot_success']
                    slow.update(retention_policy='p999_reserve8/v1', tail_limit_nanos=1000000000, seen=17, overwritten=1)
                    template = slow['records'][0]
                    rows = []
                    for ordinal in [1]+list(range(3,18)):
                        row = copy.deepcopy(template)
                        row.update(ordinal=ordinal, key=dict(KEY, client_seq=ordinal+worker['worker_id']*100),
                                   observed_after_registration=1100000000 if ordinal == 1 else 700000000,
                                   sdk_write_to_observed=1099000000 if ordinal == 1 else 699000000)
                        rows.append(row)
                    slow['records'] = rows
                    if case == 'unsupported': slow['retention_policy'] = 'unknown'
                    if case == 'wrong-tail': slow['tail_limit_nanos'] = 600000000
                    if case == 'bool-tail': slow['tail_limit_nanos'] = True
                    if case == 'bool-seen': slow['seen'] = True
                    if case == 'bool-overwritten': slow['overwritten'] = True
                    if case == 'duplicate': rows[1]['ordinal'] = 1
                    if case == 'regressing': rows[1]['ordinal'],rows[2]['ordinal'] = 4,3
                    if case == 'above-seen': rows[-1]['ordinal'] = 18
                    if case == 'missing-newest': rows[-1]['ordinal'] = 16
                    if case == 'legacy-gap':
                        del slow['retention_policy'],slow['tail_limit_nanos']
                data = self.capture()
                if case == 'valid':
                    self.assertEqual(data['status'],'captured')
                    self.assertEqual(data['anchor_count'],48)
                    self.assertEqual(sum(a['observed_after_registration']>1000000000 for a in data['anchors']),3)
                    self.assertEqual(data['binding']['anchor_retention_policies'],[
                        dict(worker_id=i,policy='p999_reserve8/v1',tail_limit_nanos=1000000000) for i in range(3)])
                    self.assertLessEqual(len(self.calls),18)
                else:
                    self.assertEqual(data['status'],'unknown')
                    self.assertEqual(self.calls,[])
                self.calls.clear()
                (self.root/'observations.json').unlink()

    def test_cross_assignment_sidecar_prevents_http(self):
        self.side['workers'][0]['assignment_id'] = 'another-assignment'
        self.assertEqual(self.capture()['status'],'unknown')
        self.assertEqual(self.calls,[])

    def test_falsy_policy_types_are_not_legacy_absence(self):
        original = copy.deepcopy(self.side)
        for invalid in [False,0,None,[],{}]:
            with self.subTest(policy=invalid):
                (self.root/'observations.json').unlink(missing_ok=True)
                self.calls.clear()
                self.side = copy.deepcopy(original)
                for worker in self.side['workers']:
                    worker['attempt_link_diagnostics']['slow_hot_success']['retention_policy'] = invalid
                data = self.capture()
                self.assertEqual(data['status'],'unknown')
                self.assertEqual(self.calls,[])
                self.calls.clear()
                (self.root/'observations.json').unlink()

    def test_duplicate_native_keys_and_boolean_ids_are_not_fences(self):
        def duplicate():
            path = self.root/'run-start.json'
            path.write_text(path.read_text().replace('"generation": 1','"generation": 2, "generation": 1'))
        self.assertEqual(self.capture(prepare=duplicate)['status'],'unknown')
        self.assertEqual(self.calls,[])
        (self.root/'observations.json').unlink()
        self.side['workers'][0]['worker_id'] = False
        self.assertEqual(self.capture()['status'],'unknown')
        self.assertEqual(self.calls,[])

    def test_boolean_response_version_is_unknown(self):
        self.mode = 'version-bool'
        self.assertEqual(self.capture()['status'],'partial')

    def test_typed_echoes_and_unused_reply_branches_fail_closed(self):
        for mode in ['node-bool','key-bool','key-float','unused-rpc','unused-sample','selector-float']:
            with self.subTest(mode=mode):
                self.mode = mode
                self.assertEqual(self.capture()['status'],'partial')
                self.calls.clear()
                (self.root/'observations.json').unlink()

    def test_anchor_extra_fields_and_typed_native_fence_are_rejected(self):
        self.side['workers'][0]['attempt_link_diagnostics']['slow_hot_success']['records'][0]['raw_uid'] = 'raw-user-fixture'
        self.assertEqual(self.capture()['status'],'unknown')
        self.assertEqual(self.calls,[])
        (self.root/'observations.json').unlink()
        del self.side['workers'][0]['attempt_link_diagnostics']['slow_hot_success']['records'][0]['raw_uid']
        self.final['fence']['generation'] = True
        self.assertEqual(self.capture()['status'],'unknown')
        self.assertEqual(self.calls,[])

    def test_unused_native_verdict_fields_are_not_exported(self):
        self.final['verdict']['raw_uid'] = 'raw-user-fixture'
        self.assertEqual(self.capture()['status'],'captured')

    def test_actual_cohort_namespace_and_local_parent_are_preserved(self):
        self.mode = 'full-cohort'
        data = self.capture()
        self.assertEqual(data['status'],'captured')
        self.assertIn('wal_queue_wait_duration',json.dumps(data))
        self.assertIn('terminal_wait',json.dumps(data))
        self.assertIn('local/'+'4'*32+'/',json.dumps(data))

    def test_nonregular_inputs_fail_immediately_without_http(self):
        for name in ['coordinator-exit-status','run-start.json','final.json','attempt-link-diagnostics.json']:
            def fifo():
                path = self.root/name
                path.unlink()
                os.mkfifo(path)
            with self.subTest(name=name):
                try:
                    data = self.capture(prepare=fifo,timeout=1.5,expected_original=None)
                    self.assertEqual(data['status'],'unknown')
                    self.assertEqual(self.calls,[])
                finally:
                    (self.root/'observations.json').unlink(missing_ok=True)
                    (self.root/name).unlink(missing_ok=True)
        def symlink():
            original = self.root/'run-start.json'
            original.rename(self.root/'saved-start')
            original.symlink_to(self.root/'saved-start')
        self.assertEqual(self.capture(prepare=symlink)['status'],'unknown')
        self.assertEqual(self.calls,[])

    def test_malformed_nested_evidence_is_unknown_with_artifact(self):
        for mode in ['phase-private','phase-string','ref-integer','spans-string','sample-private','duration-bool','cohort-private']:
            with self.subTest(mode=mode):
                self.mode = mode
                data = self.capture()
                self.assertEqual(data['status'],'partial')
                self.assertEqual(data['causal_verdict'],'unverified')
                self.calls.clear()
                (self.root/'observations.json').unlink()

    def test_permission_refs_keep_service_identity_and_share_total_budget(self):
        for mode in ['permission','permission-same-trace','permission-many']:
            with self.subTest(mode=mode):
                self.mode = mode
                data = self.capture()
                self.assertEqual(data['status'],'captured')
                selectors = [s for q in data['queries'] for s in q['request'].get('rpc_selectors',[])]
                identities = {(s['service_id'],s['trace_id']) for s in selectors}
                self.assertEqual({s[0] for s in identities},{21,91})
                self.assertLessEqual(len(identities),16)
                self.assertLessEqual(len(self.calls),18)
                if mode == 'permission-many':
                    self.assertEqual(sum(s[0]==21 for s in identities),8)
                    self.assertEqual(sum(s[0]==91 for s in identities),8)
                if mode == 'permission-same-trace':
                    self.assertIn((21,TRACE),identities)
                    self.assertIn((91,TRACE),identities)
                self.calls.clear()
                (self.root/'observations.json').unlink()

    def test_permission_missing_overflow_and_invalid_refs_are_not_append(self):
        for mode in ['permission-overflow','permission-over-cap','permission-private','permission-invalid-state','permission-bool-overflow']:
            with self.subTest(mode=mode):
                self.mode = mode
                data = self.capture()
                selectors = [s for q in data['queries'] for s in q['request'].get('rpc_selectors',[])]
                if mode == 'permission-overflow':
                    self.assertEqual(data['status'],'captured')
                    self.assertTrue(selectors)
                    self.assertTrue(all(s['service_id']==21 for s in selectors))
                else:
                    self.assertEqual(data['status'],'partial')
                self.assertNotIn('raw-user-fixture',json.dumps(data))
                self.calls.clear()
                (self.root/'observations.json').unlink()

    def test_permission_reference_state_identity_pairs_fail_closed(self):
        for suffix in ['bound-empty','bound-local','unbound-remote','unbound-empty','disabled-id','exhausted-id','local-id','remote-id','zero-request']:
            with self.subTest(case=suffix):
                self.calls.clear()
                (self.root/'observations.json').unlink(missing_ok=True)
                self.mode = 'permission-ref-'+suffix
                data = self.capture()
                self.assertEqual(data['status'],'partial')
                self.assertTrue(all(q['status'] == 'unknown' for q in data['queries']))
                self.calls.clear()
                (self.root/'observations.json').unlink()

    def test_permission_reference_explicit_missing_and_unbound_stay_compatible(self):
        for suffix in ['unbound','disabled','exhausted','local','remote']:
            with self.subTest(case=suffix):
                self.mode = 'permission-ref-valid-'+suffix
                data = self.capture()
                self.assertEqual(data['status'],'captured')
                selectors = [s for q in data['queries'] for s in q['request'].get('rpc_selectors',[])]
                self.assertTrue(selectors)
                self.assertTrue(all(s['service_id']==21 for s in selectors))
                self.calls.clear()
                (self.root/'observations.json').unlink()

    def p95_native_fixture(self):
        self.final.update(schema_version='wukongim/chat-lifecycle-report/v9',
                          threshold_version='wukongim/chat-lifecycle-thresholds/v3',
                          profile='local',mode='soak',stage='shakeout',
                          qualification=dict(scope='local_diagnostic',external_linux_evidence_required=False))
        self.final['thresholds']['latency']['hot_sendack'] = dict(p95=600000000,p999=1000000000)
        for worker in self.side['workers']:
            worker['attempt_link_diagnostics']['slow_hot_success']['percentile'] = 95

    def paired_p95_native_fixture(self):
        self.p95_native_fixture()
        self.final.update(schema_version='wukongim/chat-lifecycle-report/v10',
                          threshold_version='wukongim/chat-lifecycle-thresholds/v4')
        self.final['thresholds']['latency'].update(hot_sendack=dict(p95=600000000),
                                                  sync=dict(p95=1000000000,p999=3000000000),
                                                  cold=dict(p99=2000000000,p999=5000000000))

    def test_paired_p95_native_contract_roundtrip_and_bounded_http(self):
        self.paired_p95_native_fixture()
        data = self.capture()
        self.assertEqual(data['status'],'captured')
        self.assertEqual(data['anchor_count'],3)
        self.assertLessEqual(len(self.calls),18)
        self.assertEqual(data['original_exit_status'],7)
        self.assertEqual(data['causal_verdict'],'unverified')

    def test_paired_p95_rejects_half_policy_false_types_and_stale_tail(self):
        for mode in ['old_report','old_threshold','formal','sync99','sync_both','missing_sync','float_sync95','bool_sync95','zero_sync95','null_sync95','wrong_sync_tail','bool_sync_tail','hot_tail','cold95','reserved_side']:
            with self.subTest(mode=mode):
                self.calls.clear()
                (self.root/'observations.json').unlink(missing_ok=True)
                self.paired_p95_native_fixture()
                latency=self.final['thresholds']['latency']
                if mode=='old_report': self.final['schema_version']='wukongim/chat-lifecycle-report/v9'
                if mode=='old_threshold': self.final['threshold_version']='wukongim/chat-lifecycle-thresholds/v3'
                if mode=='formal': self.final['profile']='formal'
                if mode=='sync99': latency['sync']=dict(p99=1000000000,p999=3000000000)
                if mode=='sync_both': latency['sync']['p99']=1000000000
                if mode=='missing_sync': del latency['sync']
                if mode in ('float_sync95','bool_sync95','zero_sync95','null_sync95'):
                    latency['sync']['p95']={'float_sync95':1e9,'bool_sync95':True,'zero_sync95':0,'null_sync95':None}[mode]
                if mode=='wrong_sync_tail': latency['sync']['p999']=2000000000
                if mode=='bool_sync_tail': latency['sync']['p999']=True
                if mode=='hot_tail': latency['hot_sendack']['p999']=1000000000
                if mode=='cold95': latency['cold']=dict(p95=2000000000,p999=5000000000)
                if mode=='reserved_side':
                    for w in self.side['workers']:
                        w['attempt_link_diagnostics']['slow_hot_success'].update(retention_policy='p999_reserve8/v1',tail_limit_nanos=1000000000)
                data=self.capture()
                self.assertEqual(data['status'],'unknown')
                self.assertEqual(self.calls,[])
                for w in self.side['workers']:
                    slow=w['attempt_link_diagnostics']['slow_hot_success']
                    slow.pop('retention_policy',None);slow.pop('tail_limit_nanos',None)

    def test_p95_native_report_uses_explicit_new_contract_and_same_bounded_capture(self):
        self.p95_native_fixture()
        data = self.capture()
        self.assertEqual(data['status'],'captured')
        self.assertEqual(data['anchor_count'],3)
        self.assertLessEqual(len(self.calls),18)
        self.assertEqual(data['original_exit_status'],7)
        self.assertEqual(data['causal_verdict'],'unverified')

    def test_p95_native_contract_rejects_mixed_or_relabelled_legacy_evidence(self):
        for mode in ['both','missing95','wrong_threshold_version','legacy_header','formal_scope','cold95','side99','side_missing','wrong_tail','wrong_first','float95','float999','float_first']:
            with self.subTest(mode=mode):
                self.calls.clear()
                (self.root/'observations.json').unlink(missing_ok=True)
                self.p95_native_fixture()
                hot = self.final['thresholds']['latency']['hot_sendack']
                if mode=='both': hot['p99']=600000000
                if mode=='missing95': del hot['p95']
                if mode=='wrong_threshold_version': self.final['threshold_version']='wukongim/chat-lifecycle-thresholds/v2'
                if mode=='legacy_header': self.final['schema_version']='wukongim/chat-lifecycle-report/v8'
                if mode=='formal_scope': self.final['profile']='formal'
                if mode=='cold95': self.final['thresholds']['latency']['cold']=dict(p95=2000000000,p999=5000000000)
                if mode=='wrong_tail': hot['p999']=2000000000
                if mode=='wrong_first': self.final['thresholds']['latency']['hot_attempt_timeout']=1000000000
                if mode=='float95': hot['p95']=600000000.0
                if mode=='float999': hot['p999']=1e9
                if mode=='float_first': self.final['thresholds']['latency']['hot_attempt_timeout']=2e9
                for worker in self.side['workers']:
                    slow = worker['attempt_link_diagnostics']['slow_hot_success']
                    if mode=='side99': slow['percentile']=99
                    if mode=='side_missing': del slow['percentile']
                data = self.capture()
                self.assertEqual(data['status'],'unknown')
                self.assertEqual(self.calls,[])
                # No malformed case may poison the next fresh fixture.
                self.final['thresholds']['latency'].pop('cold',None)
                self.final['thresholds']['latency']['hot_attempt_timeout']=2000000000

    def test_exact_opaque_postload_join(self):
        data = self.capture()
        self.assertEqual(data['status'],'captured')
        self.assertEqual(data['anchor_count'],3)
        self.assertEqual(len(self.calls),6)
        self.assertTrue(all(c[1]=='/debug/diagnostics/send-attempts/query' and c[2]=='Bearer '+TOKEN for c in self.calls))
        rpc = [c for c in self.calls if c[3].get('rpc_selectors')]
        self.assertTrue(rpc)
        self.assertTrue(all(s['service_id']==21 for c in rpc for s in c[3]['rpc_selectors']))
        self.assertIn('service_queue',json.dumps(data))
        self.assertEqual(data['causal_verdict'],'unverified')

    def test_preparation_phases_preserve_legacy_and_exact_bounded_facts(self):
        for mode,count in [('ok',5),('preparation',7)]:
            with self.subTest(mode=mode):
                self.mode = mode
                data = self.capture()
                self.assertEqual(data['status'],'captured')
                summaries = [q for q in data['queries'] if q['request'].get('keys')]
                for query in summaries:
                    for row in query['response']['results']:
                        phases = row['candidates'][0]['phases']
                        self.assertEqual(len(phases),count)
                        if mode == 'preparation':
                            self.assertEqual(phases[-2]['duration'],300000000)
                            self.assertEqual(phases[-2]['boundary'],'shared_permission_batch')
                            self.assertEqual(phases[-1]['boundary'],'shared_pre_append_until_submit')
                self.assertLessEqual(len(self.calls),18)
                self.assertEqual(data['causal_verdict'],'unverified')
                self.calls.clear()
                (self.root/'observations.json').unlink()

    def test_preparation_private_unknown_or_invalid_children_remain_unknown(self):
        for mode in ['preparation-private','preparation-unknown','preparation-bool-duration',
                     'preparation-missing-start','preparation-over-cap']:
            with self.subTest(mode=mode):
                self.mode = mode
                data = self.capture()
                self.assertEqual(data['status'],'partial')
                self.assertEqual(data['causal_verdict'],'unverified')
                self.assertNotIn('raw-user-fixture',json.dumps(data))
                self.calls.clear()
                (self.root/'observations.json').unlink()

    def test_complete_phase_order_and_corresponding_boundaries_are_required(self):
        for mode in ['legacy-duplicate','legacy-reordered','preparation-duplicate',
                     'preparation-missing-legacy','preparation-wrong-boundary']:
            with self.subTest(mode=mode):
                self.mode = mode
                try:
                    data = self.capture()
                    self.assertEqual(data['status'],'partial')
                    self.assertEqual(data['causal_verdict'],'unverified')
                finally:
                    self.calls.clear()
                    (self.root/'observations.json').unlink(missing_ok=True)

    def test_complete_unknown_preparation_stays_unknown_without_filling(self):
        self.mode = 'preparation-unknown-values'
        data = self.capture()
        self.assertEqual(data['status'],'captured')
        for query in data['queries']:
            if query['request'].get('keys'):
                for row in query['response']['results']:
                    phases = row['candidates'][0]['phases'][5:]
                    self.assertEqual(len(phases),2)
                    self.assertTrue(all(not p['observed'] and p['duration'] == 0 and p['result'] == '' for p in phases))

    def test_permission_envelope_eighth_phase_preserves_exact_and_unknown(self):
        for mode in ['preparation-envelope','preparation-envelope-unknown']:
            with self.subTest(mode=mode):
                self.mode = mode
                try:
                    data = self.capture()
                    self.assertEqual(data['status'],'captured')
                    for query in data['queries']:
                        if query['request'].get('keys'):
                            for row in query['response']['results']:
                                self.assertEqual(len(row['candidates'][0]['phases']),8)
                                phase = row['candidates'][0]['phases'][-1]
                                self.assertEqual(phase['stage'],'message.permission_rpc')
                                self.assertEqual(phase['boundary'],'longest_origin_rpc91')
                                self.assertEqual(phase['observed'],mode == 'preparation-envelope')
                                self.assertEqual(phase['duration'],250000000 if mode == 'preparation-envelope' else 0)
                    self.assertLessEqual(len(self.calls),18)
                    self.assertEqual(data['causal_verdict'],'unverified')
                finally:
                    self.calls.clear()
                    (self.root/'observations.json').unlink(missing_ok=True)

    def test_permission_envelope_malformed_phase_is_unknown(self):
        for suffix in ['private','bool','negative','wrong-boundary','unsupported','missing','over-cap','reordered']:
            with self.subTest(case=suffix):
                self.mode = 'preparation-envelope-'+suffix
                try:
                    data = self.capture()
                    self.assertEqual(data['status'],'partial')
                    self.assertEqual(data['causal_verdict'],'unverified')
                    self.assertNotIn('raw-user-fixture',json.dumps(data))
                    self.assertLessEqual(len(self.calls),18)
                finally:
                    self.calls.clear()
                    (self.root/'observations.json').unlink(missing_ok=True)

    def test_invalid_fences_prevent_all_http(self):
        changes = [lambda: self.final.update(final=False),lambda: self.final['fence'].update(generation=2),
                   lambda: self.final['fence'].update(assignment_hash='sha256:'+'b'*64),lambda: self.side.update(run_id='other'),
                   lambda: self.side['workers'][0].update(phase='running'),lambda: self.side['workers'][0].update(snapshot_sequence=7),
                   lambda: self.side['workers'][0]['attempt_link_diagnostics']['slow_hot_success'].update(limit_nanos=400000000)]
        original = copy.deepcopy((self.start,self.final,self.side))
        for change in changes:
            with self.subTest(change=change):
                self.start,self.final,self.side = copy.deepcopy(original)
                self.assertEqual(self.capture(change)['status'],'unknown')
                self.assertEqual(self.calls,[])
                (self.root/'observations.json').unlink()

    def test_active_coordinator_prevents_all_http(self):
        self.pid = os.getpid()
        self.assertEqual(self.capture()['status'],'unknown')
        self.assertEqual(self.calls,[])

    def test_missing_token_and_invalid_ports_do_not_query(self):
        for token,ports in [('',None),(TOKEN,'localhost:80'),(TOKEN,'80,80,81')]:
            with self.subTest(ports=ports):
                self.assertEqual(self.capture(token=token,ports=ports)['status'],'unknown')
                self.assertEqual(self.calls,[])
                (self.root/'observations.json').unlink()

    def test_stale_unknown_and_late_anchors_not_selected(self):
        for w in self.side['workers']:
            w['attempt_link_diagnostics']['slow_hot_success']['records'][0]['observed_at'] = (NOW-dt.timedelta(seconds=301)).isoformat()
        self.assertEqual(self.capture()['anchor_count'],0)
        self.assertEqual(self.calls,[])

    def test_failures_are_unknown_never_follow_redirect_or_expose_body(self):
        for mode in ['redirect','auth','oversize','malformed','wrong-key','unknown-field','timeout']:
            with self.subTest(mode=mode):
                self.mode = mode
                data = self.capture()
                self.assertEqual(data['status'],'partial')
                self.assertLessEqual(len(self.calls),3)
                self.calls.clear()
                (self.root/'observations.json').unlink()

    def test_unsupported_and_ambiguous_are_not_claimed_exact(self):
        for mode in ['unsupported','ambiguous','wrong-rpc']:
            with self.subTest(mode=mode):
                self.mode = mode
                data = self.capture()
                self.assertEqual(data['causal_verdict'],'unverified')
                self.assertIn('unknown' if mode=='wrong-rpc' else mode,json.dumps(data))
                self.calls.clear()
                (self.root/'observations.json').unlink()

    def test_existing_output_never_overwritten_or_queried(self):
        self.capture(preexisting=True)
        self.assertEqual(self.calls,[])


if __name__ == '__main__':
    result = unittest.main(verbosity=2,exit=False).result
    artifact = os.environ.get('LOCAL_SEND_CAPTURE_TEST_ARTIFACT')
    if artifact:
        Path(artifact).write_text(json.dumps(dict(tests_run=result.testsRun,failures=len(result.failures),errors=len(result.errors),fixture_only=True)))
    sys.exit(0 if result.wasSuccessful() else 1)
