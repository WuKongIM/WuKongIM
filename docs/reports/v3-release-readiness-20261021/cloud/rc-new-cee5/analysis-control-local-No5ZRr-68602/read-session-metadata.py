"""Read only ZIP directory and the public session.json member, never encrypted payloads."""
from pathlib import Path
import subprocess,json,hashlib,datetime,re,struct,urllib.request,zlib,sys
b=Path(__file__).parent
p=b.parent.parent/'rc-new-cee5-private'/'analysis-control-local-No5ZRr-68602'
a=next(a for a in json.loads((b/'poll-02/prepare-artifacts.json').read_text())['artifacts'] if a['id']==12366282899)
out=b/'39644107534/artifact-12366282899';out.mkdir()
r=subprocess.run(['gh','api','--method','HEAD','--include',f"repos/WuKongIM/WuKongIM/actions/artifacts/{a['id']}/zip"],capture_output=True,timeout=45)
for n,data in [('headers',r.stdout),('stderr',r.stderr)]:
 f=p/('session-12366282899.'+n);f.write_bytes(data);f.chmod(0o600)
assert r.returncode==0
matches=re.findall(rb'(?im)^location:\s*(https://[^\r\n]+)',r.stdout)
assert matches
url=matches[0].decode();total=a['size_in_bytes'];ranges=[]
def get(start,end,purpose):
 assert 0<=start<=end<total and end-start<65536
 req=urllib.request.Request(url,headers={'Range':f'bytes={start}-{end}','Accept-Encoding':'identity'})
 with urllib.request.urlopen(req,timeout=30) as response:
  assert response.status==206,'range response must be206; no full body consumed'
  assert response.headers['Content-Range']==f'bytes {start}-{end}/{total}'
  data=response.read(end-start+2)
 assert len(data)==end-start+1
 ranges.append({'start':start,'end':end,'purpose':purpose,'size':len(data),'sha256':hashlib.sha256(data).hexdigest()})
 return data
try:
 end=get(total-22,total-1,'EOCD only')
 sig,disk,cddisk,ndisk,n,cdsize,cdoff,comment=struct.unpack('<4s4H2IH',end)
 assert sig==b'PK\x05\x06' and disk==cddisk==0 and ndisk==n and comment==0 and n<20 and cdsize<65536
 central=get(cdoff,cdoff+cdsize-1,'central directory only')
 inventory=[];offset=0
 for _ in range(n):
  fields=struct.unpack_from('<4s6H3I5H2I',central,offset);assert fields[0]==b'PK\x01\x02'
  name=central[offset+46:offset+46+fields[10]].decode();inventory.append({'name':name,'compressed_size':fields[8],'size':fields[9],'crc32':fields[7],'method':fields[4],'flags':fields[3],'local_offset':fields[16]})
  offset+=46+fields[10]+fields[11]+fields[12]
 assert offset==len(central)
 item=next(i for i in inventory if i['name']=='session.json');assert item['size']<65536 and item['compressed_size']<65536 and not item['flags']&1
 local=get(item['local_offset'],item['local_offset']+29,'session local header only');h=struct.unpack('<4s5H3I2H',local);assert h[0]==b'PK\x03\x04'
 data_start=item['local_offset']+30+h[9]+h[10]
 compressed=get(data_start,data_start+item['compressed_size']-1,'session.json compressed bytes only')
 raw=zlib.decompress(compressed,-15) if item['method']==8 else compressed
 assert item['method'] in [0,8] and len(raw)==item['size'] and zlib.crc32(raw)&0xffffffff==item['crc32']
 session=json.loads(raw);assert session['request_id']=='local-No5ZRr-68602'
 (out/'session.json').write_bytes(raw)
 receipt={'artifact_id':a['id'],'artifact_name':a['name'],'api_declared_archive_digest':a['digest'],'full_archive_digest_recomputed':False,'observed_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'selected_member':'session.json','selected_size':len(raw),'selected_sha256':hashlib.sha256(raw).hexdigest(),'selected_crc32_verified':True,'encrypted_token_bytes_downloaded':False,'other_member_bytes_downloaded':False,'member_inventory':inventory,'http_ranges':ranges}
 (out/'range-verification.json').write_text(json.dumps(receipt,indent=2)+'\n')
 print(json.dumps({'selected':receipt['selected_member'],'state':session['state'],'source_sha':session.get('source_sha'),'expires_at':session.get('expires_at'),'bytes':len(raw),'ranges':len(ranges)}))
except Exception as e:
 (out/'range-failure.json').write_text(json.dumps({'exception_type':type(e).__name__,'ranges':ranges},indent=2)+'\n')
 print('Public session metadata extraction failed: '+type(e).__name__)
 sys.exit(1)
