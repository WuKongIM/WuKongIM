from pathlib import Path
import contextlib,gc,hashlib,importlib.util,io,json,socket,sys,tempfile,warnings
from unittest import mock
R=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/packages-native-delivery-sdk')
E=Path('/tmp/wukongim-v3-readiness-20261021/packages/m2b-native-delivery-sdk-repair-v2')
OUT=Path(__file__).parent
freeze=json.loads((E/'source-freeze.json').read_bytes())
def h(p):return hashlib.sha256(p.read_bytes()).hexdigest()
before={f['path']:h(R/f['path']) for f in freeze['files']}
assert all(before[f['path']]==f['sha256'] for f in freeze['files'])
spec=importlib.util.spec_from_file_location('native_sdk_drill',R/'tests/provider_sdk/native_object_delivery_sdk_drill.py')
d=importlib.util.module_from_spec(spec);spec.loader.exec_module(d)
probes=[]
for mode in ('clean','swallowed','converted','primary_and_cleanup','cleanup_only','cleanup_noop'):
 with tempfile.TemporaryDirectory(prefix='wk-spec-probe-') as tmp:
  root=Path(tmp);work=root/'native-sdk-work';work.mkdir();bundle=work/'bundle.json';bundle.write_text('{}');output=root/'receipt.json'
  def cases(args,report):
   report['object_put_attempts']=7
   if mode in ('swallowed','converted'):
    def attempt():
     # A context manager ensures this probe-owned socket closes. The driver
     # guard intercepts connect before any real network operation.
     with socket.socket() as s:
      try:s.connect(('127.0.0.1',9))
      except RuntimeError:
       if mode=='converted':raise ValueError('converted') from None
       raise
    d.refused(attempt)
   if mode=='primary_and_cleanup':raise KeyError('source-stage')
   report['cases']=[{'case':name,'passed':True} for name in d.CASES[:19]]
  argv=['driver','sdk','--source-sha',freeze['base'],'--bundle',str(bundle),'--bundle-sha256','0'*64,'--output',str(output)]
  with warnings.catch_warnings(record=True) as seen,contextlib.redirect_stdout(io.StringIO()),mock.patch.object(sys,'argv',argv),mock.patch.object(d,'sdk_cases',side_effect=cases):
   warnings.simplefilter('always',ResourceWarning)
   if mode in ('primary_and_cleanup','cleanup_only','cleanup_noop'):
    with mock.patch.object(d.shutil,'rmtree',side_effect=None if mode=='cleanup_noop' else PermissionError('cleanup')):exit_code=d.main()
   else:exit_code=d.main()
   gc.collect()
  r=json.loads(output.read_bytes())
  probes.append({'mode':mode,'exit_code':exit_code,'state':r['state'],'error_type':r.get('error_type'),'cleanup':r['cleanup'],'network_guard':r['network_guard'],'attempts_preserved':r['object_put_attempts']==7,'receipt_bytes':output.stat().st_size,'selfcheck_resource_warnings':[{'category':w.category.__name__,'message':str(w.message).split(' laddr=')[0],'file':Path(w.filename).name,'line':w.lineno} for w in seen if issubclass(w.category,ResourceWarning)]})
  assert exit_code==(0 if mode=='clean' else 1)
  assert r['network_guard']['unexpected_attempts']==(1 if mode in ('swallowed','converted') else 0)
  if mode=='primary_and_cleanup':assert r['error_type']=='KeyError' and r['cleanup']['error_type']=='PermissionError'
  if mode=='cleanup_noop':assert r['cleanup']['error_type']=='CleanupIncomplete'
  assert r['object_put_attempts']==7
  # After restoring rmtree, TemporaryDirectory removes any deliberate leftovers.
after={f['path']:h(R/f['path']) for f in freeze['files']};assert before==after
result={'probe_scope':'stdlib-only main/guard/cleanup boundaries; sdk_cases replaced, no SDK/native/provider execution; connect intercepted by frozen guard before OS','probes':probes,'source_hashes_before':before,'source_hashes_after':after,'source_unchanged':True,'actual_network_connections':0,'owned_scratch_removed':True}
(OUT/'bounded-probes.json').write_text(json.dumps(result,indent=2)+'\n')
print(json.dumps({'probes':len(probes),'source_unchanged':True,'warnings':sum(len(x['selfcheck_resource_warnings']) for x in probes)}))
