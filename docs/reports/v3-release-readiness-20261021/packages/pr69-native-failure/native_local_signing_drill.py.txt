#!/usr/bin/env python3
"""TEST ONLY: real local file/env/stdin signing of tiny data-only packages.

Run in the existing pinned, network-disabled Linux toolchain. No package is
installed or executed. Only the public JSON result leaves temporary scratch;
test keys, exports, signed trees and payloads are never uploaded.
"""

from __future__ import annotations

import argparse
import gzip
import hashlib
import importlib.util
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path
import xml.etree.ElementTree as ET


ROOT = Path(__file__).resolve().parents[1]
SIGNER = ROOT / "scripts/sign-package-family.py"
APT_RELEASE = "dists/stable/Release"
RPM_REPOSITORY = "stable/el/9/x86_64"
PACKAGE = "Packages/wukongim.rpm"


class SignerInvocationError(RuntimeError):
    """Retain a bounded public diagnostic for one failed positive signer case."""

    def __init__(self, diagnostic: dict):
        self.diagnostic = diagnostic
        super().__init__(f"TEST ONLY signer case failed: {diagnostic['case']}")


def load(name: str, filename: str):
    spec = importlib.util.spec_from_file_location(name, ROOT / "tests" / filename)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    sys.modules[name] = module
    spec.loader.exec_module(module)
    return module


fixtures = load("local_drill_native_fixtures", "native_source_metadata_drill.py")
keys = load("local_drill_test_keys", "test_validate_signing_material.py")


def require(condition: bool, message: str) -> None:
    if not condition:
        raise RuntimeError(message)


def facts(path: Path) -> dict:
    raw = path.read_bytes()
    return {"sha256": hashlib.sha256(raw).hexdigest(), "size": len(raw)}


def environment(home: Path) -> dict[str, str]:
    return {"HOME": str(home), "GNUPGHOME": str(home), "LANG": "C", "LC_ALL": "C",
            "PATH": "/usr/bin:/bin"}


def run(command: list[str], *, env: dict, cwd: Path | None = None,
        input_bytes: bytes | None = None, success: bool = True) -> subprocess.CompletedProcess:
    result = subprocess.run(command, input=input_bytes or b"", capture_output=True,
                            env=env, cwd=cwd, timeout=120)
    if success:
        require(result.returncode == 0, f"TEST ONLY native command failed: {Path(command[0]).name}")
    return result


def gpg(home: Path, arguments: list[str], *, success: bool = True) -> subprocess.CompletedProcess:
    return run(["gpg", "--no-options", "--homedir", str(home), "--batch", "--no-tty",
                *arguments], env=environment(home), success=success)


def exact_signature(result: subprocess.CompletedProcess, fingerprint: str) -> None:
    rows = [row.split() for row in result.stdout.decode().splitlines()
            if row.startswith("[GNUPG:] VALIDSIG ")]
    require(result.returncode == 0 and len(rows) == 1 and len(rows[0]) >= 10
            and rows[0][2] == fingerprint and rows[0][9] == "8",
            "independent verification did not prove exact current/SHA256 signature")


def prepare_inputs(scratch: Path, deb: Path, rpm: Path) -> tuple[Path, Path, list[str]]:
    apt = scratch / "apt-input"
    pool = apt / "pool/main/w/wukongim"
    pool.mkdir(parents=True)
    shutil.copyfile(deb, pool / "wukongim.deb")
    index = apt / "dists/stable/main/binary-amd64/Packages"
    index.parent.mkdir(parents=True)
    index.write_bytes(run(["dpkg-scanpackages", "--multiversion", "pool"],
                          env=environment(scratch), cwd=apt).stdout)
    index.with_suffix(".gz").write_bytes(gzip.compress(index.read_bytes(), mtime=0))
    release = apt / APT_RELEASE
    release.write_bytes(run(["apt-ftparchive", "-o", "APT::FTPArchive::Release::Suite=stable",
                             "-o", "APT::FTPArchive::Release::Codename=stable", "release",
                             "dists/stable"], env=environment(scratch), cwd=apt).stdout)
    rpm_input = scratch / "rpm-input"
    package = rpm_input / RPM_REPOSITORY / PACKAGE
    package.parent.mkdir(parents=True)
    shutil.copyfile(rpm, package)
    allowlists = []
    for name, value in (
        ("new", {"schema": "wukongim/rpm-package-allowlist/v1",
                 "packages": [{"path": PACKAGE, **facts(package)}]}),
        ("signed", {"schema": "wukongim/rpm-package-allowlist/v1", "packages": []}),
        ("active", {"schema": "wukongim/rpm-active-allowlist/v1", "paths": [PACKAGE]}),
    ):
        path = scratch / f"{name}.json"
        path.write_bytes(json.dumps(value, sort_keys=True, separators=(",", ":")).encode() + b"\n")
        allowlists += [f"--{name}-rpm-allowlist", str(path)]
    return apt, rpm_input, allowlists


