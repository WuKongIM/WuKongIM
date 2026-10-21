import ast, copy, hashlib, importlib.util, json, os, socket, stat, subprocess, sys, tempfile
from contextlib import ExitStack
from pathlib import Path
from unittest.mock import patch
sys.dont_write_bytecode = True
ROOT = Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/packages-stable-native-clients')
OUT = Path(__file__).parent
REPAIR = Path('/tmp/wukongim-v3-readiness-20261021/packages/rpm-cache-filetype-repair')
BASE = 'de43579a9fa88bf70f64e6dcd926a2ddf6057496'
PARENT = 'dnf-cli-cache/wukongim-stable-5702a8a51b8fa885/pubring'
NAMES = ('S.gpg-agent', 'S.gpg-agent.browser', 'S.gpg-agent.extra', 'S.gpg-agent.ssh', 'S.scdaemon')
checks=[]
def check(name, condition):
    assert condition, name
    checks.append(name)
def git(*args): return subprocess.check_output(['git','-C',str(ROOT),*args])
def sha(data): return hashlib.sha256(data).hexdigest()
source=REPAIR/'source-freeze.json'; freeze=json.loads(source.read_text())
check('author freeze sha', sha(source.read_bytes())=='551a8954bdaabc7817a8cd41f6a3f0658dc85e9c89c79b81488bcaaea2a11877')
check('base HEAD', git('rev-parse','HEAD').decode().strip()==BASE)
check('no untracked', not git('ls-files','--others','--exclude-standard'))
files={name:{'sha256':sha((ROOT/name).read_bytes()),'size':(ROOT/name).stat().st_size} for name in freeze['files']}
check('four frozen files', files==freeze['files'])
diff=git('diff','HEAD')
check('diff sha',sha(diff)==freeze['diff_sha256']=='d46227c5c27e57f4588704e4d9114609881a36f2f29e4fc37fcf9fef6ff43ec6')
changed=git('diff','--name-only','HEAD').decode().splitlines()
check('only intended four files',set(changed)==set(files))
original=git('show',BASE+':tests/native_stable_clients_drill.py').decode()
current=(ROOT/'tests/native_stable_clients_drill.py').read_text()
def funcs(text): return {n.name:ast.dump(n,include_attributes=False) for n in ast.parse(text).body if isinstance(n,(ast.FunctionDef,ast.ClassDef))}
a,b=funcs(original),funcs(current)
check('no function added removed',a.keys()==b.keys())
check('only cache_inventory function changed',[name for name in a if a[name]!=b[name]]==['cache_inventory'])
unchanged=[name for name in a if a[name]==b[name]]
protected=[]
for item in freeze['protected']:
    name=item['path']; now=(ROOT/name).read_bytes(); before=git('show',BASE+':'+name)
    check('protected byte identity '+name,now==before and sha(now)==item['sha256'])
    protected.append({'path':name,'sha256':sha(now)})
url='https://packages.githubim.com/rpm/stable/el/9/x86_64/mirrorlist'
check('canonical URL exact parent hash',sha(url.encode())[:16]=='5702a8a51b8fa885')
spec=importlib.util.spec_from_file_location('review_cache',ROOT/'tests/native_stable_clients_drill.py'); mod=importlib.util.module_from_spec(spec); spec.loader.exec_module(mod)
check('exact allowlist',mod.DNF_PUBRING==PARENT and mod.DNF_IPC_NAMES==frozenset(NAMES))
def bind(stack,path,live=True):
    path.parent.mkdir(parents=True,exist_ok=True)
    s=stack.enter_context(socket.socket(socket.AF_UNIX,socket.SOCK_STREAM)); s.bind(str(path))
    if not live:s.close()
    return s
def rejected(root,label):
    with patch.object(Path,'read_bytes',side_effect=AssertionError('unexpected read')):
        try:mod.cache_inventory(root)
        except RuntimeError as e:
            message=str(e); check(label, 'cache' in message and 'mode=' in message and 'nlink=' in message and 'size=' in message and str(root) not in message and '\n' not in message and len(message)<=400);return message
        raise AssertionError(label+' accepted')
