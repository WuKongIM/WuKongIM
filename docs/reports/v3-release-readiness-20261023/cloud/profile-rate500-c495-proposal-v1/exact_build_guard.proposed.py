"""Read-only fixedc495 construction gate; never builds or executes a product."""
from pathlib import Path
import hashlib,json,os,subprocess
SOURCE = "c4954c6a638080e6a915ecec305a3381f4afd9a0"
SOURCE_ROOT = Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-exact-source-c495-candidate')
BUILD_ROOT = Path('/tmp/wukongim-v3-readiness-20261023/cloud/exact-source-build-c495-native-v1')
EXACT_SERVER_SHA = "82ba7ab6955c4ed9e6b60f1f98eb44f14b8e64c9d09224b319c56e5c281fc5a6"
EXACT_CLI_SHA = "1d44f1f1fba89662b2824c6771da37e9f84276ee4afec74eb16477800e02a2ea"
PRODUCT_BINARIES = {name:BUILD_ROOT/'darwin-arm64'/name for name in ['wukongim','wkcli']}
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
def verify_exact_build():
 assert (SOURCE_ROOT/'.git').is_dir(), 'source_requires_real_git_directory'
 assert subprocess.check_output(['git','rev-parse','HEAD'],cwd=SOURCE_ROOT,text=True,timeout=3).strip()==SOURCE,'source_revision_mismatch'
 assert not subprocess.check_output(['git','status','--porcelain','--untracked-files=all'],cwd=SOURCE_ROOT,text=True,timeout=3),'source_not_clean'
 for root,digest in [(BUILD_ROOT,'ece8616d6e2075eea66ad000889d43ab63e7711fbbcb21c3966d6223181b0aba')]:
  assert sha(root/'PUBLIC_SHA256SUMS')==digest,'build_receipt_manifest_mismatch'
  for line in (root/'PUBLIC_SHA256SUMS').read_text().splitlines():
   expected,name=line.split('  ',1);path=Path(name);assert not path.is_absolute() and '..' not in path.parts
   assert sha(root/path)==expected,'build_receipt_member_mismatch'
 completed=json.loads((BUILD_ROOT/'completed.json').read_text())
 assert completed['source_clean'] and completed['binary_count']==2 and completed['tracked_embedded_assets_equal']==76 and not completed['runtime_started'],'incomplete_build_receipt'
 assets=json.loads((BUILD_ROOT/'embedded-assets-comparison.json').read_text());assert len(assets)==76
 for asset in assets:assert asset['equals_original_cloud_asset'] and sha(SOURCE_ROOT/asset['source_path'])==asset['sha256'],'source_asset_mismatch'
 result={}
 env=dict(os.environ);env.update({'GOTOOLCHAIN':'local','GOWORK':'off','GOPROXY':'off','GOSUMDB':'off'})
 for name,expected in [('wukongim',EXACT_SERVER_SHA),('wkcli',EXACT_CLI_SHA)]:
  binary=PRODUCT_BINARIES[name];assert binary.is_file() and not binary.is_symlink() and sha(binary)==expected,'exact_binary_mismatch'
  identity=json.loads((BUILD_ROOT/('darwin-arm64-'+name+'-identity.json')).read_text())
  assert identity['sha256']==expected and identity['source_revision']==SOURCE and identity['compiler']=='go1.25.11' and identity['CGO_ENABLED']==0 and identity['trimpath'] and identity['linkflags']=='-s -w -buildid=','binary_identity_mismatch'
  info=subprocess.check_output(['/Users/tt/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.25.11.darwin-arm64/bin/go','version','-m',str(binary)],cwd=SOURCE_ROOT,env=env,text=True,timeout=5)
  assert info.splitlines()[0].endswith(': go1.25.11'),'embedded_compiler_mismatch'
  settings={line.split('\t',2)[2].split('=',1)[0]:line.split('\t',2)[2].split('=',1)[1] for line in info.splitlines() if line.startswith('\tbuild\t') and '=' in line.split('\t',2)[2]}
  for k,v in {'-trimpath':'true','CGO_ENABLED':'0','GOOS':'darwin','GOARCH':'arm64','vcs.revision':SOURCE,'vcs.modified':'false'}.items():assert settings.get(k)==v,'embedded_setting_mismatch:'+k
  assert sha(BUILD_ROOT/(name+'-buildinfo.txt'))==hashlib.sha256(info.encode()).hexdigest(),'buildinfo_recheck_mismatch'
  result[name]={'path':str(binary),'sha256':expected,'compiler':'go1.25.11','build_settings':settings}
 return {'schema':'wukongim/exact-c495-native-readonly-preflight/v1','verified':True,'product_started':False,'source_root':str(SOURCE_ROOT),'source_revision':SOURCE,'source_clean':True,'real_git_directory':True,'binaries':result,'build_receipt_manifest_sha256':sha(BUILD_ROOT/'PUBLIC_SHA256SUMS'),'cloud_binary_byte_equality_claimed':False,'runtime_limit':'native shared Macarm64 diagnosis, not a Linux performance environment'}
if __name__=='__main__':print(json.dumps(verify_exact_build(),indent=2))
