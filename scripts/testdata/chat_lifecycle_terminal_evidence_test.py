#!/usr/bin/env python3
"""Process-level evidence capture tests. Every SSH invocation is a fixture."""
import calendar
import copy
from decimal import Decimal, ROUND_HALF_UP
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import shlex
import subprocess
import sys
import tempfile
import time
import unittest
from unittest import mock

sys.dont_write_bytecode = True
ROOT = Path(__file__).resolve().parents[2]
RESULTS = []
START = "2026-10-21T17:05:48.909224454Z"
END = "2026-10-21T17:06:50.121277292Z"
RUN = "terminal-fixture"
FAMILIES = ["presence", "delivery", "up", "runtime", "channel_worker", "process", "counters", "go"]
NAMES = {"up": "up", "runtime": "wukongim_runtime_pool_queue_depth", "channel_worker": "wukongim_channel_worker_queue_depth", "process": "wukongim_process_cpu_jiffies_total", "counters": "wukongim_channelv2_meta_created_total", "go": "go_goroutines"}
REGISTERED_RESOURCES = {
    "process": ["wukongim_process_cpu_jiffies_total", "process_cpu_seconds_total", "process_start_time_seconds", "process_resident_memory_bytes"],
    "counters": ["wukongim_channelv2_meta_created_total", "wukongim_channel_meta_created_total", "wukongim_channelv2_activation_rejected_total", "wukongim_channel_activation_rejected_total"],
    "go": ["go_goroutines", "go_memstats_heap_alloc_bytes", "go_gc_duration_seconds_count", "go_sched_latencies_seconds_bucket", "go_sched_latencies_seconds_sum", "go_sched_latencies_seconds_count"],
}


def report(stage="rehearsal"):
    return {"schema_version": "wukongim/chat-lifecycle-report/v7", "threshold_version": "wukongim/chat-lifecycle-thresholds/v2",
            "qualification": {"scope": "rehearsal" if stage=="rehearsal" else "formal_protocol", "external_linux_evidence_required": True},
            "stage": "formal" if stage=="capacity" else stage, "mode": "capacity" if stage=="capacity" else "soak",
            "kind": "final", "final": True, "continue": False, "fence": {"run_hash": "sha256:"+hashlib.sha256(RUN.encode()).hexdigest(), "generation": 1},
            "window": {"start": START, "end": END}, "verdict": {"terminal": True, "outcome": "product_failure", "cause": "message_loss"},
            "capacity": dict(attempted=False, completed=False, lower_bound=False, maximum_passing_rate=0, first_failing_rate=0, recovery_passed=False),
            "worker_generations": [{"worker_index": i, "generation": 1, "snapshot_sequence": 13, "phase": "final"} for i in range(3)]}


def worker():
    return {"schema": "wukongim/chat-lifecycle-worker-evidence/v1", "run_id": RUN, "at": END,
            "workers": [{"worker_id": i, "generation": 1, "snapshot_sequence": 13,
                         "evidence": {"classification": "product_failure", "classes": [{"class": 3, "count": 1,
                             "first": [{"stage": 5, "code": 11, "sample_index": 234, "fingerprint": [1]*16, "value": 60000000000}], "last": None}]}} for i in range(3)]}


def status():
    connection = dict(target=1, online=0, starting=0, closing=0, traffic_ready=0)
    close = dict(expired=0, heartbeat_failed=0, remote_terminal=0, read_failed=0, generation_stop=1, explicit_logout=0, transport_close_failed=0)
    harness = dict(failures=0, command_saturation=0, offered_underdelivery=0, planned_cancellations=0, lifecycle_approval_rejections=dict.fromkeys(["missing_timer","invalid_shape","timer_fence","activity_fence","invalidated","fence_exhausted","deadline","replay_expired","replay_mismatch"],0), drain_timed_out=False, unexpected_exit=False)
    return dict(schema="wukongim/chat-lifecycle-diagnostic-status/v1", run_id=RUN, state="running", stage="measured", started_at=START, updated_at=END, cut="terminal", totals=dict(connection, target=3), close_reasons=dict(close, generation_stop=3), harness=harness,
                workers=[dict(worker_id=i, phase="final", snapshot_sequence=13, connections=connection, close_reasons=close, harness=harness) for i in range(3)], recent_events=None)


def metric_family(query):
    if 'runtime_.*|channel_worker' in query:return 'combined'
    if query in ('up','{__name__="up"}'):return 'up'
    for fragment,family in [('presence_endpoint_lookup_','presence'),('delivery_','delivery'),('runtime_','runtime'),('channel_worker_','channel_worker'),('process_','process'),('meta_created','counters'),('metadata_create_total','counters'),('go_','go')]:
        if fragment in query:return family
    raise AssertionError('unknown fixture metric query')


def ssh_calls(path):
    """Decode commands actually received by SSH, never planned or expected calls."""
    calls=[]
    for raw in path.read_bytes().split(b'\0'):
        if not raw:continue
        command=raw.decode()
        if 'worker-evidence.json' in command or 'diagnostic-status.json' in command:
            filename='worker-evidence.json' if 'worker-evidence.json' in command else 'diagnostic-status.json'
            stage=re.search(r'/reports/(rehearsal|formal|capacity)/',command).group(1)
            calls.append(dict(kind=stage+'/'+filename))
        elif 'api/v1/query_range' in command:
            words=shlex.split(command)
            if words[:4]==['bash','-o','pipefail','-c']:words=shlex.split(words[4])
            parameters=dict(words[i+1].split('=',1) for i,x in enumerate(words) if x=='--data-urlencode')
            calls.append(dict(kind='range-'+metric_family(parameters['query']),query=parameters['query'],**{key:parameters[key] for key in ['start','end','step']}))
        else:calls.append(dict(kind='targets' if 'api/v1/targets' in command else 'other'))
    return calls


