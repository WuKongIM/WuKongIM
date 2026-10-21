from pathlib import Path
from decimal import Decimal
import sys, importlib.util, json, tempfile, hashlib
sys.dont_write_bytecode = True
ROOT = Path(sys.argv[1])
def load(name, path):
    s=importlib.util.spec_from_file_location(name,path); m=importlib.util.module_from_spec(s); s.loader.exec_module(m); return m
h=load('helper',ROOT/'scripts/chat-lifecycle/terminal-evidence.py')
f=load('fixture',ROOT/'scripts/testdata/chat_lifecycle_terminal_evidence_test.py')
results=[]
def note(name, actual, expected):
    results.append({'case':name,'actual':actual,'expected':expected,'pass':actual==expected})
def body(stamp):
    return ('{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"__name__":"up"},"values":[['+str(stamp)+',"1"]]}]}}').encode()
with tempfile.TemporaryDirectory() as d:
    p=Path(d)/'report.json'
    for suffix, expected in [('121277292','1792602410.121'),('121777292','1792602410.122'),('121500000','1792602410.122'),('999777292','1792602411.000')]:
        r=f.report('formal');r['window']['end']='2026-10-21T17:06:50.'+suffix+'Z'; raw=json.dumps(r).encode();p.write_bytes(raw)
        _,a=h.read_report(p,'formal')
        note('round-'+suffix,a['epoch_end'],expected)
        note('raw-'+suffix,[a['end'],a['report_sha256']],[r['window']['end'],hashlib.sha256(raw).hexdigest()])
        note('900s-'+suffix,str(Decimal(a['epoch_end'])-Decimal(a['epoch_start'])),'900.000')
        start,end=Decimal(a['epoch_start']),Decimal(a['epoch_end'])
        for label,stamp,accept in [('first',start,True),('last',end,True),('before',start-Decimal('.001'),False),('after',end+Decimal('.001'),False),('sub-ms',start+Decimal('.0001'),False)]:
            try:h.validate_metric(body(stamp),'up',a['epoch_start'],a['epoch_end']); actual=True
            except ValueError:actual=False
            note('metric-'+suffix+'-'+label,actual,accept)
h.document_capture=lambda output,directory,report,name,limit,validator,timeout:{'name':directory+'-'+name,'state':'collected'}
h.collect_ranges=lambda output,anchor:([],{'requests':0,'anchor':anchor})
def maincase(label,change,expected,cap_end=None):
    with tempfile.TemporaryDirectory() as d:
        p=Path(d);(p/'formal').mkdir();r=f.report('formal');change(r);(p/'formal/final.json').write_text(json.dumps(r))
        if cap_end:
            (p/'capacity').mkdir();c=f.report('capacity');c['capacity']['attempted']=True;c['window']['end']=cap_end;(p/'capacity/final.json').write_text(json.dumps(c))
        result=h.main('formal',p/'evidence',30)
        rows={x['name']:x['state'] for x in result['captures']}
        note(label,[(k,v) for k,v in rows.items() if k in ('terminal-anchor','capacity-terminal-anchor','capacity-documents')],[expected])
for outcome,cause in [('product_failure','message_loss'),('harness_invalid','invalid_observation'),('infrastructure_failure','lease_expiry'),('operator_stop','operator_requested'),('insufficient_evidence','insufficient_evidence')]:
    maincase('legal-failure-'+outcome,lambda r,o=outcome,c=cause:r['verdict'].update(outcome=o,cause=c),('capacity-documents','not_applicable'))
for outcome,cause in [('pass','completed'),('passed_with_capacity_warning','infrastructure_capacity')]:
    maincase('success-'+outcome,lambda r,o=outcome,c=cause:r['verdict'].update(outcome=o,cause=c),('capacity-terminal-anchor','missing'))
invalids={
 'unknown-outcome':lambda r:r['verdict'].update(outcome='not_a_valid_outcome'),
 'unknown-cause':lambda r:r['verdict'].update(cause='not_a_valid_cause'),
 'crossed-failure':lambda r:r['verdict'].update(cause='operator_requested'),
 'success-cause-failure':lambda r:r['verdict'].update(cause='completed'),
 'missing-cause':lambda r:r['verdict'].pop('cause'),
 'continuing':lambda r:r.update({'continue':True}),
 'wrong-stage':lambda r:r.update(stage='rehearsal'),
 'wrong-mode':lambda r:r.update(mode='capacity'),
 'incomplete-zero':lambda r:r['capacity'].pop('completed'),
 'contradictory-zero':lambda r:r['capacity'].update(maximum_passing_rate=1),
 'capacity-cause-on-soak':lambda r:r['verdict'].update(cause='capacity_headroom_latency')}
for label,change in invalids.items():maincase(label,change,('terminal-anchor','invalid'))
maincase('attempted-missing',lambda r:r['capacity'].update(attempted=True),('capacity-terminal-anchor','missing'))
maincase('raw-capacity-order',lambda r:r['verdict'].update(outcome='pass',cause='completed'),('capacity-terminal-anchor','invalid'),'2026-10-21T17:06:50.121277291Z')
print(json.dumps({'scope':'Independent pure helper read_report/validate_metric/main probes; capture functions replaced with no-I/O implementations. No product, SSH, cloud or full tests.','results':results,'count':len(results),'failed':sum(not x['pass'] for x in results)},indent=2))
assert all(x['pass'] for x in results)
