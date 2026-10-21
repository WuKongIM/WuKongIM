from pathlib import Path
import datetime,gzip,hashlib,json,re
p=Path(__file__).resolve().parent;out=p/'frozen-final';out.mkdir()
summary=json.loads((p/'merge-summary.json').read_text());post=json.loads((p/'postmerge-03/summary.json').read_text());assert len(post['runs'])==1 and post['runs'][0]['conclusion']=='success'
summary['postmerge']=post;summary['completed_at']=datetime.datetime.now(datetime.timezone.utc).isoformat();(out/'summary.json').write_text(json.dumps(summary,indent=2)+'\n')
files=list(p.glob('*.json'))+list(p.glob('*.py'))
for name in ['postmerge-01','postmerge-02','postmerge-03','postmerge-logs-public']:
 files +=[f for f in (p/name).rglob('*') if f.is_file()]
for f in sorted(set(files)):
 b=f.read_bytes();rel=f.relative_to(p);dest=out/rel
 if f.suffix=='.log':dest=out/(str(rel)+'.gz');b=gzip.compress(b,mtime=0)
 dest.parent.mkdir(parents=True,exist_ok=True);dest.write_bytes(b)
(out/'README.md').write_text('''# PR #1020 合并交付

人类已明确批准“同意合并 #1020”。2026-10-21 12:43:39 UTC 使用普通 `gh pr merge --merge --match-head-commit 0cdfaa37b00c58819895dee54029c27c83c9caa9` 合并，没有 `--admin`、保护修改、Review Agent 评论或手工 Actions dispatch。

- 新 main / merge：`e30299fa59ea6669ea71024eabe209840f241f19`。
- Parents：`ce3c4d9110f17bb71dc99a89dc3c09758fabf980` 与已验收 head `0cdfaa37b00c58819895dee54029c27c83c9caa9`。
- Full tree：`36f96357c967418517a00444e52ae03f77c3ff90`，与首轮 Linux CI 的候选及 synthetic merge 完全一致。
- Fresh 保护 JSON 前后完全相同。非 draft、mergeable clean、无阻断 Review；22 checks 中20 success与2条件skip。比最终CI快照多一个ready事件的成功 credential-free wake-up，不是CI rerun。
- 拉取远端 main 并验证 contains 原tip后，专属 `.worktrees/v3-rc-released-continuation` 和本地 `codex/v3-rc-released-continuation` 已普通删除，未用force；清理前tracked/untracked/ignored均为空。
- 用户 primary main 仍为 `a804f4a57c2b9f1bf6dcfa74382d9cfdc9e34f83`，primary status逐字不变；未修改其他worktree、packages或证据分支。
- 自然 postmerge Docs run `39601918104` attempt1 / exact merge SHA：构建、部署、direct-origin验证、既有bounded CDN refresh四job全部success。四份完整公共日志经行级认证关键词脱敏并gzip，原始字节数/SHA和脱敏行摘要保留；raw日志仅本机私有目录且未归档。

原 Mac scripts-integration 首轮9分钟超时及所有CI原始证据继续保留于前代收据，不被合并成功覆盖。合并不等于正式v3发布：未采购新的RC主机、未重新Quote/dispatch，也未创建产品tag/Release或生产stable包发布。本次只完成已批准PR合并和既有自动文档流程。

使用 `SHA256SUMS` 核对完整目录。`ARCHIVE_ALLOWLIST.json` 列出公开文件；不含私钥、解密云凭证、构建缓存、二进制或原始认证日志。扫描仅为固定私钥/PAT形状及日志关键词过滤，不是完整秘密审计。
''')
entries=[]
for f in sorted(out.rglob('*')):
 if not f.is_file():continue
 b=f.read_bytes();raw=gzip.decompress(b) if f.suffix=='.gz' else b
 assert not re.search(rb'-----BEGIN (?:OPENSSH|RSA|EC|DSA)? ?PRIVATE KEY',raw),str(f)
 assert not re.search(rb'(?:github_pat_[A-Za-z0-9_]{30,}|gh[pousr]_[A-Za-z0-9]{30,})',raw),str(f)
 entries.append({'path':str(f.relative_to(out)),'bytes':len(b),'sha256':hashlib.sha256(b).hexdigest()})
(out/'ARCHIVE_ALLOWLIST.json').write_text(json.dumps({'entries':entries,'private_key_or_pat_marker_found':False,'excluded':['postmerge-logs-private','all other worktrees','binary/cache/keys']},indent=2)+'\n')
rows=[hashlib.sha256(f.read_bytes()).hexdigest()+'  '+str(f.relative_to(out)) for f in sorted(out.rglob('*')) if f.is_file()]
(out/'SHA256SUMS').write_text('\n'.join(rows)+'\n')
for row in rows:
 h,n=row.split('  ',1);assert hashlib.sha256((out/n).read_bytes()).hexdigest()==h
print(json.dumps({'directory':str(out),'files':len(rows)+1,'bytes':sum(f.stat().st_size for f in out.rglob('*') if f.is_file()),'manifest_sha256':hashlib.sha256((out/'SHA256SUMS').read_bytes()).hexdigest()}))
