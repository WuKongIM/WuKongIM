#!/usr/bin/env python3
"""Independently hash the downloaded Action ZIP and all offline tar members; execute nothing."""
import argparse
import hashlib
import json
from pathlib import Path, PurePosixPath
import tarfile
import zipfile

p=argparse.ArgumentParser(description=__doc__)
p.add_argument('--zip',required=True,type=Path)
p.add_argument('--artifact-metadata',required=True,type=Path)
p.add_argument('--source-sha',required=True)
p.add_argument('--control-sha',required=True)
p.add_argument('--output-dir',required=True,type=Path)
p.add_argument('--expected-intent',required=True,type=Path)
a=p.parse_args()
def digest_stream(handle):
    value=hashlib.sha256()
    size=0
    while block:=handle.read(1024*1024):
        value.update(block)
        size+=len(block)
    return value.hexdigest(),size
def strict_pairs(pairs):
    value={}
    for key,item in pairs:
        assert key not in value, 'duplicate JSON key'
        value[key]=item
    return value
def strict_json(raw):
    return json.loads(raw,object_pairs_hook=strict_pairs)
metadata=strict_json(a.artifact_metadata.read_bytes())
with a.zip.open('rb') as handle:
    zip_sha,zip_size=digest_stream(handle)
assert zip_size==metadata['size_in_bytes'], 'Action ZIP size differs'
assert 'sha256:'+zip_sha==metadata['digest'], 'Action ZIP digest differs'
assert not metadata['expired']
seen=set()
regular_paths=set()
records=[]
small={}
elf={}
with zipfile.ZipFile(a.zip) as archive:
    names=archive.namelist()
    expected={'cloud-deployment-bundle.tar.gz','cloud-deployment-bundle.tar.gz.sha256','bundle-manifest-output.json'}
    assert len(names)==len(set(names)) and set(names)==expected, names
    assert all(info.file_size<=1024*1024*1024 for info in archive.infolist())
    assert archive.getinfo('bundle-manifest-output.json').file_size<=1024*1024
    assert archive.getinfo('cloud-deployment-bundle.tar.gz.sha256').file_size<=256
    output_raw=archive.read('bundle-manifest-output.json')
    assert len(output_raw)<=1024*1024
    manifest_output=strict_json(output_raw)
    checksum=archive.read('cloud-deployment-bundle.tar.gz.sha256').decode().split()
    assert len(checksum)==2 and checksum[1]=='cloud-deployment-bundle.tar.gz'
    with archive.open('cloud-deployment-bundle.tar.gz') as handle:
        tar_sha,tar_size=digest_stream(handle)
    assert checksum[0]==tar_sha
    total=0
    with archive.open('cloud-deployment-bundle.tar.gz') as handle, tarfile.open(fileobj=handle,mode='r|gz') as bundle:
        for member in bundle:
            name=member.name
            if name.startswith('./'):
                name=name[2:]
            if name in ('','.'):  # GNU tar root entry
                assert member.isdir()
                continue
            normalized=name.rstrip('/') if member.isdir() else name
            parts=PurePosixPath(normalized).parts
            assert normalized and not normalized.startswith('/')
            assert '\\' not in normalized and all(ord(char)>=32 and ord(char)!=127 for char in normalized)
            assert all(part not in ('.','..') for part in parts)
            assert PurePosixPath(normalized).as_posix()==normalized
            assert normalized not in seen, 'duplicate tar path'
            seen.add(normalized)
            assert member.type in (tarfile.REGTYPE,tarfile.AREGTYPE,tarfile.DIRTYPE) and not member.sparse, 'nonregular tar member'
            assert all('/'.join(parts[:index]) not in regular_paths for index in range(1,len(parts))), 'regular parent path'
            if member.isfile():
                assert not any(value.startswith(normalized+'/') for value in seen), 'regular parent path'
                regular_paths.add(normalized)
            if member.isdir():
                continue
            assert len(records)<2048 and member.size>0
            total+=member.size
            assert total<=2*1024*1024*1024
            file=bundle.extractfile(member)
            value=hashlib.sha256()
            size=0
            captured=bytearray()
            capture=normalized in ('bundle-manifest.json','deployment-intent.json')
            header=b''
            while block:=file.read(1024*1024):
                if not header:
                    header=block[:20]
                value.update(block)
                size+=len(block)
                if capture:
                    assert size<=1024*1024
                    captured.extend(block)
            assert size==member.size
            records.append({'path':normalized,'mode':member.mode,'size':size,'sha256':value.hexdigest()})
            if capture:
                small[normalized]=bytes(captured)
            if normalized.startswith('bin/'):
                elf[normalized]=(len(header)>=20 and header[:4]==b'\x7fELF' and header[4:6]==b'\x02\x01'
                                 and header[7] in (0,3) and int.from_bytes(header[16:18],'little') in (2,3)
                                 and int.from_bytes(header[18:20],'little')==62 and member.mode==0o755)
