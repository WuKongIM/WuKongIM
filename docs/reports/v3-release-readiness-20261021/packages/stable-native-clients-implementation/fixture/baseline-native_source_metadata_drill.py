#!/usr/bin/env python3
"""TEST ONLY: exercise the metadata CLI with real, tiny DEB/RPM packages.

Run only inside the existing digest-pinned, network-disabled toolchain. No
package is installed or executed. Ephemeral test keys never leave scratch.
The synthetic receipt is not GitHub provenance or release qualification.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
INSPECTOR = ROOT / "scripts/verify-source-package-metadata.py"


def run(command: list[str], *, environment: dict | None = None) -> str:
    result = subprocess.run(command, stdin=subprocess.DEVNULL, capture_output=True,
                            text=True, timeout=90, env=environment)
    if result.returncode:
        raise RuntimeError(f"TEST ONLY command failed: {command[0]}\n{result.stderr}")
    return result.stdout.strip()


def build_packages(root: Path, deb_version: str, rpm_version: str,
                   *, epoch: int = 0) -> tuple[Path, Path]:
    """Build data-only fixtures; neither package contains an executable payload."""
    deb_root = root / "deb"
    (deb_root / "DEBIAN").mkdir(parents=True)
    (deb_root / "DEBIAN/control").write_text(
        f"Package: wukongim\nVersion: {deb_version}\nArchitecture: amd64\n"
        "Maintainer: TEST ONLY <test-only@invalid>\nDescription: TEST ONLY metadata fixture\n"
    )
    data = deb_root / "usr/share/doc/wukongim/TEST-ONLY"
    data.parent.mkdir(parents=True)
    data.write_text("TEST ONLY, no product binary\n")
    deb = root / "fixture.deb"
    run(["dpkg-deb", "--build", "--root-owner-group", str(deb_root), str(deb)])
    top = root / "rpmbuild"
    top.mkdir()
    spec = root / "fixture.spec"
    spec.write_text(
        f"Name: wukongim\nVersion: {rpm_version}\nRelease: 1\nEpoch: {epoch}\n"
        "Summary: TEST ONLY metadata fixture\nLicense: Apache-2.0\nBuildArch: x86_64\n"
        "%description\nTEST ONLY, no product binary.\n"
        "%install\nmkdir -p %{buildroot}/usr/share/doc/wukongim\n"
        "printf 'TEST ONLY, no product binary\\n' > %{buildroot}/usr/share/doc/wukongim/TEST-ONLY\n"
        "%files\n/usr/share/doc/wukongim/TEST-ONLY\n"
    )
    run(["rpmbuild", "-bb", "--define", f"_topdir {top}",
         "--define", f"_tmppath {root}", "--define", "_build_id_links none", str(spec)])
    rpms = list((top / "RPMS").rglob("*.rpm"))
    if len(rpms) != 1:
        raise RuntimeError("TEST ONLY build did not produce exactly one RPM")
    return deb, rpms[0]


def fixture_receipt(root: Path, version: str, deb: Path, rpm: Path) -> tuple[Path, Path]:
    assets = root / "assets"
    assets.mkdir(parents=True)
    prefix = f"wukongim_{version}"
    for platform in ("darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64"):
        (assets / f"{prefix}_{platform}.tar.gz").write_bytes(b"TEST ONLY opaque archive\n")
    shutil.copyfile(deb, assets / f"{prefix}_linux_amd64.deb")
    shutil.copyfile(rpm, assets / f"{prefix}_linux_amd64.rpm")
    checksums = [{"name": p.name, "sha256": hashlib.sha256(p.read_bytes()).hexdigest()}
                 for p in sorted(assets.iterdir())]
    checksum_name = f"{prefix}_checksums.txt"
    (assets / checksum_name).write_text("".join(f"{c['sha256']}  {c['name']}\n" for c in checksums))
    facts = [{"id": i, "name": p.name, "downloaded_file": p.name,
              "sha256": hashlib.sha256(p.read_bytes()).hexdigest(), "size": p.stat().st_size}
             for i, p in enumerate(sorted(assets.iterdir()), 1)]
    receipt = {"schema": "wukongim/source-release-resolution/v1", "repository": "WuKongIM/WuKongIM",
               "release_id": 4242, "tag": f"v{version}", "version": version,
               "prerelease": "-" in version, "published_at": "2026-01-01T00:00:00Z",
               "source_sha": "1" * 40, "initial_main_sha": "2" * 40,
               "final_main_sha": "2" * 40, "main_sha": "2" * 40,
               "asset_count": 7, "total_size": sum(a["size"] for a in facts),
               "assets": facts, "checksum_asset": checksum_name, "checksum_entries": checksums,
               "release_revalidated": True, "tag_revalidated": True, "main_ancestry_revalidated": True}
    path = root / "source.json"
    path.write_text(json.dumps(receipt))
    for p in assets.iterdir():
        p.chmod(0o444)
    assets.chmod(0o555)
    return path, assets


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output-dir", required=True, type=Path)
    args = parser.parse_args()
    if not args.output_dir.is_dir() or any(args.output_dir.iterdir()):
        parser.error("output directory must exist and be empty")
    required = ("dpkg-deb", "rpm", "rpmbuild", "rpmsign", "gpg", "gpgconf")
    for tool in required:
        if shutil.which(tool) is None:
            raise RuntimeError(f"required real native tool is unavailable: {tool}")
    evidence = {"schema": "wukongim/test-only-native-source-metadata/v1", "test_only": True,
                "payloads_executed": False, "synthetic_source_provenance": True,
                "passed": False, "cases": [], "native_signature_tags": []}
    try:
        with tempfile.TemporaryDirectory(prefix="wk-metadata-TEST-ONLY-") as temporary:
            scratch = Path(temporary)
            inspector_home = scratch / "home"
            inspector_home.mkdir(mode=0o700)
            os.environ["HOME"] = str(inspector_home)
            evidence["tool_versions"] = {tool: run([tool, "--version"]).splitlines()[0]
                                         for tool in ("dpkg-deb", "rpm", "gpg")}

            def check(name: str, version: str, channel: str, deb: Path, rpm: Path,
                      expected_error: str | None = None) -> None:
                receipt, assets = fixture_receipt(scratch / name, version, deb, rpm)
                result = subprocess.run(
                    [sys.executable, "-B", str(INSPECTOR), "--receipt", str(receipt),
                     "--asset-dir", str(assets), "--channel", channel],
                    capture_output=True, text=True, timeout=180,
                )
                passed = (result.returncode == 0) if expected_error is None else (
                    result.returncode != 0 and not result.stdout and expected_error in result.stderr)
                item = {"case": name, "passed": passed, "exit_code": result.returncode,
                        "expected_error": expected_error, "stderr": result.stderr,
                        "metadata": json.loads(result.stdout) if result.returncode == 0 else None}
                evidence["cases"].append(item)
                if not passed:
                    raise RuntimeError(f"native metadata case failed: {name}: {result.stderr}")

            stable = scratch / "stable-build"
            stable.mkdir()
            deb, rpm = build_packages(stable, "3.1.0", "3.1.0")
            check("stable", "3.1.0", "stable", deb, rpm)
            check("wrong-channel", "3.1.0", "preview", deb, rpm, "preview source tag")
            check("wrong-deb-version", "3.1.1", "stable", deb, rpm, "DEB Version mismatch")
            preview = scratch / "preview-build"
            preview.mkdir()
            preview_deb, preview_rpm = build_packages(preview, "3.1.0~rc.1-hotfix", "3.1.0~rc.1_hotfix")
            check("preview", "3.1.0-rc.1-hotfix", "preview", preview_deb, preview_rpm)
            check("wrong-rpm-version", "3.1.0", "stable", deb, preview_rpm, "RPM VERSION mismatch")
            epoch = scratch / "epoch-build"
            epoch.mkdir()
            _, epoch_rpm = build_packages(epoch, "3.1.0", "3.1.0", epoch=9)
            check("wrong-rpm-epoch", "3.1.0", "stable", deb, epoch_rpm, "RPM EPOCHNUM mismatch")

            home = scratch / "test-only-gnupg"
            home.mkdir(mode=0o700)
            try:
                run(["gpg", "--homedir", str(home), "--batch", "--pinentry-mode", "loopback",
                     "--passphrase", "", "--quick-generate-key",
                     "Native metadata TEST ONLY <test-only@invalid>", "rsa3072", "sign", "1d"])
                listing = run(["gpg", "--homedir", str(home), "--batch", "--with-colons", "--list-keys"])
                fingerprint = next(line.split(":")[9] for line in listing.splitlines() if line.startswith("fpr:"))
                signed = scratch / "signed.rpm"
                shutil.copyfile(rpm, signed)
                run(["rpmsign", "--define", f"_gpg_name {fingerprint}", "--define", f"_gpg_path {home}",
                     "--define", "__gpg /usr/bin/gpg", "--define", "_gpg_digest_algo sha256", "--addsign", str(signed)])
                for tag in ("SIGPGP", "SIGGPG", "RSAHEADER", "DSAHEADER"):
                    signature = run(["rpm", "-qp", "--queryformat", f"%{{{tag}:pgpsig}}", str(signed)])
                    if signature != "(none)":
                        evidence["native_signature_tags"].append(tag)
                if "RSAHEADER" not in evidence["native_signature_tags"]:
                    raise RuntimeError("native signed fixture must exercise an actual RSAHEADER")
                check("signed-rpm", "3.1.0", "stable", deb, signed, "mismatch")
            finally:
                run(["gpgconf", "--homedir", str(home), "--kill", "all"])
            evidence["passed"] = True
        return 0
    finally:
        # Only public metadata results leave the isolated scratch tree.
        output = args.output_dir / "native-source-metadata.json"
        with output.open("x") as stream:
            json.dump(evidence, stream, sort_keys=True, indent=2)
            stream.write("\n")
        print(json.dumps(evidence, sort_keys=True))


if __name__ == "__main__":
    raise SystemExit(main())
