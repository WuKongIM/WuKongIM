# Native local-input drill: test scenarios before implementation

During the fixed-load quiet window this is authored/reviewed only, not run.
The prior six local-input CLI tests already demonstrated red then green.

Real tiny data-only DEB/RPM, separate ephemeral TEST ONLY APT/RPM certificates,
and the existing fixed Linux tool image are mandatory; no fake native programs.
Required success cases: apt-files, rpm-files, apt-env, rpm-env, apt-stdin.
Required rejection cases: wrong-passphrase, full-primary-secret,
apt-tampered-release, rpm-tampered-payload, rpm-tampered-repomd.

Before treating a success case as passed:
- exact family receipt/key and input bytes are checked;
- test secret homes/exports/passphrases are removed and agents stopped;
- independent fresh public-only GPG homes verify exact current+SHA256 APT
  detached/clearsigned Release and repomd signatures;
- RPM verification uses a separate rpmdb containing only a filtered public
  current subkey certificate and actual rpmkeys; a second next-only db must
  not verify the same package;
- Release->Packages->DEB and repomd->all gzip metadata->signed RPM hashes and
  sizes match; signed payload differs from unsigned while source stays intact.

Bad passphrase/full-primary file input must fail with empty stdout and no
output/stage. Tamper checks must independently fail after actual positive
verification. Native tool failure or any absent success must keep passed=false.
Never install/execute payloads. Output allowlist is one JSON summary of public
facts/source hashes/check results; no test keys, secret files, package payloads,
signed trees or temporary homes leave scratch.

CI integration: existing PR TEST ONLY workflow, unchanged contents:read and
no protected Environment/production workflow changes. Reuse fixed manifest
image, network none, readonly control, non-root uid, limits, bounded timeout
and unconditional exact-container cleanup. Upload only the named JSON. Existing
workflow-contract test is extended before its workflow step is implemented;
it is not executed during quiet.
