import importlib.util,json,hashlib,shutil,tempfile
from pathlib import Path
spec=importlib.util.spec_from_file_location('verify_site','/control/scripts/verify-production-package-site.py');V=importlib.util.module_from_spec(spec);spec.loader.exec_module(V)
package=Path('/tmp/wukongim-v3-readiness-20261021/packages/m2b-native-delivery-sdk-repair-v2/native-diagnostic/materialized-B/archives/4243/site/rpm/stable/el/9/x86_64/Packages/wukongim_3.0.1_linux_amd64.rpm');cert=Path('/tmp/wukongim-v3-readiness-20261021/packages/provider-preview-fixture-implementation/ci-init-native-1/fixtures/B/control/keys/rpm-stable.asc')
candidate=V.rpm_signature_packet_issuer(package);exports=V.export_rpm_candidate_certificates(cert,[candidate])
rows=[]
with tempfile.TemporaryDirectory(prefix='wk-path-pair-') as temporary:
 for label in ('good','bad'):
  p=Path(temporary)/label/'fixture.rpm';p.parent.mkdir();shutil.copyfile(package,p)
  rows.append({'path':str(p),'sha256':hashlib.sha256(p.read_bytes()).hexdigest(),'original_function_result':V.rpm_candidate_signature_verifies(p,exports[candidate],candidate)})
Path('/tmp/wukongim-v3-readiness-20261021/packages/m2b-native-delivery-sdk-repair-v2/native-diagnostic/path-pair/result.json').write_text(json.dumps({'test_only':True,'new_signing':False,'source_package':str(package),'certificate_sha256':hashlib.sha256(cert.read_bytes()).hexdigest(),'candidate':candidate,'cases':rows,'limitation':'Independent paired probe; original failure package path was not retained and original cause remains unknown.'},indent=2)+'\n')
