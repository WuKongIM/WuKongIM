#!/usr/bin/env python3
"""Verify a small protected-workflow public JSON artifact without executing content."""
import argparse,hashlib,json,zipfile
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--zip',type=Path,required=True);p.add_argument('--metadata',type=Path,required=True);p.add_argument('--output-dir',type=Path,required=True);p.add_argument('--allowed',nargs='+',required=True);a=p.parse_args()
m=json.loads(a.metadata.read_bytes());raw=a.zip.read_bytes();assert len(raw)==m['size_in_bytes']<10*1024*1024;assert 'sha256:'+hashlib.sha256(raw).hexdigest()==m['digest'];assert not m['expired'];records=[];payloads={}
def pairs(items):
 out={}
 for k,v in items:assert k not in out;out[k]=v
 return out
with zipfile.ZipFile(a.zip) as z:
 names=z.namelist();assert len(names)==len(set(names));assert set(names)<=set(a.allowed)
 for n in names:
  info=z.getinfo(n);assert not info.is_dir() and info.file_size<=2*1024*1024
  b=z.read(n);assert len(b)==info.file_size
  assert b'-----BEGIN' not in b
  if b:
   value=json.loads(b,object_pairs_hook=pairs);assert isinstance(value,dict)
  else:value=None
  records.append({'name':n,'size':len(b),'sha256':hashlib.sha256(b).hexdigest(),'schema':value.get('schema') if value else None,'empty':not b});payloads[n]=b
 assert z.testzip() is None
a.output_dir.mkdir()
for n,b in payloads.items():(a.output_dir/n).write_bytes(b)
receipt={'artifact_id':m['id'],'api_size_digest_and_zip_crc_pass':True,'members':records,'no_payload_execution':True}
(a.output_dir/'verification.json').write_text(json.dumps(receipt,indent=2)+'\n');print(json.dumps(receipt))