def invoke(scratch: Path, name: str, family: str, mode: str, key, successor: str,
           source: Path, allowlists: list[str], *, secret: Path | None = None,
           passphrase: Path | None = None, reject: bool = False) -> tuple[Path, dict | None]:
    output = scratch / name
    env = environment(scratch)
    arguments = [sys.executable, "-B", str(SIGNER), "--family", family,
                 "--input-root", str(source), "--output-root", str(output),
                 "--public-cert", str(key.public_cert),
                 "--primary-fingerprint", key.primary_fingerprint,
                 "--signing-subkey-fingerprint", key.signing_subkey_fingerprint,
                 "--next-signing-subkey-fingerprint", successor]
    selected = secret or key.selected_secret_base64
    phrase = passphrase or key.passphrase_file
    stdin = None
    if mode == "env":
        env["WK_TEST_ONLY_SECRET"] = selected.read_text()
        env["WK_TEST_ONLY_PASSPHRASE"] = phrase.read_text()
        arguments += ["--secret-subkey-base64-env", "WK_TEST_ONLY_SECRET",
                      "--passphrase-env", "WK_TEST_ONLY_PASSPHRASE"]
    else:
        arguments += ["--passphrase-file", str(phrase)]
        if mode == "stdin":
            arguments += ["--secret-subkey-base64-stdin"]
            stdin = selected.read_bytes()
        else:
            arguments += ["--secret-subkey-base64-file", str(selected)]
    arguments += (["--apt-release", APT_RELEASE] if family == "apt" else
                  ["--rpm-repository", RPM_REPOSITORY, *allowlists])
    result = run(arguments, env=env, input_bytes=stdin, success=False)
    if reject:
        require(result.returncode != 0 and not result.stdout and not output.exists()
                and not list(scratch.glob(f".{name}.*.tmp.*")),
                f"negative signer case exposed success or partial output: {name}")
        return output, None
    if result.returncode != 0:
        message = "no recognized signer diagnostic"
        for line in result.stderr.splitlines():
            if line.startswith(b"package-family signing failed: "):
                # Only the signer's top-level diagnostic is public. Never copy
                # arbitrary tool stderr or the TEST ONLY credential fixture.
                for private in (selected.read_bytes().strip(), phrase.read_bytes().strip()):
                    if private:
                        line = line.replace(private, b"[redacted]")
                message = line[:2048].decode("utf-8", errors="ignore")
                break
        raise SignerInvocationError({"case": name, "family": family, "input_mode": mode,
                                     "exit_code": result.returncode, "message": message})
    receipt = json.loads(result.stdout)
    require(receipt["schema"] == "wukongim/package-family-signing-receipt/v1"
            and receipt["family"] == family and receipt["key"]["validated"] is True
            and receipt["key"]["signing_subkey_fingerprint"] == key.signing_subkey_fingerprint,
            f"signer receipt identity differs: {name}")
    return output, receipt


def public_home(scratch: Path, family: str, certificate: Path) -> Path:
    home = scratch / f"public-only-{family}"
    home.mkdir(mode=0o700)
    gpg(home, ["--import", str(certificate)])
    secret = gpg(home, ["--with-colons", "--list-secret-keys"]).stdout
    require(not any(line.startswith((b"sec:", b"ssb:")) for line in secret.splitlines()),
            "independent verifier unexpectedly has a secret key")
    return home


