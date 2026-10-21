"""Read selected public JSON ZIP members; do not download raw diagnostics or ciphertext."""
from pathlib import Path
import os,subprocess,json,hashlib,datetime,re,struct,urllib.request,zlib,sys
b=Path(__file__).parent;p=b.parents[2]/'rc-new-cee5-private'/'stop-release-observation'
a=next(x for x in json.loads((b/'poll-02/39645768090-artifacts.json').read_text())['artifacts'] if x['id']==12367302003)
out=b/'artifact-12367302003-selected';out.mkdir(exist_ok=False)
env=dict(os.environ,GH_DEBUG='api')
r=subprocess.run(['gh','api','--method','HEAD','--include',f"repos/WuKongIM/WuKongIM/actions/artifacts/{a['id']}/zip"],env=env,capture_output=True,timeout=45)
for n,data in [('headers',r.stdout),('stderr',r.stderr)]:
 f=p/('complete-12367302003.'+n);f.write_bytes(data);f.chmod(0o600)
ranges=[]
try:
 assert r.returncode==0
 matches=re.findall(rb'(?im)^(?:< )?location:\s*(https://[^\r\n]+)',r.stdout+b'\n'+r.stderr);assert matches,'no redirect location'
 url=matches[0].decode();total=a['size_in_bytes']
 def get(start,end,purpose):
  assert 0<=start<=end<total and end-start<262144
  req=urllib.request.Request(url,headers={'Range':f'bytes={start}-{end}','Accept-Encoding':'identity'})
  with urllib.request.urlopen(req,timeout=30) as response:
   assert response.status==206,'refuse fullbody'
   assert response.headers['Content-Range']==f'bytes {start}-{end}/{total}'
   data=response.read(end-start+2)
  assert len(data)==end-start+1
  ranges.append({'start':start,'end':end,'purpose':purpose,'size':len(data),'sha256':hashlib.sha256(data).hexdigest()})
  return data
 end=get(total-22,total-1,'EOCD only');sig,disk,cddisk,ndisk,n,cdsize,cdoff,comment=struct.unpack('<4s4H2IH',end)
 assert sig==b'PK\x05\x06' and disk==cddisk==0 and ndisk==n and comment==0 and n<1000 and cdsize<262144
 central=get(cdoff,cdoff+cdsize-1,'central directory only');inventory=[];offset=0
 for _ in range(n):
  f=struct.unpack_from('<4s6H3I5H2I',central,offset);assert f[0]==b'PK\x01\x02'
  name=central[offset+46:offset+46+f[10]].decode();inventory.append({'name':name,'compressed_size':f[8],'size':f[9],'crc32':f[7],'method':f[4],'flags':f[3],'local_offset':f[16]});offset+=46+f[10]+f[11]+f[12]
 assert offset==len(central)
 selected={'manifest.json','cleanup/cleanup.json','cleanup/zero-inventory.json','terminal/finalization.json','terminal/final.json','terminal/rehearsal-result.json','terminal/evidence/manifest.json','terminal/diagnosis-window.json','terminal/run-start.json','terminal/receipt.json','terminal/run-plan.json','terminal/handoff.json','terminal/quote.json'}
 receipts=[]
 for item in inventory:
  if item['name'] not in selected:continue
  assert item['size']<262144 and item['compressed_size']<262144 and not item['flags']&1
  local=get(item['local_offset'],item['local_offset']+29,item['name']+' header only');h=struct.unpack('<4s5H3I2H',local);assert h[0]==b'PK\x03\x04'
  start=item['local_offset']+30+h[9]+h[10];comp=get(start,start+item['compressed_size']-1,item['name']+' body only')
  raw=zlib.decompress(comp,-15) if item['method']==8 else comp
  assert item['method'] in [0,8] and len(raw)==item['size'] and zlib.crc32(raw)&0xffffffff==item['crc32'];json.loads(raw)
  f=out/item['name'];f.parent.mkdir(parents=True,exist_ok=True);f.write_bytes(raw);receipts.append({'path':item['name'],'size':len(raw),'sha256':hashlib.sha256(raw).hexdigest(),'crc32_verified':True})
 assert {'manifest.json','cleanup/zero-inventory.json','terminal/finalization.json'}<=set(x['path'] for x in receipts)
 receipt={'artifact_id':a['id'],'api_declared_archive_digest':a['digest'],'full_archive_digest_recomputed':False,'archive_total_bytes':total,'observed_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'selected_members':receipts,'member_inventory':inventory,'http_ranges':ranges,'raw_diagnostic_or_ciphertext_member_bytes_downloaded':False}
 (out/'selection-verification.json').write_text(json.dumps(receipt,indent=2)+'\n');print(json.dumps({'selected_count':len(receipts),'inventory_count':len(inventory),'selected_bytes':sum(x['size'] for x in receipts),'downloaded_range_bytes':sum(x['size'] for x in ranges)}))
except Exception as e:
 (out/'read-failure.json').write_text(json.dumps({'exception_type':type(e).__name__,'ranges':ranges,'no_full_archive_download':True},indent=2)+'\n');print('selected member read failed: '+type(e).__name__);sys.exit(1)
