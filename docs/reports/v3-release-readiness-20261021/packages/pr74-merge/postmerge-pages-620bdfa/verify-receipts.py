"""Offline read-only verification of the first post-merge Pages run."""
import base64, hashlib, json, re
from pathlib import Path
P=Path(__file__).resolve().parent
HEAD='620bdfa1804c732dc1b3b072bb64dbe6f0ae0475'
TREE='7a498c42b4f5b0e6f86a524878fde651d7301371'
def read(n): return json.loads((P/n).read_text())
r=read('pages-run-final.json')
assert r['id']==39626500979 and r['head_sha']==HEAD and r['run_attempt']==1 and r['event']=='push'
assert r['status']=='completed' and r['conclusion']=='success'
m=read('merged-commit.json');assert m['sha']==HEAD and m['tree']['sha']==TREE
text=(P/'pages-job.log').read_text()
checkout=re.search(r'git log -1 --format=%H\n[^\n]*?([0-9a-f]{40})\n',text).group(1)
assert checkout==HEAD
assert re.search(r'Ran 528 tests[^\n]*\n[^\n]*\n[^\n]*OK\n',text)
assert re.search(r'Ran 4 tests[^\n]*\n[^\n]*\n[^\n]*OK\n',text)
jobs=read('pages-jobs-read2.json')['jobs'];assert [j['conclusion'] for j in jobs]==['success','skipped']
for s in jobs[0]['steps']:
    if s['name'] in ['Configure Pages','Upload complete Pages artifact']: assert s['conclusion']=='skipped'
api=read('channels-merged-source.json');data=base64.b64decode(api['content'])
assert data==(P/'channels-merged-decoded.json').read_bytes()
assert hashlib.sha1(b'blob '+str(len(data)).encode()+b'\0'+data).hexdigest()==api['sha']
stable=json.loads(data)['channels']['stable'];assert stable=={'enabled':False,'status':'object_storage_required','releases':[]}
print(json.dumps({'passed':True,'head':HEAD,'tree':TREE,'actual_checkout':checkout,'run':r['id'],'attempt':1,'event':'push','tests':528,'skipped_tests':0,'publication_control_tests':4,'validation_job':119313016951,'validation':'success','deploy_job':119313928477,'deploy':'skipped','stable':stable,'channels_git_blob':api['sha'],'reruns_dispatched':0,'production_mutations':0},indent=2))