def verify_apt(output: Path, source: Path, home: Path, fingerprint: str) -> None:
    release = output / APT_RELEASE
    exact_signature(gpg(home, ["--status-fd", "1", "--verify",
                               str(release.with_name("Release.gpg")), str(release)]), fingerprint)
    extracted = home / f"{output.name}-Release"
    exact_signature(gpg(home, ["--status-fd", "1", "--output", str(extracted), "--decrypt",
                               str(release.with_name("InRelease"))]), fingerprint)
    require(extracted.read_bytes() == release.read_bytes() == (source / APT_RELEASE).read_bytes(),
            "APT signed cleartext differs from original Release")
    checksum_section = release.read_text().split("SHA256:\n", 1)[1].split("SHA512:", 1)[0]
    index_paths = set()
    for line in checksum_section.splitlines():
        digest, size, relative = line.split()
        actual = facts(release.parent / relative)
        require(actual == {"sha256": digest, "size": int(size)}, "APT Release hash closure differs")
        index_paths.add(relative)
    require(index_paths == {"main/binary-amd64/Packages", "main/binary-amd64/Packages.gz"},
            "APT Release does not close over both package indexes")
    index = release.parent / "main/binary-amd64/Packages"
    require(gzip.decompress(index.with_suffix(".gz").read_bytes()) == index.read_bytes(),
            "APT compressed package index differs")
    fields = dict(line.split(": ", 1) for line in index.read_text().strip().splitlines())
    require(facts(output / fields["Filename"]) == facts(source / fields["Filename"])
            == {"sha256": fields["SHA256"], "size": int(fields["Size"])},
            "APT index does not preserve the source DEB bytes")


def rpm_database(home: Path, fingerprint: str, primary: str) -> Path:
    certificate = home / f"{fingerprint}.asc"
    gpg(home, ["--armor", "--output", str(certificate), "--export-options", "export-minimal",
               "--export-filter", f"drop-subkey=fpr <> {fingerprint}", "--export", primary])
    shown = gpg(home, ["--with-colons", "--with-fingerprint", "--with-subkey-fingerprint",
                      "--show-keys", str(certificate)]).stdout
    identifiers = [line.split(b":")[9].decode() for line in shown.splitlines() if line.startswith(b"fpr:")]
    require(identifiers == [primary, fingerprint], "RPM verifier certificate was not current-only")
    db = home / f"rpmdb-{fingerprint}"
    db.mkdir(mode=0o700)
    run(["rpm", "--dbpath", str(db), "--initdb"], env=environment(home))
    run(["rpm", "--dbpath", str(db), "--import", str(certificate)], env=environment(home))
    return db


def rpm_signature(package: Path, db: Path, home: Path, fingerprint: str) -> bool:
    result = run(["rpmkeys", "--dbpath", str(db), "--verbose", "--checksig", str(package)],
                 env=environment(home), success=False)
    text = (result.stdout + result.stderr).decode()
    signatures = re.findall(r"Header V4 RSA/SHA256 Signature, key ID ([a-fA-F0-9]{8,16}): OK", text)
    return (result.returncode == 0 and len(signatures) == 1
            and fingerprint.lower().endswith(signatures[0].lower())
            and not any(word in text.upper() for word in ("NOKEY", "NOT OK", "BAD")))


