"""Read-only regression probe for the exact Rocky pin declared by the validator."""
import ast
import hashlib
import json
from pathlib import Path
import sys
import urllib.error
import urllib.request

tree = ast.parse((Path(sys.argv[1]) / 'scripts/validate-production-package-clients.py').read_text())
node = next(n for n in tree.body if isinstance(n, ast.Assign)
            and any(isinstance(t, ast.Name) and t.id == 'RPM_CLIENTS' for t in n.targets))
reference = dict(ast.literal_eval(node.value))['rocky-linux-9']
prefix = 'quay.io/rockylinux/rockylinux@sha256:'
assert reference.startswith(prefix)
digest = reference[len(prefix):]
assert len(digest) == 64 and all(c in '0123456789abcdef' for c in digest)
url = 'https://quay.io/v2/rockylinux/rockylinux/manifests/sha256:' + digest
receipt = {'reference': reference, 'url': url, 'read_only': True, 'passed': False}
request = urllib.request.Request(url, headers={'Accept': 'application/vnd.docker.distribution.manifest.v2+json, application/vnd.oci.image.manifest.v1+json'})
try:
    with urllib.request.urlopen(request, timeout=20) as response:
        raw = response.read(2 * 1024 * 1024 + 1)
        assert len(raw) <= 2 * 1024 * 1024
        receipt.update(status=response.status, bytes=len(raw), sha256=hashlib.sha256(raw).hexdigest(),
                       declared_digest=response.headers.get('Docker-Content-Digest'))
        value = json.loads(raw)
        receipt['passed'] = (response.status == 200 and receipt['sha256'] == digest
                             and receipt['declared_digest'] == 'sha256:' + digest
                             and value.get('schemaVersion') == 2 and isinstance(value.get('config'), dict))
except urllib.error.HTTPError as error:
    receipt.update(status=error.code, error_body=error.read(4096).decode(errors='replace'))
except (OSError, ValueError, AssertionError) as error:
    receipt['error'] = type(error).__name__
print(json.dumps(receipt, sort_keys=True, indent=2))
raise SystemExit(0 if receipt['passed'] else 1)