def prepare_ssh(directory, cfg, end):
    """Prepare response bytes once; each real SSH process clips the requested bytes."""
    responses=directory/'responses';responses.mkdir();routes=[];outcomes=[]
    names={'presence':'wukongim_presence_endpoint_lookup_total','delivery':'wukongim_delivery_recipient_worker_process_total',**NAMES,'combined':'up'}
    modes={**cfg['document_modes'],**{'range-'+key:value for key,value in cfg['metric_modes'].items()}}
    bodies={key:Path(path).read_bytes() for key,path in cfg['files'].items()}
    boundary_bounds={}
    for family,name in names.items():
        key='range-'+family;mode=modes.get(key,'normal')
        result={'metric':{'__name__':name},'values':[[1792602410,'1']]}
        data={'status':'success','data':{'resultType':'matrix','result':[result]}}
        if mode=='registered':
            data['data']['result']=[{'metric':{'__name__':metric,'job':'wukongim'},'values':[[1792602400,'1'],[1792602410,'2']]} for metric in REGISTERED_RESOURCES[family]]
        if mode in ('boundary-start','boundary-end','before-start','after-end','sub-millisecond'):
            # Independent fixture time plus exact actual-command matching prevents
            # a wrong query from receiving a conveniently prepared boundary value.
            second,fraction=end.removesuffix('Z').split('.')
            epoch=calendar.timegm(time.strptime(second,'%Y-%m-%dT%H:%M:%S'))
            finish=(Decimal(epoch)+Decimal('0.'+fraction)).quantize(Decimal('.001'),rounding=ROUND_HALF_UP)
            start=finish-900
            stamp={'boundary-start':start,'boundary-end':finish,'before-start':start-Decimal('.001'),'after-end':finish+Decimal('.001'),'sub-millisecond':start+Decimal('.0001')}[mode]
            result['values']=[[float(stamp),'1']]
            boundary_bounds[key]=(str(start),str(finish))
        if mode=='wrong-type':data['data']['resultType']='vector'
        if mode=='wrong-family':result['metric']['__name__']='private_metric'
        if mode=='empty':data['data']['result']=[]
        if mode=='error':data={'status':'error','error':'secret-canary-do-not-publish'}
        if mode=='large':result['metric']['fixture_padding']='x'*900000
        if mode=='oversized':result['metric']['fixture_padding']='x'*(4194304+10)
        bodies[key]=json.dumps(data).encode()
        if family=='combined' and cfg['combined_oversized']:bodies[key]=b'x'*4194305
    for key,body in bodies.items():
        mode=modes.get(key,'normal');document=key.endswith('.json');name=key.replace('/','-')
        if document:routes.append('*'+key+'*) kind='+name+' ;;')
        if document and mode.startswith('boundary-'):body+=b' '*max(0,int(mode.removeprefix('boundary-'))-len(body))
        if mode=='malformed':body=b'not-json-secret-canary-do-not-publish'
        if document and mode=='oversized':body=b'x'*1048577
        if document and mode=='empty':body=b''
        (responses/name).write_bytes(body)
        code={'missing':3,'unavailable':255,'partial':255,'timeout':124}.get(mode,0)
        action='code='+str(code)
        if mode=='unavailable':action="printf '%s' 'secret-canary-do-not-publish' >&2; exit 255"
        elif mode in ('missing','timeout'):action='exit '+str(code)
        if key in boundary_bounds:
            start,finish=boundary_bounds[key]
            expected=' --data-urlencode start='+start+' --data-urlencode end='+finish+' --data-urlencode step=15 '
            action='case "$command" in *'+shlex.quote(expected)+'*) ;; *) exit 99 ;; esac; '+action
        outcomes.append(name+') '+action+' ;;')
    return r'''#!/bin/sh
command=""
for arg do command="$arg"; done
printf '%s\0' "$command" >>"$TERMINAL_FIXTURE_COMMANDS"
case "$command" in
'''+ '\n'.join(routes)+r'''
 *api/v1/query_range*)
  case "$command" in
   *'runtime_.*|channel_worker'*) kind=range-combined ;;
   *presence_endpoint_lookup_*) kind=range-presence ;;
   *delivery_*) kind=range-delivery ;;
   *runtime_*) kind=range-runtime ;;
   *channel_worker_*) kind=range-channel_worker ;;
   *process_*) kind=range-process ;;
   *meta_created*|*metadata_create_total*) kind=range-counters ;;
   *go_*) kind=range-go ;;
   *query=up*|*'__name__="up"'*) kind=range-up ;;
   *) exit 99 ;;
  esac ;;
 *api/v1/targets*) printf '%s' '{"status":"success","data":{"activeTargets":[]}}'; exit 0 ;;
 *) printf 'fixture terminal capture\n'; exit 0 ;;
esac
case "$kind" in
'''+ '\n'.join(outcomes)+r'''
 *) exit 99 ;;
esac
rest=${command##*head -c }
limit=${rest%%[!0-9]*}
case "$limit" in ''|*[!0-9]*) exit 98 ;; esac
head -c "$limit" "$TERMINAL_FIXTURE_RESPONSES/$kind"
exit "$code"
'''