for live in (True,False):
    with tempfile.TemporaryDirectory(dir='/tmp',prefix='sr-') as tmp, ExitStack() as stack:
        root=Path(tmp); nodes=[]
        for name in NAMES:
            path=root/PARENT/name;bind(stack,path,live);nodes.append((path,path.lstat()))
        result=mod.cache_inventory(root,arm=True)
        check('live=%s sockets excluded'%live,result=={})
        for reuse in (False,True):
            try:mod.cache_evidence({},result,require_reuse=reuse)
            except RuntimeError as e:check('live=%s reuse=%s no fake metadata'%(live,reuse),'nonempty metadata' in str(e))
            else:raise AssertionError('socket-only accepted')
        for path,old in nodes:
            now=path.lstat();check('live=%s untouched %s'%(live,path.name),all(getattr(now,f)==getattr(old,f) for f in ('st_mode','st_nlink','st_size','st_ino','st_mtime_ns','st_atime_ns')))
        name='dnf-cli-cache/repo/repodata/primary.xml'; path=root/name;path.parent.mkdir(parents=True);path.write_bytes(b'existing signed metadata test bytes')
        before=mod.cache_inventory(root,arm=True);after=mod.cache_inventory(root)
        try:mod.cache_evidence(before,after,require_reuse=True)
        except RuntimeError as e:check('live=%s no unread reuse'%live,'read of existing metadata' in str(e))
        else:raise AssertionError('unread reuse accepted')
        before=mod.cache_inventory(root,arm=True);path.read_bytes();after=mod.cache_inventory(root)
        check('live=%s actual OS read reuse'%live,mod.cache_evidence(before,after,require_reuse=True)['metadata_reads']==[name])
        for field in ('device','inode','sha256','size'):
            changed=copy.deepcopy(after);changed[name][field]='f'*64 if field=='sha256' else changed[name][field]+1
            try:mod.cache_evidence(before,changed,require_reuse=True)
            except RuntimeError:check('live=%s reject modified %s'%(live,field),True)
            else:raise AssertionError('mutated reuse accepted')
for relative in ('S.gpg-agent','apt/lists/S.gpg-agent','dnf-cache/wukongim-stable-5702a8a51b8fa885/pubring/S.gpg-agent','dnf-cli-cache/wukongim-stable-aaa08530d5caaef3/pubring/S.gpg-agent',PARENT+'/nested/S.gpg-agent',PARENT+'/S.gpg-agent.foo','dnf-cli-cache/repo/repodata/S.gpg-agent'):
    with tempfile.TemporaryDirectory(dir='/tmp',prefix='sr-') as tmp,ExitStack() as stack:
        root=Path(tmp);bind(stack,root/relative);rejected(root,'reject socket '+relative)
for kind in ('fifo','symlink','regular-hardlink','socket-hardlink','regular','large-regular'):
    with tempfile.TemporaryDirectory(dir='/tmp',prefix='sr-') as tmp,ExitStack() as stack:
        root=Path(tmp);p=root/PARENT/'S.gpg-agent';p.parent.mkdir(parents=True)
        if kind=='fifo':os.mkfifo(p)
        elif kind=='symlink':p.symlink_to('/no-such-spec-review-target')
        elif kind=='regular-hardlink':p.write_bytes(b'guard');os.link(p,p.with_name('S.gpg-agent.extra'))
        elif kind=='socket-hardlink':bind(stack,p);os.link(p,p.with_name('S.gpg-agent.extra'))
        else:
            p.write_bytes(b'ordinary file named socket')
            if kind=='large-regular':
                with p.open('r+b') as f:f.truncate(64*1024*1024+1)
        if kind=='regular':check('ordinary same-name hashed',mod.cache_inventory(root)[PARENT+'/S.gpg-agent']['sha256']==sha(b'ordinary file named socket'))
        else:rejected(root,'reject '+kind)
for relative in ('odd\nname',('x'*100+'/')*3+'fifo'):
    with tempfile.TemporaryDirectory(dir='/tmp',prefix='sr-') as tmp:
        root=Path(tmp);p=root/relative;p.parent.mkdir(parents=True,exist_ok=True);os.mkfifo(p)
        message=rejected(root,'bounded escaped diagnostic '+repr(relative));check('relative repr retained '+repr(relative),repr(relative)[:240] in message)
receipt={'base':BASE,'status':'pass','checks':checks,'check_count':len(checks),'source_freeze_sha256':sha(source.read_bytes()),'patch_sha256':sha(diff),'files':files,'unchanged_driver_functions':unchanged,'protected':protected,'real_socket_hardlink_test':'passed without skip','scope':'Small independent local AF_UNIX and filesystem probes; no Docker/network/private keys/full suite/native49','source_unchanged_after':all((ROOT/name).read_bytes() and sha((ROOT/name).read_bytes())==facts['sha256'] for name,facts in files.items())}
(OUT/'probe-results.json').write_text(json.dumps(receipt,indent=2)+'\n')
(OUT/'reviewed.patch').write_bytes(diff)
print(json.dumps({'status':'pass','check_count':len(checks),'four_files_verified':len(files),'protected_files_verified':len(protected),'unchanged_driver_functions':len(unchanged),'source_unchanged_after':receipt['source_unchanged_after']},indent=2))