def verify_rpm(output: Path, source: Path, home: Path, fingerprint: str,
               current_db: Path, next_db: Path, successor: str) -> None:
    root = output / RPM_REPOSITORY
    package = root / PACKAGE
    require(rpm_signature(package, current_db, home, fingerprint), "actual RPM current-key verification failed")
    require(not rpm_signature(package, next_db, home, successor), "next-only key unexpectedly verified RPM")
    require(facts(package) != facts(source / RPM_REPOSITORY / PACKAGE), "RPM payload was not signed")
    repomd = root / "repodata/repomd.xml"
    exact_signature(gpg(home, ["--status-fd", "1", "--verify", str(repomd.with_suffix(".xml.asc")),
                               str(repomd)]), fingerprint)
    ns = "{http://linux.duke.edu/metadata/repo}"
    entries = ET.fromstring(repomd.read_bytes()).findall(f"{ns}data")
    require(sorted(entry.attrib["type"] for entry in entries) == ["filelists", "other", "primary"],
            "RPM metadata kinds differ")
    for entry in entries:
        location, checksum = entry.find(f"{ns}location"), entry.find(f"{ns}checksum")
        opened_checksum = entry.find(f"{ns}open-checksum")
        raw = (root / location.attrib["href"]).read_bytes()
        opened = gzip.decompress(raw)
        require(checksum.attrib["type"] == opened_checksum.attrib["type"] == "sha256"
                and hashlib.sha256(raw).hexdigest() == checksum.text
                and len(raw) == int(entry.findtext(f"{ns}size"))
                and hashlib.sha256(opened).hexdigest() == opened_checksum.text
                and len(opened) == int(entry.findtext(f"{ns}open-size")),
                "RPM compressed/open metadata closure differs")
        if entry.attrib["type"] == "primary":
            common = "{http://linux.duke.edu/metadata/common}"
            packages = ET.fromstring(opened).findall(f"{common}package")
            require(len(packages) == 1, "RPM primary must contain the single TEST ONLY package")
            item = packages[0]
            require(item.find(f"{common}location").attrib["href"] == PACKAGE
                    and item.find(f"{common}checksum").attrib["type"] == "sha256"
                    and item.findtext(f"{common}checksum") == facts(package)["sha256"]
                    and int(item.find(f"{common}size").attrib["package"]) == facts(package)["size"],
                    "RPM metadata does not reference the exact signed package")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output-dir", required=True, type=Path)
    args = parser.parse_args()
    if not args.output_dir.is_dir() or any(args.output_dir.iterdir()):
        parser.error("output directory must exist and be empty")
    evidence = {"schema": "wukongim/test-only-native-local-signing/v1", "test_only": True,
                "passed": False, "payloads_executed": False, "production_keys_used": False,
                "source_provenance_verified": False, "published": False, "cases": []}
    try:
        for tool in ("gpg", "gpgconf", "rpm", "rpmbuild", "rpmsign", "rpmkeys", "createrepo_c",
                     "dpkg-deb", "dpkg-scanpackages", "apt-ftparchive"):
            require(shutil.which(tool) is not None, f"required real native tool unavailable: {tool}")
        evidence["control_files"] = {p: facts(ROOT / p) for p in (
            "tests/native_local_signing_drill.py", "tests/native_source_metadata_drill.py",
            "tests/test_validate_signing_material.py", "scripts/sign-package-family.py",
            "scripts/validate-signing-material.py", "manifests/signing-toolchain.json")}
        with tempfile.TemporaryDirectory(prefix="wk-local-TEST-ONLY-", dir="/tmp") as directory:
            scratch = Path(directory)
            os.environ.clear()
            os.environ.update(environment(scratch))
            evidence["tool_versions"] = {
                tool: run([tool, "--version"], env=environment(scratch)).stdout.decode().splitlines()[0]
                for tool in ("gpg", "rpm", "dpkg-deb", "createrepo_c")
            }
            build = scratch / "build"
            build.mkdir()
            deb, rpm = fixtures.build_packages(build, "3.1.0", "3.1.0")
            apt_input, rpm_input, allowlists = prepare_inputs(scratch, deb, rpm)
            source_bytes = {str(path.relative_to(scratch)): facts(path)
                            for root in (apt_input, rpm_input) for path in root.rglob("*") if path.is_file()}
            key_root = scratch / "private-test-keys"
            factory = keys.TestKeyFactory(key_root)
            public = scratch / "public"
            public.mkdir()
            identities, completed = {}, []
            try:
                for family, algorithm in (("apt", "ed25519"), ("rpm", "rsa3072")):
                    key = factory.generate(f"LOCAL-{family.upper()}", signing_expiration="60d",
                                           add_encryption_subkey=False, algorithm=algorithm)
                    factory._gpg(key.home, ["--pinentry-mode", "loopback", "--passphrase-fd", "0",
                                          "--quick-add-key", key.primary_fingerprint, algorithm, "sign", "120d"],
                                 input_bytes=keys.PASSPHRASE + b"\n")
                    _, subkeys = factory._fingerprints(key.home)
                    require(len(subkeys) == 2 and subkeys[0] == key.signing_subkey_fingerprint,
                            "TEST ONLY current/next topology differs")
                    successor = subkeys[1]
                    key.public_cert.write_bytes(factory._gpg(key.home, ["--armor", "--export", key.primary_fingerprint]))
                    certificate = public / f"{family}.asc"
                    shutil.copyfile(key.public_cert, certificate)
                    identities[family] = {"primary": key.primary_fingerprint,
                                          "current": key.signing_subkey_fingerprint,
                                          "next": successor, "public_certificate": facts(certificate)}
                    source = apt_input if family == "apt" else rpm_input
                    for mode in (("files", "env", "stdin") if family == "apt" else ("files", "env")):
                        name = f"{family}-{mode}"
                        output, receipt = invoke(scratch, name, family, mode, key, successor, source, allowlists)
                        completed.append((name, family, output, receipt))
                    if family == "apt":
                        bad_phrase = scratch / "wrong-passphrase.secret"
                        bad_phrase.write_bytes(b"WRONG TEST ONLY")
                        bad_phrase.chmod(0o600)
                        invoke(scratch, "wrong-passphrase", family, "files", key, successor,
                               source, allowlists, passphrase=bad_phrase, reject=True)
                        bad_phrase.unlink()
                        invoke(scratch, "full-primary-secret", family, "files", key, successor,
                               source, allowlists, secret=key.full_secret_base64, reject=True)
                        evidence["cases"] += [{"case": name, "passed": True} for name in
                                              ("wrong-passphrase", "full-primary-secret")]
            finally:
                factory.close()
                if key_root.exists():
                    shutil.rmtree(key_root)
            fingerprints = [value for identity in identities.values()
                            for name, value in identity.items() if name != "public_certificate"]
            require(all(len({value[-width:] for value in fingerprints}) == len(fingerprints)
                        for width in (40, 16, 8)), "TEST ONLY key identifiers collide")
            evidence["secret_tree_removed_before_verification"] = not key_root.exists()
            homes = []
            try:
                for family in ("apt", "rpm"):
                    home = public_home(scratch, family, public / f"{family}.asc")
                    homes.append(home)
                    identity = identities[family]
                    if family == "rpm":
                        current_db = rpm_database(home, identity["current"], identity["primary"])
                        next_db = rpm_database(home, identity["next"], identity["primary"])
                    for name, kind, output, receipt in completed:
                        if kind != family:
                            continue
                        if family == "apt":
                            verify_apt(output, apt_input, home, identity["current"])
                        else:
                            verify_rpm(output, rpm_input, home, identity["current"], current_db,
                                       next_db, identity["next"])
                        evidence["cases"].append({"case": name, "passed": True, "receipt": receipt,
                                                  "independent_public_verification": True})
                    if family == "apt":
                        release = scratch / "apt-files" / APT_RELEASE
                        release.write_bytes(release.read_bytes() + b"tampered\n")
                        result = gpg(home, ["--verify", str(release.with_name("Release.gpg")), str(release)], success=False)
                        require(result.returncode != 0, "tampered APT Release unexpectedly verified")
                        evidence["cases"].append({"case": "apt-tampered-release", "passed": True})
                    else:
                        root = scratch / "rpm-files" / RPM_REPOSITORY
                        package = root / PACKAGE
                        raw = bytearray(package.read_bytes())
                        raw[-16] ^= 1
                        package.write_bytes(raw)
                        require(not rpm_signature(package, current_db, home, identity["current"]),
                                "tampered RPM unexpectedly verified")
                        repomd = root / "repodata/repomd.xml"
                        repomd.write_bytes(repomd.read_bytes() + b"\n")
                        result = gpg(home, ["--verify", str(repomd.with_suffix(".xml.asc")), str(repomd)], success=False)
                        require(result.returncode != 0, "tampered repomd unexpectedly verified")
                        evidence["cases"] += [{"case": name, "passed": True} for name in
                                              ("rpm-tampered-payload", "rpm-tampered-repomd")]
            finally:
                for home in homes:
                    run(["gpgconf", "--homedir", str(home), "--kill", "all"], env=environment(home))
            require(all(facts(scratch / path) == original for path, original in source_bytes.items()),
                    "source input bytes changed during signing")
            require(len(evidence["cases"]) == 10, "native signing case set is incomplete")
            evidence.update(passed=True, keys=identities, source_inputs_unchanged=True)
        return 0
    except SignerInvocationError as error:
        evidence["failure"] = error.diagnostic
        raise
    finally:
        with (args.output_dir / "native-local-signing.json").open("x") as stream:
            json.dump(evidence, stream, sort_keys=True, indent=2)
            stream.write("\n")
        print(json.dumps(evidence, sort_keys=True))


if __name__ == "__main__":
    raise SystemExit(main())