class CollectorTests(unittest.TestCase):
    def run_case(self, label, stage="rehearsal", mutation=None, report_mutation=None, document_modes=None, metric_modes=None, capacity=False, combined_oversized=False, end=END, capacity_end=END):
        with tempfile.TemporaryDirectory(prefix="wk-terminal-fixture-") as directory:
            d=Path(directory);bin=d/'bin';bin.mkdir();output=d/'final'/'evidence';output.parent.mkdir()
            reports={stage:report(stage)}
            reports[stage]['window']['end']=end
            if capacity:
                reports['capacity']=report('capacity');reports['capacity']['window']['end']=capacity_end
                reports['capacity']['capacity']['attempted']=True
                reports[stage]['verdict'].update(outcome='pass',cause='completed')
            if report_mutation:report_mutation(reports[stage])
            files={}
            for current,doc in reports.items():
                target=output.parent/('final.json' if stage=='rehearsal' else current+'/final.json')
                target.parent.mkdir(parents=True,exist_ok=True);target.write_text(json.dumps(doc))
                for name,value in [('worker-evidence.json',worker()),('diagnostic-status.json',status())]:
                    value['at' if name=='worker-evidence.json' else 'updated_at']=doc['window'].get('end',end)
                    if mutation:mutation(name,value)
                    p=d/(current+'-'+name);p.write_text(json.dumps(value));files[current+'/'+name]=str(p)
            cfg=dict(files=files,calls=str(d/'calls'),document_modes=document_modes or {},metric_modes=metric_modes or {},combined_oversized=combined_oversized)
            for name,body in [('ssh',prepare_ssh(d,cfg,end)),('timeout','#!/bin/sh\nshift\nexec "$@"\n')]:
                p=bin/name;p.write_text(body);p.chmod(0o700)
            env=dict(os.environ,PATH=str(bin)+os.pathsep+os.environ['PATH'],TERMINAL_FIXTURE_COMMANDS=str(d/'commands'),TERMINAL_FIXTURE_RESPONSES=str(d/'responses'),WK_CLOUD_SSH_CONFIG=str(d/'ssh-config'),WK_CLOUD_SERVICE1_IP='fixture-1',WK_CLOUD_SERVICE2_IP='fixture-2',WK_CLOUD_SERVICE3_IP='fixture-3')
            cmd=['bash',str(ROOT/'scripts/chat-lifecycle/collect-terminal-evidence.sh'),stage,str(output)]
            run=subprocess.run(cmd,env=env,cwd=ROOT,capture_output=True,timeout=35)
            self.assertNotIn(b'secret-canary',run.stdout+run.stderr)
            manifest=json.loads((output/'manifest.json').read_text())
            self.assertNotIn('secret-canary',json.dumps(manifest))
            calls=ssh_calls(d/'commands')
            retained={str(p.relative_to(output)):p.read_bytes() for p in output.rglob('*') if p.is_file()}
            RESULTS.append(dict(case=label,exit_code=run.returncode,manifest=manifest,calls=calls,files=[dict(path=p,bytes=len(b),sha256=hashlib.sha256(b).hexdigest()) for p,b in sorted(retained.items())]))
            return run.returncode,manifest,calls,retained,files

    def capture(self, manifest, name):
        found=[x for x in manifest['captures'] if x['name']==name]
        self.assertEqual(len(found),1,'missing capture '+name)
        return found[0]

    def test_rehearsal_retains_exact_terminal_documents_and_failure_window(self):
        rc,m,calls,files,_=self.run_case('rehearsal')
        self.assertEqual(rc,0);self.assertTrue(m['complete'])
        for name in ['worker-evidence.json','diagnostic-status.json']:
            path='workload/rehearsal/'+name;self.assertIn(path,files)
            expected=json.dumps(worker() if name.startswith('worker') else status()).encode()
            self.assertEqual(files[path],expected)
            row=self.capture(m,'rehearsal-'+name.removesuffix('.json'))
            self.assertEqual(row['sha256'],hashlib.sha256(expected).hexdigest())
        ranges=[c for c in calls if c['kind'].startswith('range-')]
        self.assertEqual([c['kind'] for c in ranges],['range-'+x for x in FAMILIES])
        for c in ranges:
            self.assertEqual(c['start'],'1792601510.121');self.assertEqual(c['end'],'1792602410.121');self.assertEqual(c['step'],'15')
        self.assertEqual(m['metrics_budget']['max_requests'],8)
        self.assertEqual(m['metrics_budget']['max_total_bytes'],6*4194304)
        self.assertEqual(m['metrics_budget']['deadline_seconds'],60)

    def test_formal_before_capacity_is_explicitly_not_applicable(self):
        rc,m,calls,files,_=self.run_case('formal-only',stage='formal')
        self.assertEqual(rc,0)
        self.assertEqual(self.capture(m,'capacity-documents')['state'],'not_applicable')
        self.assertFalse(any(c['kind'].startswith('capacity/') for c in calls))
        self.assertIn('workload/formal/worker-evidence.json',files)

    def test_registered_resource_families_survive_the_real_collector(self):
        rc,m,calls,files,_=self.run_case('registered-resource-families',metric_modes=dict.fromkeys(REGISTERED_RESOURCES,'registered'))
        self.assertEqual(rc,0);self.assertTrue(m['complete'])
        ranges=[c for c in calls if c['kind'].startswith('range-')]
        self.assertEqual([c['kind'] for c in ranges],['range-'+x for x in FAMILIES])
        for family,names in REGISTERED_RESOURCES.items():
            with self.subTest(family=family):
                row=self.capture(m,'range-'+family)
                self.assertEqual(row['state'],'collected')
                self.assertEqual(row['series_count'],len(names));self.assertEqual(row['sample_count'],2*len(names))
                body=files['metrics/range-'+family+'.json']
                self.assertEqual(row['sha256'],hashlib.sha256(body).hexdigest())
                self.assertEqual([x['metric']['__name__'] for x in json.loads(body)['data']['result']],names)
                query=next(c['query'] for c in ranges if c['kind']=='range-'+family)
                pattern=re.fullmatch(r'\{__name__=~"([^"]+)"\}',query).group(1)
                self.assertTrue(all(re.fullmatch(pattern,name) for name in names))
                self.assertEqual(row['window']['epoch_start'],'1792601510.121');self.assertEqual(row['window']['epoch_end'],'1792602410.121')
        self.assertEqual(m['metrics_budget']['max_requests'],8)
        self.assertEqual(m['metrics_budget']['max_total_bytes'],24<<20)
        self.assertEqual(m['metrics_budget']['deadline_seconds'],60)
        self.assertTrue(all(c['step']=='15' for c in ranges))

    def test_prometheus_millisecond_closed_window_and_raw_anchor(self):
        for suffix in ['121277292','121777292','121500000','999777292']:
            end='2026-10-21T17:06:50.'+suffix+'Z'
            normalized=Decimal('1792602410.'+suffix).quantize(Decimal('.001'),rounding=ROUND_HALF_UP)
            for boundary in ['start','end']:
                with self.subTest(suffix=suffix,boundary=boundary):
                    rc,m,calls,_,_=self.run_case('millisecond-'+suffix+'-'+boundary,end=end,metric_modes={'runtime':'boundary-'+boundary})
                    self.assertEqual(rc,0)
                    row=self.capture(m,'range-runtime')
                    self.assertEqual(row['state'],'collected');self.assertEqual(row['window']['end'],end)
                    self.assertEqual(row['window']['epoch_end'],str(normalized))
                    self.assertEqual(row['window']['epoch_start'],str(normalized-900))
                    self.assertEqual(row['window']['query_precision'],'nearest_millisecond')
                    for call in [c for c in calls if c['kind'].startswith('range-')]:
                        self.assertEqual(call['start'],str(normalized-900));self.assertEqual(call['end'],str(normalized))
        for mode in ['before-start','after-end','sub-millisecond']:
            with self.subTest(mode=mode):
                rc,m,_,files,_=self.run_case(mode,metric_modes={'runtime':mode})
                self.assertNotEqual(rc,0);self.assertEqual(self.capture(m,'range-runtime')['state'],'invalid')
                self.assertIn('metrics/range-presence.json',files)

    def test_capacity_order_preserves_nanoseconds(self):
        rc,m,_,_,_=self.run_case('capacity-backward-submillisecond',stage='formal',capacity=True,capacity_end='2026-10-21T17:06:50.121277291Z')
        self.assertNotEqual(rc,0);self.assertEqual(self.capture(m,'capacity-terminal-anchor')['state'],'invalid')

    def test_formal_exemption_requires_legal_failure_and_phase_evidence(self):
        for outcome,cause in [('product_failure','message_loss'),('harness_invalid','invalid_observation'),('infrastructure_failure','lease_expiry'),('operator_stop','operator_requested'),('insufficient_evidence','insufficient_evidence')]:
            with self.subTest(outcome=outcome):
                rc,m,_,_,_=self.run_case('formal-'+outcome,stage='formal',report_mutation=lambda x:x['verdict'].update(outcome=outcome,cause=cause))
                self.assertEqual(rc,0)
                row=self.capture(m,'capacity-documents');self.assertEqual(row['state'],'not_applicable')
                self.assertEqual(row['phase_evidence'],dict(stage='formal',mode='soak',outcome=outcome,cause=cause,capacity_attempted=False))
                self.assertRegex(row['report_sha256'],r'^[0-9a-f]{64}$')
        for outcome,cause in [('pass','completed'),('passed_with_capacity_warning','infrastructure_capacity')]:
            with self.subTest(outcome=outcome):
                rc,m,_,_,_=self.run_case('formal-success-'+outcome,stage='formal',report_mutation=lambda x:x['verdict'].update(outcome=outcome,cause=cause))
                self.assertNotEqual(rc,0);self.assertEqual(self.capture(m,'capacity-terminal-anchor')['state'],'missing')

    def test_invalid_formal_verdict_or_capacity_proof_is_not_exempted(self):
        mutations={
            'unknown-outcome':lambda x:x['verdict'].update(outcome='invented'),
            'unknown-cause':lambda x:x['verdict'].update(cause='invented'),
            'missing-cause':lambda x:x['verdict'].pop('cause'),
            'success-pair':lambda x:x['verdict'].update(outcome='passed_with_capacity_warning',cause='message_loss'),
            'failure-pair':lambda x:x['verdict'].update(outcome='product_failure',cause='operator_requested'),
            'rehearsal-success':lambda x:x['verdict'].update(outcome='rehearsal_pass',cause='rehearsal_completed'),
            'capacity-only-cause':lambda x:x['verdict'].update(cause='capacity_headroom_latency'),
            'continuation':lambda x:x.update({'continue':True}),
            'mode':lambda x:x.update(mode='capacity'),
            'capacity-missing-completed':lambda x:x['capacity'].pop('completed'),
            'capacity-completed':lambda x:x['capacity'].update(completed=True),
            'capacity-lower-bound':lambda x:x['capacity'].update(lower_bound=True),
            'capacity-rate':lambda x:x['capacity'].update(maximum_passing_rate=1),
            'capacity-failed-rate':lambda x:x['capacity'].update(first_failing_rate=1),
            'capacity-recovery':lambda x:x['capacity'].update(recovery_passed=True),
            'capacity-attribution':lambda x:x['capacity'].update(attribution='product_failure'),
        }
        for label,change in mutations.items():
            with self.subTest(label=label):
                rc,m,calls,_,_=self.run_case('invalid-phase-'+label,stage='formal',report_mutation=change)
                self.assertNotEqual(rc,0);self.assertFalse(m['complete'])
                self.assertEqual(self.capture(m,'terminal-anchor')['state'],'invalid')
                self.assertFalse(any(c['kind'].startswith('range-') for c in calls))
        for stage,outcome,cause in [('rehearsal','rehearsal_pass','rehearsal_completed'),('formal','product_failure','message_loss')]:
            with self.subTest(valid_stage=stage):
                rc,m,_,_,_=self.run_case('valid-terminal-'+stage,stage=stage,report_mutation=lambda x:x['verdict'].update(outcome=outcome,cause=cause))
                self.assertEqual(rc,0)

    def test_formal_capacity_retains_both_stages(self):
        rc,m,calls,files,_=self.run_case('capacity',stage='formal',capacity=True)
        self.assertEqual(rc,0)
        for stage in ['formal','capacity']:self.assertIn('workload/'+stage+'/worker-evidence.json',files)
        self.assertEqual(len([x for x in calls if x['kind'].startswith('range-')]),8)

    def test_invalid_or_missing_diagnostic_files_stay_incomplete(self):
        for mode in ['missing','empty','malformed','oversized','unavailable','partial','timeout']:
            with self.subTest(mode=mode):
                rc,m,calls,files,_=self.run_case('worker-'+mode,document_modes={'rehearsal/worker-evidence.json':mode})
                self.assertNotEqual(rc,0);self.assertFalse(m['complete'])
                self.assertNotIn('workload/rehearsal/worker-evidence.json',files)
                expected={'partial':'unavailable','malformed':'invalid','timeout':'timeout'}.get(mode,mode)
                self.assertEqual(self.capture(m,'rehearsal-worker-evidence')['state'],expected)
                self.assertIn('workload/rehearsal/diagnostic-status.json',files)

    def test_wrong_identity_schema_and_unsafe_fields_are_rejected(self):
        mutations={
            'schema':lambda x:x.update(schema='wrong'),
            'run':lambda x:x.update(run_id='other'),
            'at':lambda x:x.update(at=START),
            'worker-duplicate':lambda x:x['workers'][1].update(worker_id=0),
            'generation':lambda x:x['workers'][0].update(generation=0),
            'sequence':lambda x:x['workers'][0].update(snapshot_sequence=12),
            'raw-field':lambda x:x['workers'][0]['evidence'].update(raw_error='secret-canary-do-not-publish'),
            'bad-example':lambda x:x['workers'][0]['evidence']['classes'][0]['first'][0].update(code=255),
        }
        for label,change in mutations.items():
            with self.subTest(label=label):
                rc,m,_,files,_=self.run_case(label,mutation=lambda name,x:change(x) if name=='worker-evidence.json' else None)
                self.assertNotEqual(rc,0);self.assertEqual(self.capture(m,'rehearsal-worker-evidence')['state'],'invalid')
                self.assertNotIn('workload/rehearsal/worker-evidence.json',files)

    def test_worker_evidence_schema_versions_keep_closed_code_ranges(self):
        # Archived v1 is never reinterpreted as the new emitter vocabulary.
        # v2 keeps the same five identity-free example fields and appends 37..46.
        for version,code,valid in [(1,27,True),(1,36,True),(1,37,False),
                                   (2,27,True),(2,37,True),(2,46,True),(2,47,False)]:
            with self.subTest(version=version,code=code):
                expected=worker()
                expected['schema']='wukongim/chat-lifecycle-worker-evidence/v'+str(version)
                for row in expected['workers']:
                    row['evidence']['classes'][0]['first'][0]['code']=code
                def mutate(name,value):
                    if name=='worker-evidence.json':
                        value.clear();value.update(copy.deepcopy(expected))
                rc,m,_,files,_=self.run_case('worker-v'+str(version)+'-code'+str(code),mutation=mutate)
                self.assertEqual(rc==0,valid)
                self.assertEqual(self.capture(m,'rehearsal-worker-evidence')['state'],'collected' if valid else 'invalid')
                path='workload/rehearsal/worker-evidence.json'
                if valid:self.assertEqual(files[path],json.dumps(expected).encode())
                else:self.assertNotIn(path,files)

    def test_capacity_required_by_passing_formal_cannot_be_missing(self):
        rc,m,calls,_,_=self.run_case('missing-capacity',stage='formal',report_mutation=lambda x:x['verdict'].update(outcome='pass',cause='completed'))
        self.assertNotEqual(rc,0);self.assertFalse(m['complete'])
        self.assertEqual(self.capture(m,'capacity-terminal-anchor')['state'],'missing')

    def test_byte_caps_accept_exact_and_reject_next_byte(self):
        for filename,maximum in [('worker-evidence.json',1<<20),('diagnostic-status.json',64<<10)]:
            # Whitespace padding keeps the actual JSON document unchanged.
            for extra in [0,1]:
                with self.subTest(filename=filename,extra=extra):
                    rc,m,_,files,_=self.run_case('boundary-'+filename+'-'+str(extra),document_modes={'rehearsal/'+filename:'boundary-'+str(maximum+extra)})
                    self.assertEqual(rc==0,extra==0)
                    row=self.capture(m,'rehearsal-'+filename.removesuffix('.json'))
                    self.assertEqual(row['state'],'collected' if extra==0 else 'oversized')

    def test_invalid_status_is_rejected(self):
        for label,change in {'wrong-run':lambda x:x.update(run_id='other'),'wrong-cut':lambda x:x.update(cut='periodic'),'raw-field':lambda x:x.update(payload='secret-canary-do-not-publish'),'wrong-sequence':lambda x:x['workers'][0].update(snapshot_sequence=9)}.items():
            with self.subTest(label=label):
                rc,m,_,files,_=self.run_case('status-'+label,mutation=lambda name,x:change(x) if name=='diagnostic-status.json' else None)
                self.assertNotEqual(rc,0);self.assertEqual(self.capture(m,'rehearsal-diagnostic-status')['state'],'invalid')
                self.assertNotIn('workload/rehearsal/diagnostic-status.json',files)

    def test_bad_terminal_anchor_never_queries_now(self):
        for label,change in {'schema':lambda x:x.update(schema_version='bad'),'not-final':lambda x:x.update(final=False),'missing-end':lambda x:x['window'].pop('end'),'future':lambda x:x['window'].update(end='2099-01-01T00:00:00Z'),'backward':lambda x:x['window'].update(end='2020-01-01T00:00:00Z')}.items():
            with self.subTest(label=label):
                rc,m,calls,_,_=self.run_case('anchor-'+label,report_mutation=change)
                self.assertNotEqual(rc,0);self.assertFalse(m['complete'])
                self.assertFalse(any(c['kind'].startswith('range-') for c in calls))
                self.assertEqual(self.capture(m,'terminal-anchor')['state'],'invalid')

    def test_partitions_retain_more_than_original_single_response_cap(self):
        rc,m,calls,files,_=self.run_case('large-split',metric_modes=dict.fromkeys(FAMILIES,'large'),combined_oversized=True)
        self.assertEqual(rc,0);self.assertTrue(m['complete'])
        total=sum(len(b) for p,b in files.items() if p.startswith('metrics/range-'))
        self.assertGreater(total,4194304);self.assertLessEqual(total,6*4194304)
        self.assertEqual(m['metrics_budget']['retained_bytes'],total)

    def test_empty_optional_metric_family_has_explicit_zero_coverage(self):
        rc,m,_,files,_=self.run_case('empty-resources',metric_modes={'go':'empty','counters':'empty'})
        self.assertEqual(rc,0);self.assertTrue(m['complete'])
        for family in ['go','counters']:
            row=self.capture(m,'range-'+family);self.assertEqual(row['series_count'],0);self.assertEqual(row['sample_count'],0)
            self.assertIsNone(row['observed_start']);self.assertIsNone(row['observed_end'])

    def test_partial_metrics_keep_successful_siblings(self):
        for mode in ['oversized','unavailable','partial','timeout','malformed','wrong-type','wrong-family','error']:
            with self.subTest(mode=mode):
                rc,m,calls,files,_=self.run_case('range-'+mode,metric_modes={'runtime':mode})
                self.assertNotEqual(rc,0);self.assertFalse(m['complete'])
                self.assertIn('metrics/range-up.json',files);self.assertIn('metrics/range-go.json',files)
                self.assertNotIn('metrics/range-runtime.json',files)
                self.assertEqual(len([c for c in calls if c['kind'].startswith('range-')]),8)
                self.assertFalse(any(p.endswith('.tmp') for p in files))


