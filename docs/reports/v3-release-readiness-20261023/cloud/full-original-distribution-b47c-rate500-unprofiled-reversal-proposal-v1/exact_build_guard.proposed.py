"""Read-only fixedb47c construction gate; never builds or executes a product."""
from pathlib import Path
import hashlib,json,os,subprocess
SOURCE = "b47c03d461a6d8918e537e027bf56539ce78c585"
SOURCE_TREE = "7ce68e726fea61bd6d176fbc7053dede920d5b00"
SOURCE_ROOT = Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-exact-source-b47c-queue-snapshot')
BUILD_ROOT = Path('/tmp/wukongim-v3-readiness-20261023/cloud/exact-source-build-b47c-native-v1')
EXACT_SERVER_SHA = "5ffa70c36e7272cb2dac30f160ebb2af5a59c973936b973984e0d16c995f2b91"
EXACT_CLI_SHA = "d4e76e3ff0b770a5450923cb5bd8f04195758b4daeea48b9856b3ba706982c41"
PRODUCT_BINARIES = {name:BUILD_ROOT/'darwin-arm64'/name for name in ['wukongim','wkcli']}
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
def verify_exact_build():
 assert (SOURCE_ROOT/'.git').is_dir(), 'source_requires_real_git_directory'
 assert subprocess.check_output(['git','rev-parse','HEAD'],cwd=SOURCE_ROOT,text=True,timeout=3).strip()==SOURCE,'source_revision_mismatch'
 assert subprocess.check_output(['git','rev-parse','HEAD^{tree}'],cwd=SOURCE_ROOT,text=True,timeout=3).strip()==SOURCE_TREE,'source_tree_mismatch'
 assert not subprocess.check_output(['git','status','--porcelain','--untracked-files=all'],cwd=SOURCE_ROOT,text=True,timeout=3),'source_not_clean'
 for root,digest in [(BUILD_ROOT,'1d0ee29c0a8c78fb59b6072ee9c7cef2f9c183d1bdea8f6889d1b2eed6d0c279')]:
  assert sha(root/'PUBLIC_SHA256SUMS')==digest,'build_receipt_manifest_mismatch'
  lines=(root/'PUBLIC_SHA256SUMS').read_text().splitlines();assert len(lines)==12,'build_receipt_member_count_mismatch'
  for line in lines:
   expected,name=line.split('  ',1);path=Path(name);assert not path.is_absolute() and '..' not in path.parts
   assert sha(root/path)==expected,'build_receipt_member_mismatch'
 receipt=json.loads((BUILD_ROOT/'build-receipt.json').read_text())
 assert receipt['source']==str(SOURCE_ROOT) and receipt['revision']==SOURCE and receipt['tree']==SOURCE_TREE,'build_receipt_source_mismatch'
 assert receipt['real_git_directory'] and receipt['clean_after'] and receipt['assets_count']==76 and receipt['runtime_executions']==0 and len(receipt['builds'])==2,'incomplete_build_receipt'
 assert receipt['compiler_version']=='go version go1.25.11 darwin/arm64' and receipt['environment']['CGO_ENABLED']=='0' and receipt['environment']['GOWORK']=='off' and receipt['environment']['GOTOOLCHAIN']=='local' and receipt['environment']['GOPROXY']=='off' and receipt['environment']['GOSUMDB']=='off','build_environment_mismatch'
 assets=json.loads((BUILD_ROOT/'embedded-assets-comparison.json').read_text());assert len(assets)==76
 for asset in assets:assert asset['equals_sealed_56c_asset'] and asset['prior_original_cloud_comparison'] and asset['equals_sealed_2662_asset'] and sha(SOURCE_ROOT/asset['source_path'])==asset['sha256'],'source_asset_mismatch'
 result={}
 env={'PATH':'/usr/bin:/bin:/usr/sbin:/sbin','LANG':'C','LC_ALL':'C','TZ':'UTC','GOTOOLCHAIN':'local','GOWORK':'off','GOPROXY':'off','GOSUMDB':'off'}
 for name,expected in [('wukongim',EXACT_SERVER_SHA),('wkcli',EXACT_CLI_SHA)]:
  binary=PRODUCT_BINARIES[name];assert binary.is_file() and not binary.is_symlink() and sha(binary)==expected,'exact_binary_mismatch'
  matches=[row for row in receipt['builds'] if row['path']==str(binary)];assert len(matches)==1,'binary_identity_mismatch'
  identity=matches[0];assert identity['sha256']==expected and identity['bytes']==binary.stat().st_size and identity['exit_code']==0 and identity['cwd']==str(SOURCE_ROOT),'binary_identity_mismatch'
  assert identity['argv']==[receipt['compiler'],'build','-p=1','-trimpath','-buildvcs=true','-mod=readonly','-ldflags=-s -w -buildid=','-o',str(binary),'./cmd/'+name],'binary_build_flags_mismatch'
  info=subprocess.check_output(['/Users/tt/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.25.11.darwin-arm64/bin/go','version','-m',str(binary)],cwd=SOURCE_ROOT,env=env,text=True,timeout=5)
  assert info.splitlines()[0].endswith(': go1.25.11'),'embedded_compiler_mismatch'
  settings={line.split('\t',2)[2].split('=',1)[0]:line.split('\t',2)[2].split('=',1)[1] for line in info.splitlines() if line.startswith('\tbuild\t') and '=' in line.split('\t',2)[2]}
  for k,v in {'-trimpath':'true','CGO_ENABLED':'0','GOOS':'darwin','GOARCH':'arm64','vcs.revision':SOURCE,'vcs.modified':'false'}.items():assert settings.get(k)==v,'embedded_setting_mismatch:'+k
  assert sha(BUILD_ROOT/(name+'-buildinfo.txt'))==hashlib.sha256(info.encode()).hexdigest(),'buildinfo_recheck_mismatch'
  result[name]={'path':str(binary),'sha256':expected,'compiler':'go1.25.11','build_settings':settings}
 return {'schema':'wukongim/exact-b47c-native-readonly-preflight/v1','verified':True,'product_started':False,'source_root':str(SOURCE_ROOT),'source_revision':SOURCE,'source_clean':True,'real_git_directory':True,'binaries':result,'build_receipt_manifest_sha256':sha(BUILD_ROOT/'PUBLIC_SHA256SUMS'),'cloud_binary_byte_equality_claimed':False,'runtime_limit':'native shared Macarm64 diagnosis, not a Linux performance environment'}
if __name__=='__main__':print(json.dumps(verify_exact_build(),indent=2))
