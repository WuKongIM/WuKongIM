#!/usr/bin/env python3
"""Read only exact current-task paths; write this task's disk audit receipts."""
import datetime
import hashlib
import json
import math
import os
from pathlib import Path
import subprocess
import zipfile
import zlib

OUT = Path(__file__).resolve().parent
REPO = Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM')
ROOTS = [Path('/tmp') / n for n in (
    'wukongim-v3-readiness-20261021', 'wukongim-performance-fixed-20261020',
    'wukongim-cpu-causal-20261021', 'wukongim-performance-followup-20261020',
    'wukongim-v3-rc-20261020', 'wukongim-cpu-placement-20261021')]

def sha(path):
    h = hashlib.sha256()
    with path.open('rb') as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b''):
            h.update(chunk)
    return h.hexdigest()

def size(path):
    return int(subprocess.check_output(['du', '-sk', str(path)], text=True).split()[0]) * 1024

copies = []
for root in ROOTS[1:3]:
    artifact = json.loads((root / 'artifacts.json').read_text())['artifacts'][0]
    archive = root / 'artifact.zip'
    actual_sha = sha(archive)
    checks = []
    with zipfile.ZipFile(archive) as z:
        bad_zip_member = z.testzip()
        entries = {i.filename: i for i in z.infolist() if not i.is_dir()}
        for f in sorted((root / 'download').rglob('*')):
            if not f.is_file():
                continue
            relative = f.relative_to(root / 'download').as_posix()
            entry = entries.get(relative)
            crc = 0
            with f.open('rb') as stream:
                for chunk in iter(lambda: stream.read(1024 * 1024), b''):
                    crc = zlib.crc32(chunk, crc)
            checks.append({'path': relative, 'size': f.stat().st_size,
                           'matches_retained_archive': bool(entry and entry.file_size == f.stat().st_size and entry.CRC == crc)})
    copies.append({'candidate_path': str(root / 'download'), 'allocated_bytes': size(root / 'download'),
                   'retained_copy': str(archive), 'retained_copy_bytes': archive.stat().st_size,
                   'sha256': actual_sha, 'matches_recorded_api_sha256': actual_sha == artifact['digest'].removeprefix('sha256:'),
                   'zip_crc_pass': bad_zip_member is None, 'download_files_match_archive': all(x['matches_retained_archive'] for x in checks),
                   'files_checked': len(checks), 'files': checks, 'artifact_id': artifact['id'],
                   'recorded_remote_expiry': artifact['expires_at'], 'remote_reavailability_rechecked': False,
                   'caveat': 'Retain archive and receipts; verify no active reader before any later cleanup. No cleanup authorized or performed here.'})

caches = []
for relative, receipt in (
    ('local/go-cache', 'local/missing-cache-build-retry.json'),
    ('replication-diagnosis/gocache', 'replication-diagnosis/commands.json')):
    p = ROOTS[0] / relative
    caches.append({'path': str(p), 'allocated_bytes': size(p), 'ownership_receipt': str(ROOTS[0] / receipt),
                   'rebuildable': True, 'retained_byte_copy_verified': False,
                   'test_evidence_outside_cache': True, 'active_user_check_performed': False,
                   'caveat': 'Task-specific compilation cache only; rebuilding costs CPU/time/space. Must establish quiescence before any later deletion; shared Go cache excluded.'})