class WorkerCaptureTests(unittest.TestCase):
    @staticmethod
    def retained_snapshot(worker_id=0, classes=1):
        # Legal production retention is 64 first + 64 last per closed class,
        # not the 16 total events observed in the archived failed rehearsal.
        example=dict(stage=6,code=46,sample_index=18446744073709551615,
                     fingerprint=[255]*16,value=18446744073709551615)
        rows=[dict(class_=index,count=128,first=[copy.deepcopy(example) for _ in range(64)],
                   last=[copy.deepcopy(example) for _ in range(64)]) for index in range(1,classes+1)]
        for row in rows:row['class']=row.pop('class_')
        return dict(run_id=RUN,assignment_id='capture-fixture',phase='final',uptime=1,
                    snapshot_sequence=13,generation=1,worker_id=worker_id,worker_count=3,
                    sessions=dict(target=3333,online=3333,traffic_ready=3333),
                    messages=dict(sent=1,send_acknowledged=1),evidence=dict(classes=rows))

    def run_capture(self, consumer, endpoint=None, size=None, classes=1):
        with tempfile.TemporaryDirectory(prefix='wk-worker-capture-') as directory:
            d=Path(directory);bin=d/'bin';bin.mkdir();responses=d/'responses';responses.mkdir()
            output=d/'output';output.mkdir();expected={}
            for index in range(3):
                bodies={'snapshot':self.retained_snapshot(index,classes),
                        'status':dict(run_id=RUN,assignment_id='capture-fixture',phase='final',
                                      generation=1,worker_id=index,worker_count=3,unexpected=False,traffic_ready=True)}
                for kind,value in bodies.items():
                    body=json.dumps(value,separators=(',',':')).encode()+b'\n'
                    if kind==endpoint and size is not None:
                        self.assertLessEqual(len(body),size)
                        body+=b' '*(size-len(body))
                    name='worker-'+str(index+1)+'-'+kind+'.json'
                    (responses/name).write_bytes(body);expected[name]=body
            scripts={
                'timeout':'#!/bin/sh\nshift\nexec "$@"\n',
                'sleep':'#!/bin/sh\nexit 0\n',
                'ssh':r'''#!/bin/sh
command=""
for arg do command="$arg"; done
printf '%s\0' "$command" >>"$WORKER_CAPTURE_COMMANDS"
case "$command" in
  *'systemctl stop'*) exit 0 ;;
  *'systemctl is-active'*) printf 'active\n'; exit 0 ;;
  *'systemctl show'*) printf 'ActiveState=active\n'; exit 0 ;;
  *journalctl*) printf '%064d\n' 0; exit 0 ;;
  *api/v1/targets*) printf '{"status":"success","data":{"activeTargets":[]}}\n'; exit 0 ;;
  *'/reports/rehearsal/'*) exit 3 ;;
esac
case "$command" in *19091*) worker=1 ;; *19092*) worker=2 ;; *19093*) worker=3 ;; *) exit 91 ;; esac
case "$command" in *'/v1/chat-lifecycle/status'*) kind=status ;; *'/v1/chat-lifecycle/snapshot'*) kind=snapshot ;; *) exit 92 ;; esac
cat "$WORKER_CAPTURE_RESPONSES/worker-$worker-$kind.json"
''',
                'wkchatlifecycle':r'''#!/bin/sh
case "$1" in
  repair-capture) printf '{"fixture":true}\n' ;;
  repair-observe) printf '{"state":{},"decision":{"action":"stop_and_diagnose","reason":"fixture_terminal"}}\n' ;;
  repair-abort) printf '{"state":{},"decision":{"action":"abort","reason":"observation_unavailable"}}\n' ;;
  *) exit 93 ;;
esac
'''}
            for name,body in scripts.items():
                path=bin/name;path.write_text(body);path.chmod(0o700)
            ssh_config=d/'ssh-config';ssh_config.write_text('Host wukong-load\n')
            state=d/'state.json';state.write_text('{}\n')
            run_start=d/'run-start.json'
            run_start.write_text(json.dumps(dict(schema='wukongim.chat_lifecycle.run_start/v1',stage='rehearsal',
                started_at=START,expected_end_at=END,run_hash='sha256:'+'a'*64,assignment_hash='sha256:'+'b'*64,generation=1)))
            env=dict(os.environ,PATH=str(bin)+os.pathsep+os.environ['PATH'],WORKER_CAPTURE_COMMANDS=str(d/'commands'),
                     WORKER_CAPTURE_RESPONSES=str(responses),WK_CHAT_LAB_DIAGNOSIS_DIR=str(output),
                     WK_CHAT_LAB_SSH_CONFIG=str(ssh_config),WK_CHAT_LAB_REQUEST_ID=RUN,
                     WK_CHAT_REPAIR_TOOL=str(bin/'wkchatlifecycle'),WK_CHAT_REPAIR_STATE=str(state),
                     WK_CHAT_REPAIR_RUN_START=str(run_start),WK_CHAT_REPAIR_OUTPUT_DIR=str(output),
                     WK_CHAT_REPAIR_SSH_CONFIG=str(ssh_config),WK_CHAT_REPAIR_REQUEST_ID=RUN,
                     WK_CHAT_REPAIR_OPERATOR_STOP_FILE=str(d/'no-operator-stop'),
                     WK_CHAT_REPAIR_MAX_SECONDS='60',WK_CHAT_REPAIR_POLL_SECONDS='1')
            script='collect-local-diagnosis.sh' if consumer=='local' else 'repair-monitor.sh'
            run=subprocess.run(['bash',str(ROOT/'scripts/chat-lifecycle'/script)],env=env,cwd=ROOT,capture_output=True,timeout=20)
            self.assertNotIn(b'Authorization: Bearer ',run.stdout+run.stderr)
            retained={str(p.relative_to(output)):p.read_bytes() for p in output.rglob('*') if p.is_file()}
            calls=[x.decode() for x in (d/'commands').read_bytes().split(b'\0') if x]
            RESULTS.append(dict(case='worker-capture-'+consumer+'-'+str(endpoint)+'-'+str(size)+'-'+str(classes),
                exit_code=run.returncode,files=[dict(path=p,bytes=len(body),sha256=hashlib.sha256(body).hexdigest()) for p,body in sorted(retained.items())]))
            return run.returncode,retained,expected,calls

    def assert_captured(self,consumer,rc,files,expected):
        if consumer=='local':
            self.assertEqual(rc,0)
            summary=json.loads(files['summary.json'])
            self.assertEqual(summary['classification'],'captured');self.assertEqual(summary['failed_captures'],0)
        else:
            self.assertEqual(rc,10)
            self.assertEqual(json.loads(files['repair-decision.json'])['decision']['reason'],'fixture_terminal')
        for name,body in expected.items():
            if consumer=='repair':
                worker,kind=re.fullmatch(r'worker-(\d)-(status|snapshot)\.json',name).groups()
                name='terminal-cut/'+kind+'-'+worker+'.json'
            self.assertEqual(files[name],body)

    def test_all_worker_capture_callers_retain_128_examples(self):
        body=json.dumps(self.retained_snapshot(),separators=(',',':')).encode()+b'\n'
        self.assertGreater(len(body),16384);self.assertLess(len(body),262144)
        for consumer in ('local','repair'):
            with self.subTest(consumer=consumer):
                rc,files,expected,calls=self.run_capture(consumer)
                self.assert_captured(consumer,rc,files,expected)
                for port in ('19091','19092','19093'):
                    for endpoint in ('status','snapshot'):
                        self.assertEqual(sum(':'+port+'/v1/chat-lifecycle/'+endpoint in call for call in calls),1)

    def test_worker_capture_bound_accepts_exact_and_rejects_next_byte(self):
        for consumer in ('local','repair'):
            for endpoint in ('status','snapshot'):
                for extra in (0,1):
                    with self.subTest(consumer=consumer,endpoint=endpoint,extra=extra):
                        rc,files,expected,_=self.run_capture(consumer,endpoint,262144+extra)
                        if extra==0:
                            self.assert_captured(consumer,rc,files,expected)
                        elif consumer=='local':
                            summary=json.loads(files['summary.json'])
                            self.assertEqual(summary['classification'],'insufficient_evidence')
                            self.assertEqual(summary['failed_captures'],3)
                            for index in range(1,4):self.assertNotIn('worker-'+str(index)+'-'+endpoint+'.json',files)
                        else:
                            self.assertEqual(rc,10)
                            self.assertEqual(json.loads(files['repair-decision.json'])['decision']['reason'],'observation_unavailable')
                            for index in range(1,4):self.assertNotIn('observation-failure/'+endpoint+'-'+str(index)+'.json',files)
                        self.assertFalse(any('.next' in name for name in files))

    def test_maximum_retained_evidence_shape_fits_capture_and_terminal_budgets(self):
        # Four classes can each retain 128 maximal five-field examples. The
        # full-snapshot Go marshal bound is tested at its owning worker seam.
        snapshots=[self.retained_snapshot(index,classes=4) for index in range(3)]
        for snapshot in snapshots:
            body=json.dumps(snapshot,separators=(',',':')).encode()+b'\n'
            self.assertLess(len(body),262144)
            self.assertEqual(sum(len(row['first'])+len(row['last']) for row in snapshot['evidence']['classes']),512)
        document=worker();document['schema']='wukongim/chat-lifecycle-worker-evidence/v2'
        for index,row in enumerate(document['workers']):row['evidence']=snapshots[index]['evidence']
        self.assertLess(len(json.dumps(document,separators=(',',':')).encode())+1,1048576)


class HelperBoundsTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        spec=importlib.util.spec_from_file_location('terminal_evidence',ROOT/'scripts/chat-lifecycle/terminal-evidence.py')
        cls.helper=importlib.util.module_from_spec(spec);spec.loader.exec_module(cls.helper)

    def test_connect_evidence_v3_and_historical_closed_vocabularies(self):
        def document(version, code, value, stage=8, failure_class=4):
            item=worker();item['schema']='wukongim/chat-lifecycle-worker-evidence/v'+str(version)
            for entry in item['workers']:
                entry['evidence']['classification']='harness_invalid'
                entry['evidence']['classes']=[dict(
                    **{'class':failure_class},count=1,
                    first=[dict(stage=stage,code=code,sample_index=0,fingerprint=[17]*16,value=value)],last=None)]
            return item
        for version,code,value,stage in [(1,30,0,8),(2,46,18446744073709551615,6)]+[(3,code,15 if code==52 else 10,8) for code in range(47,54)]:
            with self.subTest(accepted=(version,code)):
                item=document(version,code,value,stage)
                self.helper.validate_worker(json.dumps(item).encode(),report())
                self.assertEqual(set(item['workers'][0]['evidence']['classes'][0]['first'][0]),
                                 {'stage','code','sample_index','fingerprint','value'})
        for version,code,value,stage,kind in [(1,47,0,8,4),(2,47,0,8,4),(3,54,0,8,4),
                (3,47,11,8,4),(3,52,31,8,4),(3,52,1,8,4),(3,52,15,6,4),(3,52,15,8,3)]:
            with self.subTest(rejected=(version,code,value,stage,kind)):
                with self.assertRaises((ValueError,KeyError,TypeError)):
                    self.helper.validate_worker(json.dumps(document(version,code,value,stage,kind)).encode(),report())

    def test_terminal_report_contract_versions_and_scope_fail_closed(self):
        # Failure matrix: v6/v5 schema and historical thresholds, absent version or
        # scope, cross-stage scope, and a claimed Linux proof must not become
        # a current terminal anchor. The collector only retains evidence;
        # the Go report reader owns the complete numerical gate validation.
        mutations = {
            'old-v6-schema': lambda x: x.update(schema_version='wukongim/chat-lifecycle-report/v6'),
            'old-v5-schema': lambda x: x.update(schema_version='wukongim/chat-lifecycle-report/v5'),
            'old-thresholds': lambda x: x.update(threshold_version='wukongim/chat-lifecycle-thresholds/v1'),
            'missing-thresholds': lambda x: x.pop('threshold_version'),
            'missing-qualification': lambda x: x.pop('qualification'),
            'local-scope': lambda x: x['qualification'].update(scope='local_diagnostic'),
            'cross-stage-scope': lambda x: x['qualification'].update(scope='formal_protocol'),
            'claimed-linux-proof': lambda x: x['qualification'].update(scope='linux_verified'),
            'linux-proof-exempted': lambda x: x['qualification'].update(external_linux_evidence_required=False),
            'non-boolean-linux-requirement': lambda x: x['qualification'].update(external_linux_evidence_required='true'),
        }
        with tempfile.TemporaryDirectory() as directory:
            target=Path(directory)/'final.json'
            for stage in ('rehearsal', 'formal', 'capacity'):
                with self.subTest(valid_stage=stage):
                    value=report(stage)
                    if stage=='capacity':value['capacity']['attempted']=True
                    target.write_text(json.dumps(value))
                    parsed,_=self.helper.read_report(target,stage)
                    self.assertEqual(parsed['qualification']['scope'],'rehearsal' if stage=='rehearsal' else 'formal_protocol')
            for label,change in mutations.items():
                with self.subTest(invalid_contract=label):
                    value=report();change(value);target.write_text(json.dumps(value))
                    with self.assertRaises((ValueError,KeyError,TypeError)):
                        self.helper.read_report(target,'rehearsal')

    def test_selector_partition_is_disjoint_and_complete(self):
        h=self.helper
        # These names come from the registered exporters, not the selector being tested.
        expected={
            'up':['up'],
            'runtime':['wukongim_runtime_pool_queue_depth'],
            'channel_worker':['wukongim_channel_worker_inflight'],
            'process':['wukongim_process_cpu_jiffies_total','wukongim_process_up',
                       'process_cpu_seconds_total','process_start_time_seconds','process_resident_memory_bytes'],
            'counters':['wukongim_channel_meta_created_total','wukongim_channelv2_meta_created_total',
                        'wukongim_channel_activation_rejected_total','wukongim_channelv2_activation_rejected_total'],
            'go':['go_goroutines','go_memstats_heap_alloc_bytes','go_gc_duration_seconds_count',
                  'go_sched_latencies_seconds_bucket','go_sched_latencies_seconds_sum','go_sched_latencies_seconds_count'],
        }
        for family,names in expected.items():
            for name in names:
                with self.subTest(metric=name):
                    self.assertEqual([key for key,pattern in h.METRIC_PARTITIONS if re.fullmatch(pattern,name)],[family])
        for name in ['wukongim_metadata_create_total','wukongim_activation_rejected_total','wukongim_go_heap',
                     'wukongim_channel_meta_created_total_extra','wukongim_channelv2_activation_rejected_total_sum',
                     'wukongim_channel_meta_create_queue_depth','node_cpu_seconds_total','wukongim_other_secret']:
            with self.subTest(excluded=name):
                self.assertFalse(any(re.fullmatch(pattern,name) for _,pattern in h.METRIC_PARTITIONS))
        self.assertEqual([key for key,_ in h.METRIC_PARTITIONS],FAMILIES)

    def test_registered_resource_matrices_retain_exact_bytes_without_extra_queries(self):
        h=self.helper
        exported={
            'process':['wukongim_process_cpu_jiffies_total','process_cpu_seconds_total',
                       'process_start_time_seconds','process_resident_memory_bytes'],
            'counters':['wukongim_channelv2_meta_created_total','wukongim_channel_meta_created_total',
                        'wukongim_channelv2_activation_rejected_total','wukongim_channel_activation_rejected_total'],
            'go':['go_goroutines','go_memstats_heap_alloc_bytes','go_gc_duration_seconds_count',
                  'go_sched_latencies_seconds_bucket','go_sched_latencies_seconds_sum','go_sched_latencies_seconds_count'],
        }
        bodies={family:json.dumps({'status':'success','data':{'resultType':'matrix','result':[
            {'metric':{'__name__':name,'job':'wukongim'},'values':[[1792602400,'1'],[1792602410,'2']]}
            for name in names]}}).encode() for family,names in exported.items()}
        empty=b'{"status":"success","data":{"resultType":"matrix","result":[]}}'
        calls=[]
        def capture(name,query,start,end,timeout,cap):
            calls.append((name,start,end,timeout,cap));return 'collected',bodies.get(name,empty)
        with tempfile.TemporaryDirectory() as directory:
            output=Path(directory)
            rows,budget=h.collect_ranges(output,{'end':END,'epoch_start':'1792601510.121','epoch_end':'1792602410.121','source':'rehearsal/final.json'},capture)
            for family,names in exported.items():
                with self.subTest(family=family):
                    row=next(x for x in rows if x['name']=='range-'+family)
                    self.assertEqual(row['state'],'collected')
                    self.assertEqual(row['series_count'],len(names));self.assertEqual(row['sample_count'],2*len(names))
                    self.assertEqual((output/row['path']).read_bytes(),bodies[family])
                    self.assertEqual(row['sha256'],hashlib.sha256(bodies[family]).hexdigest())
        self.assertEqual([x[0] for x in calls],FAMILIES)
        self.assertTrue(all(x[1:3]==('1792601510.121','1792602410.121') and 0<x[3]<=30 and x[4]<=4<<20 for x in calls))
        self.assertEqual(budget['max_requests'],8);self.assertEqual(budget['deadline_seconds'],60)
        self.assertEqual(budget['max_total_bytes'],24<<20)

    def test_registered_counter_names_follow_exported_aliases(self):
        h=self.helper
        for source in ['channel.go','channel_runtime.go']:
            names=re.findall(r'Name:\s+"([^"]+)"',(ROOT/'pkg/metrics'/source).read_text())
            for name in names:
                if not name.endswith(('meta_created_total','activation_rejected_total')):continue
                exported=[name,name.replace('wukongim_channelv2_','wukongim_channel_')]
                for metric in set(exported):
                    self.assertEqual([key for key,pattern in h.METRIC_PARTITIONS if re.fullmatch(pattern,metric)],['counters'],metric)

    def test_diagnostic_metric_names_are_exact_and_disjoint(self):
        h=self.helper;patterns=dict(h.METRIC_PARTITIONS)
        for source,fragment in [('presence.go','presence_endpoint_lookup_'),('delivery.go','delivery_')]:
            names=re.findall(r'Name:\s+"([^"]+)"',(ROOT/'pkg/metrics'/source).read_text())
            selected=[name for name in names if fragment in name and (source=='presence.go' or any(part in name for part in ['recipient_worker_','recipient_authority_resolve_','push_rpc_']))]
            for name in selected:
                variants=[name+'_bucket',name+'_sum',name+'_count'] if name.endswith(('seconds','process_recipients')) else [name]
                for metric in variants:self.assertEqual(sum(bool(re.fullmatch(p,metric)) for _,p in h.METRIC_PARTITIONS),1,metric)
        for name in ['wukongim_presence_endpoint_lookup_secret','wukongim_delivery_payload','wukongim_delivery_recipient_worker_other']:
            self.assertFalse(any(re.fullmatch(p,name) for _,p in h.METRIC_PARTITIONS),name)

    def test_metric_total_transfer_budget_is_shared(self):
        h=self.helper;calls=[]
        def capture(name,query,start,end,timeout,cap):
            calls.append(cap);return 'oversized',b'x'*(cap+1)
        with tempfile.TemporaryDirectory() as d:
            rows,budget=h.collect_ranges(Path(d),{'end':END,'epoch_end':'1792602410.121277292','epoch_start':'1792601510.121277292','source':'rehearsal/final.json'},capture)
        self.assertLessEqual(sum(x+1 for x in calls),24<<20)
        self.assertLessEqual(len(calls),8);self.assertEqual(budget['observed_bytes'],sum(x+1 for x in calls))
        self.assertTrue(any(x['state']=='budget_exhausted' for x in rows));self.assertEqual(budget['retained_bytes'],0)

    def test_metric_deadline_stops_requests_and_retains_first_partition(self):
        h=self.helper;calls=[]
        def capture(name,query,start,end,timeout,cap):
            calls.append(name);return 'collected',json.dumps({'status':'success','data':{'resultType':'matrix','result':[{'metric':{'__name__':'wukongim_presence_endpoint_lookup_total'},'values':[[1792602410,'1']]}]}}).encode()
        with tempfile.TemporaryDirectory() as d, mock.patch.object(h.time,'monotonic',side_effect=[0,0,61,61,61,61,61,61,61]):
            rows,budget=h.collect_ranges(Path(d),{'end':END,'epoch_end':'1792602410.121277292','epoch_start':'1792601510.121277292','source':'rehearsal/final.json'},capture)
        self.assertEqual(calls,['presence']);self.assertEqual(rows[0]['state'],'collected')
        self.assertTrue(all(x['state']=='budget_exhausted' for x in rows[1:]));self.assertEqual(budget['requests'],1)


if __name__=='__main__':
    suite=unittest.defaultTestLoader.loadTestsFromModule(sys.modules[__name__]);result=unittest.TextTestRunner(verbosity=2).run(suite)
    destination=os.environ.get('CHAT_LIFECYCLE_TERMINAL_EVIDENCE_TEST_ARTIFACT')
    if destination:
        Path(destination).write_text(json.dumps({'schema':'wukongim/terminal-evidence-integration/v1','source_sha':subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip(),'tests_run':result.testsRun,'failure_count':len(result.failures),'error_count':len(result.errors),'fixture_only':True,'cases':RESULTS},indent=2)+'\n')
    sys.exit(not result.wasSuccessful())
