import datetime
import hashlib
import json
import os
from pathlib import Path
import subprocess

PRIMARY = Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM')
SOURCE = PRIMARY / '.worktrees/v3-rc-exact-source-79a'
OUT = Path('/tmp/wukongim-v3-readiness-20261023/cloud/exact-source-build-79a-v1')
REV = '79a7177032a6ae1ec7015cefe0e6f94eb0f19f06'
GO = Path('/Users/tt/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.25.11.darwin-arm64/bin/go')
OUT.mkdir(exist_ok=False)
env = {k: v for k, v in os.environ.items() if not k.startswith(('GO', 'GIT_', 'WK_')) and k not in ('CGO_ENABLED',)}
env.update(GOWORK='off', GOTOOLCHAIN='local', GOFLAGS='', GOPROXY='off', GOSUMDB='off', CGO_ENABLED='0', GOMAXPROCS='2')
events = []

def now():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()

def run(argv, name, cwd=PRIMARY, overrides=None, timeout=300):
    execution_env = dict(env)
    execution_env.update(overrides or {})
    start = now()
    result = subprocess.run([str(x) for x in argv], cwd=cwd, env=execution_env, text=True, capture_output=True, timeout=timeout)
    (OUT / (name + '.stdout')).write_text(result.stdout)
    (OUT / (name + '.stderr')).write_text(result.stderr)
    row = dict(name=name, argv=[str(x) for x in argv], cwd=str(cwd), started_at=start, finished_at=now(), exit_status=result.returncode, overrides=overrides or {})
    events.append(row)
    (OUT / 'commands.json').write_text(json.dumps(events, indent=2) + '\n')
    print(json.dumps(row), flush=True)
    if result.returncode:
        raise RuntimeError('command_failed:' + name)
    return result.stdout

assert GO.is_file()
assert not SOURCE.exists()
run(['git', 'clone', '--shared', '--no-checkout', '--no-tags', str(PRIMARY), str(SOURCE)], 'clone')
assert (SOURCE / '.git').is_dir()
run(['git', 'checkout', '--detach', REV], 'checkout', SOURCE)
assert run(['git', 'rev-parse', 'HEAD'], 'source-head', SOURCE).strip() == REV
assert not run(['git', 'status', '--porcelain=v1', '--untracked-files=all'], 'source-before', SOURCE)
version = run([GO, 'version'], 'go-version', SOURCE)
assert 'go1.25.11 darwin/arm64' in version
settings = json.loads(run([GO, 'env', '-json', 'GOVERSION', 'GOROOT', 'GOMOD', 'GOWORK', 'GOFLAGS', 'GOCACHE', 'GOMODCACHE', 'GOTOOLCHAIN', 'GOPROXY', 'GOSUMDB', 'CGO_ENABLED', 'GOOS', 'GOARCH'], 'go-env', SOURCE))
assert Path(settings['GOMOD']) == SOURCE / 'go.mod'
assert settings['GOVERSION'] == 'go1.25.11'
assert settings['GOWORK'] == 'off'
tracked = run(['git', 'ls-files', '-z'], 'tracked-source-paths', SOURCE).split('\0')
source_hashes = {p: hashlib.sha256((SOURCE / p).read_bytes()).hexdigest() for p in tracked if p and (p.endswith('.go') or p.endswith('AGENTS.md') or p.endswith('FLOW.md') or p in ('go.mod', 'go.sum'))}
(OUT / 'source-hashes.json').write_text(json.dumps(source_hashes, indent=2, sort_keys=True) + '\n')
(OUT / 'construction-plan.json').write_text(json.dumps(dict(source_revision=REV, source_root=str(SOURCE), real_git_directory=True, layout_deviation_reason='Go1.25.11 skips the regular-file .git of nested worktrees and stamps the enclosing repository; a clean independent local clone preserves a real .git directory.', compiler_sha256=hashlib.sha256(GO.read_bytes()).hexdigest(), object_cache_reuse=True, prebuilt_executable_reuse=False, network_disabled=True, platforms=['darwin/arm64', 'linux/amd64'], runtime_started=False), indent=2) + '\n')
for platform in ('darwin-arm64', 'linux-amd64'):
    target_os, target_arch = platform.split('-')
    bin_dir = OUT / platform
    bin_dir.mkdir()
    for command in ('wukongim', 'wkcli'):
        binary = bin_dir / command
        build_env = dict(GOOS=target_os, GOARCH=target_arch)
        if target_arch == 'amd64':
            build_env['GOAMD64'] = 'v1'
        run([GO, 'build', '-p=2', '-trimpath', '-buildvcs=true', '-mod=readonly', '-o', binary, './cmd/' + command], platform + '-' + command + '-build', SOURCE, build_env)
        info = run([GO, 'version', '-m', binary], platform + '-' + command + '-buildinfo', SOURCE)
        assert 'vcs.revision=' + REV in info
        assert 'vcs.modified=false' in info
        assert 'CGO_ENABLED=0' in info and '-trimpath=true' in info
        run([GO, 'tool', 'buildid', binary], platform + '-' + command + '-buildid', SOURCE)
        digest = hashlib.sha256(binary.read_bytes()).hexdigest()
        (OUT / (platform + '-' + command + '-identity.json')).write_text(json.dumps(dict(sha256=digest, bytes=binary.stat().st_size, source_revision=REV, buildinfo_verified=True), indent=2) + '\n')
assert not run(['git', 'status', '--porcelain=v1', '--untracked-files=all'], 'source-after', SOURCE)
(OUT / 'completed.json').write_text(json.dumps(dict(completed_at=now(), source_revision=REV, commands=len(events), binary_count=4, runtime_started=False), indent=2) + '\n')
print('EXACT_SOURCE_BUILD_COMPLETE', flush=True)