history = json.loads((ROOTS[0] / 'owned-temp-cleanup.json').read_text())
absent = [{'path': x['path'], 'exists_now': Path(x['path']).exists(), 'historical_bytes_not_counted': x['bytes']} for x in history]
absent.append({'path': str(ROOTS[0] / 'mqtt-diagnosis/gocache'), 'exists_now': (ROOTS[0] / 'mqtt-diagnosis/gocache').exists()})
totals = [{'path': str(p), 'allocated_bytes': size(p)} for p in ROOTS]
stat = os.statvfs('/tmp')
capacity = stat.f_blocks * stat.f_frsize
available = stat.f_bavail * stat.f_frsize
needed = max(0, math.ceil(capacity / 20) - available)
upper = sum(x['allocated_bytes'] for x in totals)
candidate = sum(x['allocated_bytes'] for x in caches + copies)
source_files = ['configs/wkbench/chat-lifecycle/local-shakeout.yaml', 'docs/superpowers/runbooks/2026-08-04-chat-lifecycle-soak.md']
data = {
    'schema': 'wukongim/task-disk-readonly-audit/v1',
    'observed_at': datetime.datetime.now(datetime.timezone.utc).isoformat(),
    'source_sha': subprocess.check_output(['git', '-C', str(REPO), 'rev-parse', 'HEAD'], text=True).strip(),
    'source_sha256': {p: sha(REPO / p) for p in source_files},
    'scope': 'Exact six task output roots and explicitly receipted dedicated caches only. No shared cache, other worktree, user-directory, or whole-disk scan.',
    'df_k': subprocess.check_output(['df', '-k', '/tmp', str(REPO)], text=True),
    'filesystem': {'capacity_bytes': capacity, 'available_bytes': available, 'available_percent': available / capacity * 100,
                   'reserve_percent': 5, 'additional_bytes_to_reserve_only': needed, 'run_headroom_bytes': None,
                   'headroom_status': 'Not estimated: cannot reach reserve even with all scoped output counted; reserve alone is insufficient to claim run admission.'},
    'task_root_totals': totals, 'all_scoped_outputs_loose_upper_bound_bytes': upper,
    'shortfall_after_loose_upper_bound_bytes': max(0, needed - upper),
    'rebuildable_cache_candidates': caches, 'verified_archive_extract_candidates': copies,
    'candidate_estimated_allocated_bytes': candidate,
    'previously_removed_paths': absent,
    'measurement_limits': ['du allocated bytes are not guaranteed reclaim on APFS snapshots/clones/hard links.', 'Disk use can change concurrently.', 'No process liveness or remote artifact retention query was performed.', 'Root totals include unique evidence and are only an optimistic arithmetic bound, not a cleanup allowlist.'],
    'status': 'blocked_disk_reserve_unsatisfied', 'cleanup_performed': False, 'load_started': False, 'threshold_changed': False,
}
(OUT / 'audit.json').write_text(json.dumps(data, ensure_ascii=False, indent=2) + '\n')
rows = '\n'.join(f"| `{x.get('path', x.get('candidate_path'))}` | {x['allocated_bytes']/1e9:.3f} GB |" for x in caches + copies)
md = f'''# 本任务磁盘只读复核

结论：本任务可识别的可重建文件不足以解除本地 30 分钟 shakeout 阻塞；没有删除文件、迁移 NAS、启动负载或修改 5% 门槛。

测量时间：{data['observed_at']}。`statvfs(/tmp)` 容量 {capacity:,} bytes，可用 {available:,} bytes（{available/1e9:.3f} GB，{available/capacity*100:.4f}%）。达到 5% 本身还需 {needed:,} bytes（{needed/1e9:.3f} GB），尚未包含测试运行余量。

仅统计六个此前收据明确记录的本任务输出目录，总占用 {upper:,} bytes（{upper/1e9:.3f} GB）。即使把其中全部验证证据也错误地算成可回收，仍差 {max(0,needed-upper)/1e9:.3f} GB；因此无需扩大扫描或试跑来判断是否足够。

下表只是未来清理的候选，不是删除授权。缓存可重建；两个 `download` 已逐文件核对保留 ZIP 的大小和 CRC，ZIP 的 SHA256 与既有 API 收据相符且 ZIP CRC 完整。必须保留 ZIP 和验证收据，任何实际清理前还须排除活动读者。

| 精确候选路径 | 当前分配占用 |
| --- | ---: |
{rows}

上述候选合计 {candidate/1e9:.3f} GB。缓存无已验证逐字副本，但构建命令及测试输出保留在缓存目录外；重建会再次耗用空间与时间。远程 artifact 的既有记录未过期（到 2027-01-18），本轮未联网确认可再下载，结论依赖已校验本地 ZIP。其余报告、原始日志、失败窗口和未确认副本的二进制保留，不列为可删除项。

历史 `owned-temp-cleanup.json` 六个路径及 mqtt 独立缓存现均不存在，不重复计算历史已释放字节。未查看或触碰共享 Go 缓存、其他任务 worktree、其他用户目录；未扫描全盘。

门槛来源：当前源码 `{data['source_sha']}`，`configs/wkbench/chat-lifecycle/local-shakeout.yaml:71` 固定 `disk_safe_stop_free_percent: 5`；runbook 第 130–132 行规定低于 5% 的 `disk_free` 是前置失败，第 274–277 行禁止降低门槛强行准入。既有 `cloud/local-shakeout-preconditions.json` 已记录同一阻塞。本次是主机前置检查，不能报告为 shakeout 完成或通过。

`du` 是分配块统计，APFS 快照、克隆或硬链接可能让实际回收量更小；可用空间也可能被并发任务改变。未估计运行余量，因为连 5% 基线都无法由此范围满足。详细文件副本核验、路径来源、数量和限制见 `audit.json`；复核脚本 `audit.py` 仅检查固定任务路径。
'''
(OUT / 'README.md').write_text(md)
print(json.dumps({'output':str(OUT), 'available_percent':available/capacity*100, 'additional_bytes_to_5_percent':needed, 'all_scoped_outputs_bytes':upper,'candidate_bytes':candidate,'status':data['status']}, ensure_ascii=False))
