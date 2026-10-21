from pathlib import Path
import urllib.request,urllib.error,json,hashlib,datetime
out=Path(__file__).parent
accept='application/vnd.oci.image.index.v1+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.docker.distribution.manifest.v2+json'
receipts=[]
def get(reference,name):
 url='https://quay.io/v2/rockylinux/rockylinux/manifests/'+reference
 req=urllib.request.Request(url,headers={'Accept':accept})
 try:
  with urllib.request.urlopen(req,timeout=20) as r:status=r.status;headers=dict(r.headers);raw=r.read(2*1024*1024+1)
 except urllib.error.HTTPError as e:status=e.code;headers=dict(e.headers);raw=e.read(2*1024*1024+1)
 assert len(raw)<=2*1024*1024
 (out/(name+'.json')).write_bytes(raw)
 record={'url':url,'observed_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'status':status,'digest':headers.get('docker-content-digest',headers.get('Docker-Content-Digest')),'actual_sha256':hashlib.sha256(raw).hexdigest(),'content_type':headers.get('Content-Type',headers.get('content-type')),'bytes':len(raw)}
 receipts.append(record);return record,json.loads(raw)
try:
 r,index=get('9-ubi-init','rocky-init-registry-index')
 assert r['status']==200
 selected=[m for m in index['manifests'] if m['platform']['os']=='linux' and m['platform']['architecture']=='amd64']
 assert len(selected)==1
 r,manifest=get(selected[0]['digest'],'rocky-init-amd64-registry-manifest')
 assert r['status']==200 and 'sha256:'+r['actual_sha256']==selected[0]['digest']
 result={'selected_descriptor':selected[0],'manifest':manifest,'candidate_reference':'quay.io/rockylinux/rockylinux@'+selected[0]['digest'],'pulled_or_executed':False}
 (out/'rocky-init-candidate.json').write_text(json.dumps(result,indent=2)+'\n');print(json.dumps(result,indent=2))
finally:(out/'quay-http-receipts.json').write_text(json.dumps(receipts,indent=2)+'\n')
