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
            thread = threading.Thread(target=server.serve_forever, daemon=True)
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

    def test_cross_assignment_sidecar_prevents_http(self):
        self.side['workers'][0]['assignment_id'] = 'another-assignment'
        self.assertEqual(self.capture()['status'],'unknown')
        self.assertEqual(self.calls,[])

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
