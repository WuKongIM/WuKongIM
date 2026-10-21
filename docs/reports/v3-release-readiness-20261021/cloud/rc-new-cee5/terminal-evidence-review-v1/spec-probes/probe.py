from pathlib import Path
import sys,importlib.util,json,tempfile,copy
sys.dont_write_bytecode=True
ROOT=Path(sys.argv[1])
def load(name,path):
 s=importlib.util.spec_from_file_location(name,path);m=importlib.util.module_from_spec(s);s.loader.exec_module(m);return m
h=load('helper',ROOT/'scripts/chat-lifecycle/terminal-evidence.py')
f=load('fixture',ROOT/'scripts/testdata/chat_lifecycle_terminal_evidence_test.py')
body=b'{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"__name__":"up"},"values":[[1792601510.121,"1"],[1792602410.121,"1"]]}]}}'
results=[]
for label,start,end in [('report_nanosecond_anchor','1792601510.121277292','1792602410.121277292'),('millisecond_control','1792601510.121','1792602410.121')]:
 try:coverage=h.validate_metric(body,'up',start,end);results.append({'case':label,'accepted':True,'coverage':coverage})
 except ValueError:results.append({'case':label,'accepted':False})
h.document_capture=lambda output,directory,report,name,limit,validator,timeout:{'class':'workload','name':directory+'-'+name,'state':'collected','bytes':1}
h.collect_ranges=lambda output,anchor:([],{'requests':0})
for outcome,cause in [('product_failure','message_loss'),('passed_with_capacity_warning','infrastructure_capacity'),('not_a_valid_outcome','not_a_valid_cause')]:
 with tempfile.TemporaryDirectory() as d:
  root=Path(d);(root/'formal').mkdir();report=f.report('formal');report['verdict'].update(outcome=outcome,cause=cause);(root/'formal/final.json').write_text(json.dumps(report))
  r=h.main('formal',root/'evidence',30);results.append({'case':'formal_without_capacity','outcome':outcome,'capacity_attempted':False,'captures':r['captures']})
print(json.dumps({'source_execution':'Only imported pure validation/main logic; network capture functions replaced with deterministic no-I/O functions. No source modification.','results':results},indent=2))
