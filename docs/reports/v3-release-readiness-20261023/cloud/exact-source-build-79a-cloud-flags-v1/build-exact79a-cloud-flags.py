import datetime, hashlib, json, os, subprocess, tarfile, zipfile
from pathlib import Path
B = Path('/tmp/wukongim-v3-readiness-20261023/cloud')
SRC = Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-exact-source-79a')
OUT = B / 'exact-source-build-79a-cloud-flags-v1'
OUT.mkdir(exist_ok=False)
GO = '/Users/tt/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.25.11.darwin-arm64/bin/go'
audit = json.loads((B/'local-binary-provenance-audit-v1/cloud-binary-buildinfo.json').read_text())
sha = lambda x: hashlib.sha256(x).hexdigest()
assert sha(Path(audit['zip']['path']).read_bytes()) == audit['zip']['sha256']
assets = []
with zipfile.ZipFile(audit['zip']['path']) as z:
    with z.open(audit['tar']['member']) as f:
        with tarfile.open(fileobj=f, mode='r|gz') as t:
            for m in t:
                n=m.name.removeprefix('./')
                for prefix, target in [('assets/manager/', 'internal/access/manager/webui/dist/'), ('assets/demo/', 'internal/access/api/demoui/dist/')]:
                    if n.startswith(prefix) and m.isfile():
                        assert m.size < 8*1024*1024
                        data=t.extractfile(m).read(m.size+1)
                        relative=target+n[len(prefix):]
                        assert '..' not in Path(relative).parts
                        actual=(SRC/relative).read_bytes()
                        assets.append(dict(bundle_path=n, source_path=relative, bytes=len(data), sha256=sha(data), byte_equal=actual==data))
assert len(assets)==76 and all(x['byte_equal'] for x in assets)
(OUT/'embedded-assets-comparison.json').write_text(json.dumps(assets,indent=2)+'\n')
env={k:v for k,v in os.environ.items() if not k.startswith(('GO','GIT_','WK_')) and k!='CGO_ENABLED'}
env.update(GOWORK='off',GOTOOLCHAIN='local',GOFLAGS='',GOPROXY='off',GOSUMDB='off',CGO_ENABLED='0',GOMAXPROCS='2')
events=[]
for target in ('darwin-arm64','linux-amd64'):
    osname,arch=target.split('-');dest=OUT/target;dest.mkdir()
    e=dict(env,GOOS=osname,GOARCH=arch)
    if arch=='amd64':e['GOAMD64']='v1'
    for name in ('wukongim','wkcli'):
        binary=dest/name
        argv=[GO,'build','-p=2','-trimpath','-buildvcs=true','-mod=readonly','-ldflags=-s -w -buildid=','-o',str(binary),'./cmd/'+name]
        started=datetime.datetime.now(datetime.timezone.utc).isoformat()
        p=subprocess.run(argv,cwd=SRC,env=e,text=True,capture_output=True,timeout=120)
        (OUT/(target+'-'+name+'-build.stdout')).write_text(p.stdout)
        (OUT/(target+'-'+name+'-build.stderr')).write_text(p.stderr)
        events.append(dict(argv=argv,cwd=str(SRC),started_at=started,exit_status=p.returncode,GOOS=osname,GOARCH=arch))
        (OUT/'commands.json').write_text(json.dumps(events,indent=2)+'\n')
        assert p.returncode==0
        info=subprocess.check_output([GO,'version','-m',str(binary)],env=env,text=True)
        (OUT/(target+'-'+name+'-buildinfo.txt')).write_text(info)
        assert 'vcs.revision=79a7177032a6ae1ec7015cefe0e6f94eb0f19f06' in info and 'vcs.modified=false' in info
        row=dict(platform=target,binary=name,sha256=sha(binary.read_bytes()),bytes=binary.stat().st_size,linkflags='-s -w -buildid=',source_revision='79a7177032a6ae1ec7015cefe0e6f94eb0f19f06',compiler='go1.25.11',CGO_ENABLED=0,trimpath=True)
        if target=='linux-amd64':
            cloud=B/'local-binary-provenance-audit-v1/PRIVATE_extracted-cloud'/name
            row['cloud_sha256']=sha(cloud.read_bytes());row['cloud_byte_equal']=row['sha256']==row['cloud_sha256']
        (OUT/(target+'-'+name+'-identity.json')).write_text(json.dumps(row,indent=2)+'\n')
        print(json.dumps(row),flush=True)
status=subprocess.check_output(['git','status','--porcelain=v1','--untracked-files=all'],cwd=SRC,text=True)
(OUT/'source-after.stdout').write_text(status);assert not status
(OUT/'completed.json').write_text(json.dumps(dict(completed_at=datetime.datetime.now(datetime.timezone.utc).isoformat(),binary_count=4,source_clean=True,tracked_embedded_assets_equal=76,runtime_started=False),indent=2)+'\n')
