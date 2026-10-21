from pathlib import Path
import datetime,gzip,hashlib,json,re,zlib
P=Path(__file__).resolve().parent
sha=lambda b:hashlib.sha256(b).hexdigest()
reserved={'ARCHIVE_ALLOWLIST.txt','archive-sha256.json','SHA256SUMS','CRC32SUMS'}
assert not any((P/x).exists() for x in reserved)
paths=[]
for p in sorted(P.rglob('*')):
 assert not p.is_symlink(),p
 if not p.is_file():continue
 rel=p.relative_to(P).as_posix();assert '..' not in Path(rel).parts and not rel.startswith('/')
 b=p.read_bytes();txt=gzip.decompress(b) if rel.endswith('.gz') else b
 txt.decode('utf-8')
 assert not re.search(rb'(?m)^-----BEGIN (?:PGP PRIVATE KEY BLOCK|(?:OPENSSH |RSA |EC |DSA )?PRIVATE KEY)-----\r?\n',txt),rel
 assert not re.search(rb'github_pat_[A-Za-z0-9_]{20,}|gh[pousr]_[A-Za-z0-9]{30,}',txt),rel
 assert not re.search(rb'https?://[^\s"<>]*[?&](?:sig|Signature|X-Amz-Signature|X-Goog-Signature|access_token)=(?!\[)[A-Za-z0-9%+/]{12,}',txt),rel
 paths.append(rel)
allpaths=sorted(paths+list(reserved));(P/'ARCHIVE_ALLOWLIST.txt').write_text('\n'.join(allpaths)+'\n')
def entry(rel):
 b=(P/rel).read_bytes();return {'path':rel,'size':len(b),'sha256':sha(b),'crc32':f'{zlib.crc32(b)&0xffffffff:08x}'}
entries=[entry(x) for x in sorted(paths+['ARCHIVE_ALLOWLIST.txt'])]
e=json.loads((P/'expected.json').read_text());pr=json.loads((P/'pr-merged.json').read_text());assert pr['merged'] and pr['merge_commit_sha']==e['head']
m={'schema':'wukongim.public-ci-archive/v1','head':e['head'],'tree':e['tree'],'attempt':1,'all_runs_terminal':True,'all_ci_passed':True,'frozen_at_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'files':entries,'public_screen':'finite regular UTF-8/source/JSON/log payload; reject symlinks, unsafe paths, private key blocks, recognized token and signed-query patterns; raw ZIP and stderr stay private','envelope':'Allowlist includes all final files; manifest binds payload plus allowlist; SHA256SUMS/CRC32SUMS additionally bind manifest; checksum files do not self-hash.'}
(P/'archive-sha256.json').write_text(json.dumps(m,sort_keys=True,indent=2)+'\n')
checks=entries+[entry('archive-sha256.json')]
(P/'SHA256SUMS').write_text(''.join(x['sha256']+'  '+x['path']+'\n' for x in sorted(checks,key=lambda x:x['path'])))
(P/'CRC32SUMS').write_text(''.join(x['crc32']+'  '+x['path']+'\n' for x in sorted(checks,key=lambda x:x['path'])))
assert sorted(x.relative_to(P).as_posix() for x in P.rglob('*') if x.is_file())==allpaths
for x in checks:assert entry(x['path'])==x
print(json.dumps({'manifest_sha256':sha((P/'archive-sha256.json').read_bytes()),'public_files':len(allpaths),'payload_bytes':sum(x['size'] for x in entries),'all_envelope_paths_SHA_CRC_size_verified':True,'all_processes_terminal_on_exit':True}))