manifest=strict_json(small['bundle-manifest.json'])
intent=strict_json(small['deployment-intent.json'])
assert manifest==manifest_output
assert manifest['schema']=='wukongim.cloud_deployment.bundle/v1'
assert intent['schema']=='wukongim.cloud_deployment.bundle_intent/v1'
assert small['deployment-intent.json']==a.expected_intent.read_bytes(), 'intent differs from independently reviewed DefaultIntent'
assert manifest['source_sha']==intent['source_sha']==a.source_sha
assert manifest['control_sha']==intent['control_sha']==a.control_sha
assert small['deployment-intent.json']==(json.dumps(intent,indent=2,ensure_ascii=False)+'\n').encode()
assert manifest['intent_sha256']=='sha256:'+hashlib.sha256(small['deployment-intent.json']).hexdigest()
assert next(item for item in records if item['path']=='bundle-manifest.json')['mode']==0o644
inventory=sorted((item for item in records if item['path']!='bundle-manifest.json'),key=lambda item:item['path'])
assert inventory==manifest['files']
ordered=b''.join(f"{item['path']}\0{item['mode']}\0{item['size']}\0{item['sha256']}\n".encode() for item in inventory)
assert manifest['bundle_digest']=='sha256:'+hashlib.sha256(ordered).hexdigest()
assert metadata['name']=='cloud-deployment-bundle-'+manifest['bundle_digest'].removeprefix('sha256:')
assert (intent['operating_system'],intent['operating_system_version'],intent['architecture'],intent['service_nodes'],
        intent['load_nodes'],intent['hash_slots'],intent['workload_slot_groups'],intent['replicas'])==('ubuntu','24.04','amd64',3,1,256,12,3)
expected_binaries={'caddy','node_exporter','prometheus','wkanalysis','wkcli','wkcloudbundle','wkcloudgate','wkcloudhost','wukongim'}
assert set(intent['offline_binaries'])==expected_binaries
assert set(elf)=={'bin/'+name for name in expected_binaries} and all(elf.values())
paths={item['path'] for item in inventory}
assert all(item['owner']=='root' and item['mode']==0o600 and item['path'].startswith('/etc/wukongim/secrets/')
           and item['path'].lstrip('/') not in paths for item in intent['secret_files'])
assert {'assets/demo/index.html','assets/manager/index.html','config/chat-lifecycle.yaml','config/chat-lifecycle-rehearsal.yaml'}<=paths
result={'status':'independent_artifact_bytes_verified','artifact_id':metadata['id'],
        'zip_sha256':zip_sha,'zip_size':zip_size,'tar_sha256':tar_sha,'tar_size':tar_size,
        'source_sha':a.source_sha,'control_sha':a.control_sha,'bundle_digest':manifest['bundle_digest'],
        'manifest_sha256':hashlib.sha256(small['bundle-manifest.json']).hexdigest(),
        'intent_sha256':manifest['intent_sha256'],'manifest_inventory_files':len(inventory),
        'all_regular_tar_files_hashed':len(records),'uncompressed_bytes':total,'binary_elf_checks':elf,
        'no_tar_extraction_or_payload_execution':True,
        'limits':'Independent byte/inventory/content-address and selected intent checks; full static semantics additionally rely on successful trusted runner seal-offline and verify-offline steps. No host, cloud, soak or publication validation.'}
a.output_dir.mkdir()
for name,raw in small.items():
    (a.output_dir/name).write_bytes(raw)
(a.output_dir/'bundle-manifest-output.json').write_bytes(output_raw)
(a.output_dir/'verification.json').write_text(json.dumps(result,indent=2,sort_keys=True)+'\n')
print(json.dumps(result,indent=2,sort_keys=True))
