"""TEST ONLY native status regression using existing signed public fixtures."""
import hashlib
import importlib.util
import json
import shutil
import tempfile
from pathlib import Path

spec = importlib.util.spec_from_file_location('rpm_verifier', '/control/scripts/verify-production-package-site.py')
V = importlib.util.module_from_spec(spec)
spec.loader.exec_module(V)
fixture = Path('/fixtures/fixtures/B')
signed = fixture / 'site/rpm/stable/el/9/x86_64/Packages/wukongim_3.0.1_linux_amd64.rpm'
unsigned = fixture / 'source/assets/wukongim_3.0.1_linux_amd64.rpm'
cert = fixture / 'control/keys/rpm-stable.asc'
candidate = V.rpm_signature_packet_issuer(signed)
topology = V.inspect_public_certificate(cert)
other = next(row['fingerprint'] for row in topology['subkeys'] if row['fingerprint'] != candidate)
exports = V.export_rpm_candidate_certificates(cert, [candidate, other])
rows = []
with tempfile.TemporaryDirectory(prefix='wk-rpm-status-') as temporary:
    root = Path(temporary)
    for label in ('good', 'bad', 'NOKEY', 'NOT OK', 'signature'):
        package = root / label / 'fixture.rpm'
        package.parent.mkdir()
        shutil.copyfile(signed, package)
        verified = V.verify_rpm_package_signature(package, cert, {candidate})
        rows.append({'case': label, 'sha256': hashlib.sha256(package.read_bytes()).hexdigest(),
                     'verified_signer': verified, 'passed': verified == candidate})
    corrupt = root / 'corrupt.rpm'
    data = bytearray(signed.read_bytes())
    data[-1] ^= 1
    corrupt.write_bytes(data)
    for label, package, signer in (('corrupt-payload', corrupt, candidate),
                                  ('unsigned', unsigned, candidate),
                                  ('wrong-certificate', signed, other)):
        result = V.rpm_candidate_signature_verifies(package, exports[signer], signer)
        rows.append({'case': label, 'accepted': result, 'passed': not result})
report = {'schema': 'wukongim.rpm-status-native-regression/v1', 'test_only': True,
          'new_signing': False, 'package_installation': False,
          'candidate': candidate, 'certificate_sha256': hashlib.sha256(cert.read_bytes()).hexdigest(),
          'verifier_sha256': hashlib.sha256(Path('/control/scripts/verify-production-package-site.py').read_bytes()).hexdigest(),
          'scratch_removed': not root.exists(), 'cases': rows,
          'passed': all(row['passed'] for row in rows) and not root.exists()}
Path('/evidence/native-result.json').write_text(json.dumps(report, indent=2) + '\n')
print(json.dumps({'passed': report['passed'], 'cases': len(rows)}))
raise SystemExit(0 if report['passed'] else 1)
