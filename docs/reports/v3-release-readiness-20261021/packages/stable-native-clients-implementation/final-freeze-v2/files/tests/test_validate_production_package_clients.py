from __future__ import annotations

import hashlib
import importlib.util
import json
import re
import stat
import subprocess
import unittest
from pathlib import Path
from tempfile import TemporaryDirectory
from unittest import mock


ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts" / "validate-production-package-clients.py"
SPEC = importlib.util.spec_from_file_location(
    "validate_production_package_clients", SCRIPT
)
assert SPEC is not None and SPEC.loader is not None
validator = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(validator)


APT_CERTIFICATE = b"reviewed apt certificate\n"
RPM_CERTIFICATE = b"reviewed rpm certificate\n"
DEB_PAYLOAD = b"authenticated deb payload\n"
RPM_PAYLOAD = b"authenticated rpm payload\n"
RETAINED_DEB_PAYLOAD = b"retained authenticated deb payload\n"
RETAINED_RPM_PAYLOAD = b"retained authenticated rpm payload\n"
APT_BOOTSTRAP = b"reviewed apt repository bootstrap package\n"
RPM_BOOTSTRAP = b"reviewed signed rpm repository bootstrap package\n"
VERSION = "3.1.0-rc.1"
RETAINED_VERSION = "3.0.0-rc.1"
BOOTSTRAP_VERSION = "1.0.0"
AUDIT_RELEASE_ID = 4242
CONTROL_SHA = "1" * 40


def digest(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


class Fixture:
    def __init__(self, root: Path) -> None:
        self.root = root
        self.site = root / "site"
        (self.site / "keys").mkdir(parents=True)
        self.apt_certificate = root / "apt-preview.asc"
        self.rpm_certificate = root / "rpm-preview.asc"
        self.apt_certificate.write_bytes(APT_CERTIFICATE)
        self.rpm_certificate.write_bytes(RPM_CERTIFICATE)
        (self.site / "keys/apt-preview.asc").write_bytes(APT_CERTIFICATE)
        (self.site / "keys/rpm-preview.asc").write_bytes(RPM_CERTIFICATE)
        self.snapshot = root / "snapshot.json"
        self.snapshot_value = {
            "schema": validator.SNAPSHOT_SCHEMA,
            "audit_release_id": AUDIT_RELEASE_ID,
            "control_sha": CONTROL_SHA,
            "releases": [{
                "version": VERSION,
                "source_sha": "a" * 40,
                "source_release_id": 100,
                "package_release_id": 200,
                "deb_sha256": digest(DEB_PAYLOAD),
                "rpm_sha256": digest(b"unsigned rpm payload\n"),
                "state": "active",
                "not_before": None,
            }],
            "retirement": {
                "phase": "none",
                "version": None,
                "not_before": None,
            },
            "payloads": {
                "apt": [{
                    "version": VERSION,
                    "path": (
                        "apt/pool/main/w/wukongim/"
                        f"wukongim_{VERSION}_linux_amd64.deb"
                    ),
                    "source_sha256": digest(DEB_PAYLOAD),
                    "published_sha256": digest(DEB_PAYLOAD),
                    "indexed": True,
                }],
                "rpm": [{
                    "version": VERSION,
                    "path": (
                        "rpm/preview/el/9/x86_64/Packages/"
                        f"wukongim_{VERSION}_linux_amd64.rpm"
                    ),
                    "source_sha256": digest(b"unsigned rpm payload\n"),
                    "published_sha256": digest(RPM_PAYLOAD),
                    "indexed": True,
                }],
            },
            "public_keys": {
                "apt": {
                    "path": "keys/apt-preview.asc",
                    "sha256": digest(APT_CERTIFICATE),
                    "size": len(APT_CERTIFICATE),
                    "primary_fingerprint": "A" * 40,
                    "current_signing_subkey_fingerprint": "B" * 40,
                    "next_signing_subkey_fingerprint": "C" * 40,
                    "historical_signing_subkey_fingerprints": [],
                },
                "rpm": {
                    "path": "keys/rpm-preview.asc",
                    "sha256": digest(RPM_CERTIFICATE),
                    "size": len(RPM_CERTIFICATE),
                    "primary_fingerprint": "D" * 40,
                    "current_signing_subkey_fingerprint": "E" * 40,
                    "next_signing_subkey_fingerprint": "F" * 40,
                    "historical_signing_subkey_fingerprints": [],
                },
            },
            "source_attestations": None,
            "toolchain": {
                "image": "ghcr.io/wukongim/native-package-signing-toolchain",
                "digest": "sha256:" + "2" * 64,
                "workflow_sha": "3" * 40,
                "manifest_sha256": "4" * 64,
                "manifest_size": 256,
            },
        }
        self.write_snapshot()
        apt_filename = (
            f"wukongim-archive-keyring_{BOOTSTRAP_VERSION}_all.deb"
        )
        rpm_filename = f"wukongim-release-{BOOTSTRAP_VERSION}-1.noarch.rpm"
        self.bootstrap_value = {
            "schema": validator.BOOTSTRAP_SCHEMA,
            "version": BOOTSTRAP_VERSION,
            "packages": {
                "apt": {
                    "name": "wukongim-archive-keyring",
                    "version": BOOTSTRAP_VERSION,
                    "architecture": "all",
                    "filename": apt_filename,
                    "repository_path": (
                        f"apt/pool/main/w/wukongim/{apt_filename}"
                    ),
                    "download_path": f"bootstrap/{apt_filename}",
                    "source_sha256": digest(APT_BOOTSTRAP),
                    "source_size": len(APT_BOOTSTRAP),
                    "published_sha256": digest(APT_BOOTSTRAP),
                    "published_size": len(APT_BOOTSTRAP),
                    "new": True,
                },
                "rpm": {
                    "name": "wukongim-release",
                    "version": BOOTSTRAP_VERSION,
                    "architecture": "noarch",
                    "filename": rpm_filename,
                    "repository_path": (
                        "rpm/preview/el/9/x86_64/Packages/" + rpm_filename
                    ),
                    "download_path": f"bootstrap/{rpm_filename}",
                    "source_sha256": digest(b"unsigned rpm bootstrap package\n"),
                    "source_size": len(b"unsigned rpm bootstrap package\n"),
                    "published_sha256": digest(RPM_BOOTSTRAP),
                    "published_size": len(RPM_BOOTSTRAP),
                    "new": True,
                },
            },
        }
        self.write_bootstrap()
        self.commands: list[list[str]] = []

    def write_snapshot(self) -> None:
        self.snapshot.write_text(
            json.dumps(self.snapshot_value, sort_keys=True, separators=(",", ":")) + "\n",
            encoding="utf-8",
        )

    def write_bootstrap(self) -> None:
        manifest = self.site / "bootstrap/manifest.json"
        manifest.parent.mkdir(parents=True, exist_ok=True)
        manifest.write_text(
            json.dumps(self.bootstrap_value, sort_keys=True, separators=(",", ":"))
            + "\n",
            encoding="utf-8",
        )
        for family, payload in (("apt", APT_BOOTSTRAP), ("rpm", RPM_BOOTSTRAP)):
            item = self.bootstrap_value["packages"][family]
            for field in ("repository_path", "download_path"):
                path = self.site.joinpath(*Path(item[field]).parts)
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(payload)
        (self.site / validator.COMPOSER.REPOSITORY_ENTRYPOINT_PATH).write_bytes(
            validator.COMPOSER.repository_entrypoint_bytes(
                self.bootstrap_value, digest(RPM_CERTIFICATE)
            )
        )

    def remove_bootstrap(self) -> None:
        for family in ("apt", "rpm"):
            item = self.bootstrap_value["packages"][family]
            for field in ("repository_path", "download_path"):
                self.site.joinpath(*Path(item[field]).parts).unlink()
        (self.site / "bootstrap/manifest.json").unlink()
        (self.site / validator.COMPOSER.REPOSITORY_ENTRYPOINT_PATH).unlink()

    def add_retained_payloads(self) -> None:
        not_before = "2026-09-02T00:00:00Z"
        self.snapshot_value["releases"].insert(0, {
            "version": RETAINED_VERSION,
            "source_sha": "9" * 40,
            "source_release_id": 90,
            "package_release_id": 190,
            "deb_sha256": digest(RETAINED_DEB_PAYLOAD),
            "rpm_sha256": digest(b"retained unsigned rpm payload\n"),
            "state": "index_removed",
            "not_before": not_before,
        })
        self.snapshot_value["retirement"] = {
            "phase": "indexes_removed",
            "version": RETAINED_VERSION,
            "not_before": not_before,
        }
        self.snapshot_value["payloads"]["apt"].insert(0, {
            "version": RETAINED_VERSION,
            "path": (
                "apt/pool/main/w/wukongim/"
                f"wukongim_{RETAINED_VERSION}_linux_amd64.deb"
            ),
            "source_sha256": digest(RETAINED_DEB_PAYLOAD),
            "published_sha256": digest(RETAINED_DEB_PAYLOAD),
            "indexed": False,
        })
        self.snapshot_value["payloads"]["rpm"].insert(0, {
            "version": RETAINED_VERSION,
            "path": (
                "rpm/preview/el/9/x86_64/Packages/"
                f"wukongim_{RETAINED_VERSION}_linux_amd64.rpm"
            ),
            "source_sha256": digest(b"retained unsigned rpm payload\n"),
            "published_sha256": digest(RETAINED_RPM_PAYLOAD),
            "indexed": False,
        })
        self.write_snapshot()

    def status(self, **overrides):
        value = {
            "schema": validator.STATUS_SCHEMA,
            "apt": True,
            "rpm": True,
            "reason": "ready",
            "audit_release_id": AUDIT_RELEASE_ID,
            "control_sha": CONTROL_SHA,
            "snapshot_sha256": digest(self.snapshot.read_bytes()),
            "operation": "add_release",
            "target_version": VERSION,
        }
        value.update(overrides)
        return value

    def remote_files(self, status=None):
        values = {
            "https://packages.example/status.json": json.dumps(
                status or self.status()
            ).encode(),
            "https://packages.example/keys/apt-preview.asc": APT_CERTIFICATE,
            "https://packages.example/keys/rpm-preview.asc": RPM_CERTIFICATE,
            "https://packages.example/bootstrap/manifest.json": (
                self.site / "bootstrap/manifest.json"
            ).read_bytes(),
            "https://packages.example/repo": (
                self.site / validator.COMPOSER.REPOSITORY_ENTRYPOINT_PATH
            ).read_bytes(),
        }
        for family, payload in (("apt", APT_BOOTSTRAP), ("rpm", RPM_BOOTSTRAP)):
            item = self.bootstrap_value["packages"][family]
            for field in ("repository_path", "download_path"):
                values[f"https://packages.example/{item[field]}"] = payload
        for family in ("apt", "rpm"):
            for item in self.snapshot_value["payloads"][family]:
                payload = {
                    ("apt", VERSION): DEB_PAYLOAD,
                    ("rpm", VERSION): RPM_PAYLOAD,
                    ("apt", RETAINED_VERSION): RETAINED_DEB_PAYLOAD,
                    ("rpm", RETAINED_VERSION): RETAINED_RPM_PAYLOAD,
                }[(family, item["version"])]
                values[f"https://packages.example/{item['path']}"] = payload
        return values

    @staticmethod
    def _mount_source(command: list[str], destination: str) -> Path:
        for index, value in enumerate(command):
            if value == "--volume":
                volume = command[index + 1]
                suffix = f":{destination}:rw"
                if volume.endswith(suffix):
                    return Path(volume[: -len(suffix)])
        raise AssertionError(f"missing {destination} mount in {command}")

    def runner(self, raw_command) -> None:
        command = list(raw_command)
        self.commands.append(command)
        output = self._mount_source(command, "/downloads")
        if stat.S_IMODE(output.stat().st_mode) != 0o777:
            raise AssertionError("container output directory is not DAC-writable")
        image = next(
            value for value in command
            if "@sha256:" in value and not value.startswith("WK_")
        )
        if image in {value for _, value in validator.APT_CLIENTS}:
            (output / f"wukongim_{VERSION}_amd64.deb").write_bytes(DEB_PAYLOAD)
        elif image in {value for _, value in validator.RPM_CLIENTS}:
            (output / "wukongim.rpm").write_bytes(RPM_PAYLOAD)
        else:
            raise AssertionError(f"unexpected image: {image}")

    def local_validate(self, runner=None, expected_version=None):
        return validator.validate_clients(
            site_root=self.site,
            snapshot_path=self.snapshot,
            base_url=None,
            apt_public_cert=self.apt_certificate,
            rpm_public_cert=self.rpm_certificate,
            expected_version=expected_version,
            runner=runner or self.runner,
        )


class ValidateProductionPackageClientsTest(unittest.TestCase):
    def test_local_mode_runs_four_pinned_clients_and_matches_snapshot(self) -> None:
        with TemporaryDirectory() as temporary:
            fixture = Fixture(Path(temporary))
            receipt = fixture.local_validate()

        self.assertEqual(validator.RECEIPT_SCHEMA, receipt["schema"])
        self.assertEqual("local", receipt["mode"])
        self.assertEqual(2, len(receipt["apt"]))
        self.assertEqual(2, len(receipt["rpm"]))
        self.assertEqual(6, len(fixture.commands))
        self.assertIsNone(receipt["expected_version"])
        self.assertFalse(receipt["expected_version_verified"])
        self.assertTrue(receipt["bootstrap_verified"])
        self.assertTrue(receipt["repository_entrypoint_executed"])
        self.assertEqual({"apt", "rpm"}, set(receipt["entrypoint_execution"]))
        self.assertEqual(BOOTSTRAP_VERSION, receipt["bootstrap"]["version"])
        self.assertRegex(receipt["bootstrap"]["manifest_sha256"], r"^[0-9a-f]{64}$")
        self.assertEqual(
            "repo", receipt["bootstrap"]["repository_entrypoint"]["path"]
        )
        self.assertNotIn("base_url", receipt)
        self.assertRegex(receipt["snapshot_sha256"], r"^[0-9a-f]{64}$")
        for family, payload in (("apt", DEB_PAYLOAD), ("rpm", RPM_PAYLOAD)):
            for result in receipt[family]:
                self.assertTrue(result["bootstrap_installed"])
                self.assertEqual(digest(payload), result["download"]["sha256"])
                self.assertEqual([VERSION], result["download"]["snapshot_versions"])
                self.assertIn("--network", fixture.commands.pop(0))

    def test_expected_add_release_version_must_be_the_downloaded_candidate(self) -> None:
        target_version = "3.1.0-rc.2"
        target_deb = b"authenticated target deb payload\n"
        target_rpm = b"authenticated target rpm payload\n"
        with TemporaryDirectory() as temporary:
            fixture = Fixture(Path(temporary))
            fixture.snapshot_value["releases"].append({
                "version": target_version,
                "source_sha": "b" * 40,
                "source_release_id": 101,
                "package_release_id": 201,
                "deb_sha256": digest(target_deb),
                "rpm_sha256": digest(b"unsigned target rpm payload\n"),
                "state": "active",
                "not_before": None,
            })
            fixture.snapshot_value["payloads"]["apt"].append({
                "version": target_version,
                "path": (
                    "apt/pool/main/w/wukongim/"
                    f"wukongim_{target_version}_linux_amd64.deb"
                ),
                "source_sha256": digest(target_deb),
                "published_sha256": digest(target_deb),
                "indexed": True,
            })
            fixture.snapshot_value["payloads"]["rpm"].append({
                "version": target_version,
                "path": (
                    "rpm/preview/el/9/x86_64/Packages/"
                    f"wukongim_{target_version}_linux_amd64.rpm"
                ),
                "source_sha256": digest(b"unsigned target rpm payload\n"),
                "published_sha256": digest(target_rpm),
                "indexed": True,
            })
            fixture.write_snapshot()

            with self.assertRaisesRegex(
                validator.ClientValidationError,
                f"downloaded a version other than expected {re.escape(target_version)}",
            ):
                fixture.local_validate(expected_version=target_version)

    def test_expected_version_success_is_recorded_in_receipt(self) -> None:
        with TemporaryDirectory() as temporary:
            fixture = Fixture(Path(temporary))
            receipt = fixture.local_validate(expected_version=VERSION)
        self.assertEqual(VERSION, receipt["expected_version"])
        self.assertTrue(receipt["expected_version_verified"])
        for family in ("apt", "rpm"):
            self.assertTrue(all(
                item["download"]["snapshot_versions"] == [VERSION]
                for item in receipt[family]
            ))

    def test_expected_version_requires_exact_indexed_snapshot_entry(self) -> None:
        with TemporaryDirectory() as temporary:
            fixture = Fixture(Path(temporary))
            with self.assertRaisesRegex(
                validator.ClientValidationError,
                "does not contain exactly expected version",
            ):
                fixture.local_validate(expected_version="3.1.0-rc.2")
            self.assertEqual([], fixture.commands)

    def test_commands_are_digest_pinned_hardened_and_download_only(self) -> None:
        with TemporaryDirectory() as temporary:
            fixture = Fixture(Path(temporary))
            downloads = Path(temporary) / "downloads"
            downloads.mkdir()
            clients = (
                ("apt", validator.APT_CLIENTS, fixture.apt_certificate),
                ("rpm", validator.RPM_CLIENTS, fixture.rpm_certificate),
            )
            for family, values, certificate in clients:
                for _, image in values:
                    self.assertRegex(image, r"^[^@]+@sha256:[0-9a-f]{64}$")
                    command = validator._client_command(
                        image, downloads, certificate, family, fixture.site, None
                    )
                    rendered = "\n".join(command)
                    self.assertIn("--read-only", command)
                    self.assertIn("--cap-drop", command)
                    self.assertIn("no-new-privileges", command)
                    self.assertIn("--network", command)
                    self.assertIn("none", command)
                    self.assertNotRegex(rendered, r"apt-get[^\n]*install[^\n]*wukongim(?:\s|$)")
                    if family == "apt":
                        self.assertIn("dpkg-query -W", rendered)
                        self.assertEqual(2, rendered.count("dpkg-query -W"))
                        self.assertIn("apt-get \"${apt_options[@]}\" update", rendered)
                        self.assertIn("apt-get \"${apt_options[@]}\" download", rendered)
                        self.assertIn("signed-by=/keys/apt-preview.asc", rendered)
                        self.assertIn(
                            'apt-get "${bootstrap_apt_options[@]}" install',
                            rendered,
                        )
                        self.assertIn('"$WK_BOOTSTRAP_PACKAGE"', rendered)
                        self.assertIn("wukongim-archive-keyring", rendered)
                        self.assertIn("wukongim-preview.sources", rendered)
                        self.assertIn(
                            "Signed-By: /usr/share/keyrings/wukongim-archive-keyring.pgp",
                            rendered,
                        )
                        self.assertNotIn("WK_CA_BUNDLE=", rendered)
                        self.assertNotIn(validator.APT_CA_BUNDLE_PATH, rendered)
                    else:
                        self.assertIn("repo_gpgcheck=1", rendered)
                        self.assertIn(
                            'cp -a /var/lib/rpm/. "$client_root/var/lib/rpm/"',
                            rendered,
                        )
                        self.assertEqual(
                            2,
                            rendered.count(
                                'if rpm --root "$client_root" -q wukongim '
                                '>/dev/null 2>&1; then'
                            ),
                        )
                        self.assertIn(
                            'rpm --root "$client_root" -q systemd', rendered
                        )
                        self.assertIn('--installroot="$client_root"', rendered)
                        self.assertIn("--releasever=9", rendered)
                        self.assertIn("dnf \"${dnf_options[@]}\" makecache", rendered)
                        self.assertIn("install --downloadonly", rendered)
                        self.assertIn("--downloaddir=/downloads wukongim", rendered)
                        self.assertIn(
                            'rpm --dbpath /tmp/bootstrap-rpmdb --install "$WK_BOOTSTRAP_PACKAGE"',
                            rendered,
                        )
                        self.assertIn("wukongim-release", rendered)
                        self.assertIn(
                            "gpgkey=file:///etc/pki/rpm-gpg/RPM-GPG-KEY-wukongim-preview",
                            rendered,
                        )
                        self.assertIn("rpmkeys --dbpath /tmp/rpmdb --checksig", rendered)
                        self.assertIn("sha256sum <", rendered)
                        self.assertNotIn("cmp ", rendered)
                        self.assertNotIn("repoquery", rendered)
                        self.assertNotIn("curl ", rendered)

    def test_docker_options_precede_image_and_container_command_follows_image(self) -> None:
        with TemporaryDirectory() as temporary:
            fixture = Fixture(Path(temporary))
            downloads = Path(temporary) / "downloads"
            downloads.mkdir()
            clients = (
                (
                    "apt",
                    validator.APT_CLIENTS,
                    fixture.apt_certificate,
                    "file:/site/apt",
                    validator.APT_SCRIPT,
                ),
                (
                    "rpm",
                    validator.RPM_CLIENTS,
                    fixture.rpm_certificate,
                    "file:///site/rpm/preview/el/9/x86_64",
                    validator.RPM_SCRIPT,
                ),
            )
            for family, values, certificate, repository, script in clients:
                for _, image in values:
                    command = validator._client_command(
                        image, downloads, certificate, family, fixture.site, None
                    )
                    image_index = command.index(image)
                    environment_index = command.index("--env")
                    self.assertLess(environment_index, image_index)
                    self.assertEqual(
                        f"WK_REPOSITORY_URL={repository}",
                        command[environment_index + 1],
                    )
                    self.assertEqual(
                        ["bash", "-c", script], command[image_index + 1:]
                    )

    def test_remote_apt_explicitly_uses_the_mounted_host_ca_bundle(self) -> None:
        with TemporaryDirectory() as temporary:
            fixture = Fixture(Path(temporary))
            downloads = Path(temporary) / "downloads"
            downloads.mkdir()
            ca_bundle = Path(temporary) / "ca-certificates.crt"
            ca_bundle.write_bytes(b"host ca\n")
            for _, image in validator.APT_CLIENTS:
                with mock.patch.object(
                    validator, "_host_ca_bundle", return_value=ca_bundle
                ):
                    command = validator._client_command(
                        image,
                        downloads,
                        fixture.apt_certificate,
                        "apt",
                        None,
                        "https://packages.example",
                    )

                image_index = command.index(image)
                self.assertIn(
                    f"{ca_bundle}:{validator.APT_CA_BUNDLE_PATH}:ro",
                    command[:image_index],
                )
                self.assertIn(
                    f"WK_CA_BUNDLE={validator.APT_CA_BUNDLE_PATH}",
                    command[:image_index],
                )
                self.assertIn(
                    'Acquire::https::CaInfo="$WK_CA_BUNDLE"',
                    command[image_index + 3],
                )

    def test_default_runner_routes_container_logs_away_from_json_stdout(self) -> None:
        command = ["docker", "run", "pinned-image"]
        with mock.patch.object(validator.subprocess, "run") as run:
            validator._run(command)

        run.assert_called_once_with(
            command,
            check=True,
            stdout=validator.sys.stderr,
            stderr=validator.sys.stderr,
        )

    def test_remote_mode_pins_endpoint_keys_and_uses_https_clients(self) -> None:
        with TemporaryDirectory() as temporary:
            fixture = Fixture(Path(temporary))
            ca_bundle = Path(temporary) / "ca-certificates.crt"
            ca_bundle.write_bytes(b"host ca\n")
            status = {
                "schema": validator.STATUS_SCHEMA,
                "apt": True,
                "rpm": True,
                "reason": "ready",
                "audit_release_id": 4242,
                "control_sha": "1" * 40,
                "snapshot_sha256": "2" * 64,
                "operation": "add_release",
                "target_version": VERSION,
            }
            fetched: list[str] = []

            values = fixture.remote_files(status)

            def fetcher(url: str, maximum: int) -> bytes:
                fetched.append(url)
                self.assertLessEqual(len(values[url]), maximum)
                return values[url]

            with mock.patch.object(validator, "_host_ca_bundle", return_value=ca_bundle):
                receipt = validator.validate_clients(
                    site_root=None,
                    snapshot_path=None,
                    base_url="https://packages.example/",
                    apt_public_cert=fixture.apt_certificate,
                    rpm_public_cert=fixture.rpm_certificate,
                    runner=fixture.runner,
                    fetcher=fetcher,
                )

        self.assertEqual("remote", receipt["mode"])
        self.assertEqual("https://packages.example", receipt["base_url"])
        self.assertEqual(4242, receipt["audit_release_id"])
        self.assertEqual("1" * 40, receipt["control_sha"])
        self.assertFalse(receipt["snapshot_verified"])
        self.assertTrue(receipt["status_revalidated"])
        self.assertTrue(receipt["bootstrap_verified"])
        self.assertEqual(10, len(fetched))
        self.assertEqual(2, fetched.count("https://packages.example/status.json"))
        entrypoint_commands = [
            command for command in fixture.commands
            if ":/repo:ro" in "\n".join(command)
        ]
        client_commands = [
            command for command in fixture.commands if command not in entrypoint_commands
        ]
        self.assertEqual(2, len(entrypoint_commands))
        self.assertEqual(4, len(client_commands))
        self.assertTrue(receipt["repository_entrypoint_executed"])
        self.assertEqual({"apt", "rpm"}, set(receipt["entrypoint_execution"]))
        for value in receipt["entrypoint_execution"].values():
            self.assertEqual(2, value["executions"])
            self.assertTrue(value["idempotent"])
            self.assertTrue(value["product_absent"])
        for command in entrypoint_commands:
            self.assertEqual("none", command[command.index("--network") + 1])
            self.assertIn(":/usr/bin/curl:ro", "\n".join(command))
            self.assertEqual(2, command[-1].count("sh /repo"))
        for command in client_commands:
            self.assertNotIn("--network", command)
            rendered = "\n".join(command)
            self.assertIn("https://packages.example/", rendered)
        apt_commands = [
            command for command in client_commands
            if any(image in command for _, image in validator.APT_CLIENTS)
        ]
        self.assertEqual(2, len(apt_commands))
        self.assertTrue(all(str(ca_bundle) in "\n".join(item) for item in apt_commands))
        self.assertTrue(
            all(
                f"WK_CA_BUNDLE={validator.APT_CA_BUNDLE_PATH}" in item
                for item in apt_commands
            )
        )
        rpm_commands = [
            command for command in client_commands
            if any(image in command for _, image in validator.RPM_CLIENTS)
        ]
        self.assertEqual(2, len(rpm_commands))
        self.assertTrue(
            all(str(ca_bundle) not in "\n".join(item) for item in rpm_commands)
        )
        self.assertTrue(
            all("WK_CA_BUNDLE=" not in "\n".join(item) for item in rpm_commands)
        )

    def test_remote_snapshot_closes_over_indexed_and_retained_payloads(self) -> None:
        with TemporaryDirectory() as temporary:
            fixture = Fixture(Path(temporary))
            fixture.add_retained_payloads()
            ca_bundle = Path(temporary) / "ca-certificates.crt"
            ca_bundle.write_bytes(b"host ca\n")
            files = fixture.remote_files()
            fetched: list[str] = []

            def fetcher(url: str, maximum: int) -> bytes:
                fetched.append(url)
                value = files[url]
                self.assertLessEqual(len(value), maximum)
                return value

            with mock.patch.object(validator, "_host_ca_bundle", return_value=ca_bundle):
                receipt = validator.validate_clients(
                    site_root=None,
                    snapshot_path=fixture.snapshot,
                    base_url="https://packages.example/",
                    apt_public_cert=fixture.apt_certificate,
                    rpm_public_cert=fixture.rpm_certificate,
                    runner=fixture.runner,
                    fetcher=fetcher,
                )
            snapshot_digest = digest(fixture.snapshot.read_bytes())

        self.assertTrue(receipt["snapshot_verified"])
        self.assertTrue(receipt["status_revalidated"])
        self.assertEqual(snapshot_digest, receipt["snapshot_sha256"])
        retained_urls = {
            f"https://packages.example/{item['path']}"
            for family in ("apt", "rpm")
            for item in fixture.snapshot_value["payloads"][family]
            if item["indexed"] is False
        }
        self.assertTrue(retained_urls.issubset(fetched))
        payload_urls = {
            f"https://packages.example/{item['path']}"
            for family in ("apt", "rpm")
            for item in fixture.snapshot_value["payloads"][family]
        }
        self.assertTrue(payload_urls.issubset(fetched))
        for family in ("apt", "rpm"):
            for result in receipt[family]:
                self.assertEqual([VERSION], result["download"]["snapshot_versions"])

    def test_remote_snapshot_accepts_reviewed_beta_order_but_rejects_duplicates(self) -> None:
        for duplicate in (False, True):
            with self.subTest(duplicate=duplicate), TemporaryDirectory() as temporary, mock.patch.dict(
                globals(), {"VERSION": "3.0.0-beta.12", "RETAINED_VERSION": "3.0.0-beta.9"}
            ):
                fixture = Fixture(Path(temporary))
                fixture.add_retained_payloads()
                # The composer preserves reviewed release order but emits payloads
                # in canonical lexical order, which differs for beta.9/beta.12.
                for family in ("apt", "rpm"):
                    fixture.snapshot_value["payloads"][family].sort(key=lambda item: item["version"])
                if duplicate:
                    fixture.snapshot_value["releases"].append(dict(fixture.snapshot_value["releases"][0]))
                fixture.write_snapshot()
                files = fixture.remote_files()
                ca_bundle = Path(temporary) / "ca-certificates.crt"
                ca_bundle.write_bytes(b"host ca\n")
                with mock.patch.object(validator, "_host_ca_bundle", return_value=ca_bundle):
                    def validate():
                        return validator.validate_clients(
                            site_root=None,
                            snapshot_path=fixture.snapshot,
                            base_url="https://packages.example/",
                            apt_public_cert=fixture.apt_certificate,
                            rpm_public_cert=fixture.rpm_certificate,
                            expected_version=VERSION,
                            runner=fixture.runner,
                            fetcher=lambda url, maximum: files[url],
                        )
                    if duplicate:
                        with self.assertRaisesRegex(validator.ClientValidationError, "versions must be unique"):
                            validate()
                        self.assertEqual([], fixture.commands)
                    else:
                        receipt = validate()
                        self.assertTrue(receipt["snapshot_verified"])
                        self.assertTrue(receipt["expected_version_verified"])
                        for family in ("apt", "rpm"):
                            for result in receipt[family]:
                                self.assertEqual([VERSION], result["download"]["snapshot_versions"])

    def test_remote_status_bytes_must_remain_exact_through_client_downloads(self) -> None:
        with TemporaryDirectory() as temporary:
            fixture = Fixture(Path(temporary))
            files = fixture.remote_files()
            initial_status = files["https://packages.example/status.json"]
            changed_status = json.dumps(
                fixture.status(reason="replaced"), sort_keys=True
            ).encode()
            status_reads = 0

            def fetcher(url: str, maximum: int) -> bytes:
                nonlocal status_reads
                if url == "https://packages.example/status.json":
                    status_reads += 1
                    return initial_status if status_reads == 1 else changed_status
                return files[url]

            ca_bundle = Path(temporary) / "ca-certificates.crt"
            ca_bundle.write_bytes(b"host ca\n")
            with mock.patch.object(validator, "_host_ca_bundle", return_value=ca_bundle):
                with self.assertRaisesRegex(
                    validator.ClientValidationError,
                    "status.json changed during client validation",
                ):
                    validator.validate_clients(
                        site_root=None,
                        snapshot_path=fixture.snapshot,
                        base_url="https://packages.example",
                        apt_public_cert=fixture.apt_certificate,
                        rpm_public_cert=fixture.rpm_certificate,
                        runner=fixture.runner,
                        fetcher=fetcher,
                    )

            self.assertEqual(2, status_reads)
            self.assertEqual(6, len(fixture.commands))

    def test_remote_snapshot_rejects_missing_and_tampered_payloads(self) -> None:
        with TemporaryDirectory() as temporary:
            fixture = Fixture(Path(temporary))
            fixture.add_retained_payloads()
            files = fixture.remote_files()
            retained_path = fixture.snapshot_value["payloads"]["apt"][0]["path"]
            retained_url = f"https://packages.example/{retained_path}"

            def missing_fetcher(url: str, maximum: int) -> bytes:
                if url == retained_url:
                    raise validator.ClientValidationError("public endpoint read failed: 404")
                return files[url]

            with self.assertRaisesRegex(
                validator.ClientValidationError, "public endpoint read failed"
            ):
                validator.validate_clients(
                    site_root=None,
                    snapshot_path=fixture.snapshot,
                    base_url="https://packages.example",
                    apt_public_cert=fixture.apt_certificate,
                    rpm_public_cert=fixture.rpm_certificate,
                    runner=mock.Mock(),
                    fetcher=missing_fetcher,
                )

            def tampered_fetcher(url: str, maximum: int) -> bytes:
                if url == retained_url:
                    return b"tampered retained payload"
                return files[url]

            with self.assertRaisesRegex(
                validator.ClientValidationError,
                "remote apt payload .* digest differs from reviewed snapshot",
            ):
                validator.validate_clients(
                    site_root=None,
                    snapshot_path=fixture.snapshot,
                    base_url="https://packages.example",
                    apt_public_cert=fixture.apt_certificate,
                    rpm_public_cert=fixture.rpm_certificate,
                    runner=mock.Mock(),
                    fetcher=tampered_fetcher,
                )

    def test_remote_snapshot_path_must_exist_before_docker(self) -> None:
        with TemporaryDirectory() as temporary:
            fixture = Fixture(Path(temporary))
            files = fixture.remote_files()
            runner = mock.Mock()
            with self.assertRaisesRegex(
                validator.ClientValidationError, "cannot open reviewed snapshot"
            ):
                validator.validate_clients(
                    site_root=None,
                    snapshot_path=Path(temporary) / "missing-snapshot.json",
                    base_url="https://packages.example",
                    apt_public_cert=fixture.apt_certificate,
                    rpm_public_cert=fixture.rpm_certificate,
                    runner=runner,
                    fetcher=lambda url, maximum: files[url],
                )
            runner.assert_not_called()

    def test_remote_snapshot_rejects_unsafe_path_before_docker(self) -> None:
        with TemporaryDirectory() as temporary:
            fixture = Fixture(Path(temporary))
            fixture.snapshot_value["payloads"]["apt"][0]["path"] = (
                "apt/pool/main/w/wukongim/../../escape.deb"
            )
            fixture.write_snapshot()
            files = fixture.remote_files()
            runner = mock.Mock()

            with self.assertRaisesRegex(validator.ClientValidationError, "path is unsafe"):
                validator.validate_clients(
                    site_root=None,
                    snapshot_path=fixture.snapshot,
                    base_url="https://packages.example",
                    apt_public_cert=fixture.apt_certificate,
                    rpm_public_cert=fixture.rpm_certificate,
                    runner=runner,
                    fetcher=lambda url, maximum: files[url],
                )
            runner.assert_not_called()

    def test_remote_snapshot_identity_must_exactly_match_status(self) -> None:
        cases = (
            ("snapshot_sha256", {"snapshot_sha256": "f" * 64}, "SHA-256 differs"),
            ("audit_release_id", {"audit_release_id": 4243}, "audit_release_id differs"),
            ("control_sha", {"control_sha": "9" * 40}, "control_sha differs"),
        )
        for name, overrides, pattern in cases:
            with self.subTest(name=name), TemporaryDirectory() as temporary:
                fixture = Fixture(Path(temporary))
                status = fixture.status(**overrides)
                files = fixture.remote_files(status)
                with self.assertRaisesRegex(validator.ClientValidationError, pattern):
                    validator.validate_clients(
                        site_root=None,
                        snapshot_path=fixture.snapshot,
                        base_url="https://packages.example",
                        apt_public_cert=fixture.apt_certificate,
                        rpm_public_cert=fixture.rpm_certificate,
                        runner=mock.Mock(),
                        fetcher=lambda url, maximum: files[url],
                    )

    def test_remote_snapshot_requires_canonical_strict_reviewed_key_facts(self) -> None:
        with TemporaryDirectory() as temporary:
            fixture = Fixture(Path(temporary))
            fixture.snapshot_value["public_keys"]["apt"]["size"] += 1
            fixture.write_snapshot()
            files = fixture.remote_files()
            with self.assertRaisesRegex(
                validator.ClientValidationError,
                "public-key size differs from the reviewed certificate",
            ):
                validator.validate_clients(
                    site_root=None,
                    snapshot_path=fixture.snapshot,
                    base_url="https://packages.example",
                    apt_public_cert=fixture.apt_certificate,
                    rpm_public_cert=fixture.rpm_certificate,
                    runner=mock.Mock(),
                    fetcher=lambda url, maximum: files[url],
                )

            fixture.snapshot_value["public_keys"]["apt"]["size"] -= 1
            fixture.snapshot.write_text(
                json.dumps(fixture.snapshot_value, indent=2), encoding="utf-8"
            )
            status = fixture.status(snapshot_sha256=digest(fixture.snapshot.read_bytes()))
            files = fixture.remote_files(status)
            with self.assertRaisesRegex(
                validator.ClientValidationError, "canonical JSON encoding"
            ):
                validator.validate_clients(
                    site_root=None,
                    snapshot_path=fixture.snapshot,
                    base_url="https://packages.example",
                    apt_public_cert=fixture.apt_certificate,
                    rpm_public_cert=fixture.rpm_certificate,
                    runner=mock.Mock(),
                    fetcher=lambda url, maximum: files[url],
                )

    def test_remote_snapshot_download_must_be_an_indexed_digest(self) -> None:
        with TemporaryDirectory() as temporary:
            fixture = Fixture(Path(temporary))
            fixture.add_retained_payloads()
            files = fixture.remote_files()

            def retained_runner(raw_command) -> None:
                command = list(raw_command)
                output = fixture._mount_source(command, "/downloads")
                image = next(value for value in command if "@sha256:" in value)
                if image in {value for _, value in validator.APT_CLIENTS}:
                    (output / "retained.deb").write_bytes(RETAINED_DEB_PAYLOAD)
                else:
                    (output / "retained.rpm").write_bytes(RETAINED_RPM_PAYLOAD)

            with self.assertRaisesRegex(
                validator.ClientValidationError, "absent from the indexed snapshot"
            ):
                validator.validate_clients(
                    site_root=None,
                    snapshot_path=fixture.snapshot,
                    base_url="https://packages.example",
                    apt_public_cert=fixture.apt_certificate,
                    rpm_public_cert=fixture.rpm_certificate,
                    runner=retained_runner,
                    fetcher=lambda url, maximum: files[url],
                )

    def test_bootstrap_manifest_and_both_package_paths_are_verified_before_clients(self) -> None:
        with TemporaryDirectory() as temporary:
            fixture = Fixture(Path(temporary))
            runner = mock.Mock()
            manifest = fixture.site / "bootstrap/manifest.json"
            manifest.write_text(
                json.dumps(fixture.bootstrap_value, indent=2), encoding="utf-8"
            )
            with self.assertRaisesRegex(
                validator.ClientValidationError, "canonical JSON encoding"
            ):
                fixture.local_validate(runner)
            runner.assert_not_called()

        for family, field in (("apt", "download_path"), ("rpm", "repository_path")):
            with self.subTest(family=family, field=field), TemporaryDirectory() as temporary:
                fixture = Fixture(Path(temporary))
                item = fixture.bootstrap_value["packages"][family]
                fixture.site.joinpath(*Path(item[field]).parts).write_bytes(b"tampered")
                runner = mock.Mock()
                with self.assertRaisesRegex(
                    validator.ClientValidationError,
                    f"published {family} bootstrap .* differs from the bootstrap manifest",
                ):
                    fixture.local_validate(runner)
                runner.assert_not_called()

    def test_remote_bootstrap_paths_are_fetched_and_digest_checked(self) -> None:
        with TemporaryDirectory() as temporary:
            fixture = Fixture(Path(temporary))
            files = fixture.remote_files()
            fetched: list[str] = []

            def fetcher(url: str, maximum: int) -> bytes:
                fetched.append(url)
                return files[url]

            ca_bundle = Path(temporary) / "ca-certificates.crt"
            ca_bundle.write_bytes(b"host ca\n")
            with mock.patch.object(validator, "_host_ca_bundle", return_value=ca_bundle):
                receipt = validator.validate_clients(
                    site_root=None,
                    snapshot_path=fixture.snapshot,
                    base_url="https://packages.example",
                    apt_public_cert=fixture.apt_certificate,
                    rpm_public_cert=fixture.rpm_certificate,
                    runner=fixture.runner,
                    fetcher=fetcher,
                )

            expected_bootstrap_urls = {
                f"https://packages.example/{item[field]}"
                for item in fixture.bootstrap_value["packages"].values()
                for field in ("repository_path", "download_path")
            }
            self.assertTrue(expected_bootstrap_urls.issubset(fetched))
            self.assertTrue(receipt["bootstrap_verified"])

            apt_direct = fixture.bootstrap_value["packages"]["apt"]["download_path"]
            files[f"https://packages.example/{apt_direct}"] = b"tampered"
            with self.assertRaisesRegex(
                validator.ClientValidationError,
                "remote apt bootstrap direct package .* differs from the bootstrap manifest",
            ):
                validator.validate_clients(
                    site_root=None,
                    snapshot_path=fixture.snapshot,
                    base_url="https://packages.example",
                    apt_public_cert=fixture.apt_certificate,
                    rpm_public_cert=fixture.rpm_certificate,
                    runner=mock.Mock(),
                    fetcher=lambda url, maximum: files[url],
                )

    def test_repository_entrypoint_bytes_are_verified_before_clean_clients(self) -> None:
        with TemporaryDirectory() as temporary:
            fixture = Fixture(Path(temporary))
            entrypoint = fixture.site / validator.COMPOSER.REPOSITORY_ENTRYPOINT_PATH
            entrypoint.write_bytes(entrypoint.read_bytes() + b"# tampered\n")
            runner = mock.Mock()
            with self.assertRaisesRegex(
                validator.ClientValidationError,
                "published repository setup entrypoint differs",
            ):
                fixture.local_validate(runner)
            runner.assert_not_called()

        with TemporaryDirectory() as temporary:
            fixture = Fixture(Path(temporary))
            files = fixture.remote_files()
            files["https://packages.example/repo"] += b"# tampered\n"
            runner = mock.Mock()
            with self.assertRaisesRegex(
                validator.ClientValidationError,
                "remote repository setup entrypoint differs",
            ):
                validator.validate_clients(
                    site_root=None,
                    snapshot_path=fixture.snapshot,
                    base_url="https://packages.example",
                    apt_public_cert=fixture.apt_certificate,
                    rpm_public_cert=fixture.rpm_certificate,
                    runner=runner,
                    fetcher=lambda url, maximum: files[url],
                )
            runner.assert_not_called()

    def test_exact_first_bootstrap_audit_may_omit_repository_entrypoint(self) -> None:
        with TemporaryDirectory() as temporary:
            fixture = Fixture(Path(temporary))
            audit_id, control_sha = next(
                iter(validator.LEGACY_REPOSITORY_ENTRYPOINT_FREE_IDENTITIES)
            )
            fixture.snapshot_value["audit_release_id"] = audit_id
            fixture.snapshot_value["control_sha"] = control_sha
            fixture.write_snapshot()
            status = fixture.status(
                audit_release_id=audit_id,
                control_sha=control_sha,
                snapshot_sha256=digest(fixture.snapshot.read_bytes()),
            )
            files = fixture.remote_files(status)
            files.pop("https://packages.example/repo")
            ca_bundle = Path(temporary) / "ca-certificates.crt"
            ca_bundle.write_bytes(b"host ca\n")
            with mock.patch.object(validator, "_host_ca_bundle", return_value=ca_bundle):
                receipt = validator.validate_clients(
                    site_root=None,
                    snapshot_path=fixture.snapshot,
                    base_url="https://packages.example",
                    apt_public_cert=fixture.apt_certificate,
                    rpm_public_cert=fixture.rpm_certificate,
                    runner=fixture.runner,
                    fetcher=lambda url, maximum: files[url],
                )
            self.assertIsNone(receipt["bootstrap"]["repository_entrypoint"])

            files["https://packages.example/status.json"] = json.dumps(
                fixture.status(
                    audit_release_id=audit_id,
                    control_sha="0" * 40,
                    snapshot_sha256=digest(fixture.snapshot.read_bytes()),
                )
            ).encode()
            with self.assertRaisesRegex(
                validator.ClientValidationError,
                "remote repository setup entrypoint is required",
            ):
                validator.validate_clients(
                    site_root=None,
                    snapshot_path=None,
                    base_url="https://packages.example",
                    apt_public_cert=fixture.apt_certificate,
                    rpm_public_cert=fixture.rpm_certificate,
                    runner=mock.Mock(),
                    fetcher=lambda url, maximum: files[url],
                )

    def test_only_the_reviewed_legacy_identity_may_omit_bootstrap(self) -> None:
        with TemporaryDirectory() as temporary:
            fixture = Fixture(Path(temporary))
            fixture.snapshot_value["audit_release_id"] = (
                validator.LEGACY_BOOTSTRAPLESS_IDENTITY[0]
            )
            fixture.snapshot_value["control_sha"] = (
                validator.LEGACY_BOOTSTRAPLESS_IDENTITY[1]
            )
            fixture.write_snapshot()
            fixture.remove_bootstrap()
            receipt = fixture.local_validate()

            self.assertFalse(receipt["bootstrap_verified"])
            self.assertIsNone(receipt["bootstrap"])
            self.assertTrue(all(
                not item["bootstrap_installed"]
                for family in ("apt", "rpm")
                for item in receipt[family]
            ))
            self.assertTrue(all(
                "WK_BOOTSTRAP_PACKAGE=" not in command
                for command in fixture.commands
            ))

            legacy_status = fixture.status(
                audit_release_id=validator.LEGACY_BOOTSTRAPLESS_IDENTITY[0],
                control_sha=validator.LEGACY_BOOTSTRAPLESS_IDENTITY[1],
                snapshot_sha256="2" * 64,
            )
            remote_files = {
                "https://packages.example/status.json": json.dumps(legacy_status).encode(),
                "https://packages.example/keys/apt-preview.asc": APT_CERTIFICATE,
                "https://packages.example/keys/rpm-preview.asc": RPM_CERTIFICATE,
            }

            def legacy_fetcher(url: str, maximum: int) -> bytes:
                if url.endswith("/bootstrap/manifest.json"):
                    raise validator.ClientValidationError("public endpoint read failed: 404")
                return remote_files[url]

            ca_bundle = Path(temporary) / "ca-certificates.crt"
            ca_bundle.write_bytes(b"host ca\n")
            fixture.commands.clear()
            with mock.patch.object(validator, "_host_ca_bundle", return_value=ca_bundle):
                remote_receipt = validator.validate_clients(
                    site_root=None,
                    snapshot_path=None,
                    base_url="https://packages.example",
                    apt_public_cert=fixture.apt_certificate,
                    rpm_public_cert=fixture.rpm_certificate,
                    runner=fixture.runner,
                    fetcher=legacy_fetcher,
                )
            self.assertFalse(remote_receipt["bootstrap_verified"])
            self.assertIsNone(remote_receipt["bootstrap"])

        with TemporaryDirectory() as temporary:
            fixture = Fixture(Path(temporary))
            fixture.remove_bootstrap()
            runner = mock.Mock()
            with self.assertRaisesRegex(
                validator.ClientValidationError, "cannot open published bootstrap manifest"
            ):
                fixture.local_validate(runner)
            runner.assert_not_called()

    def test_bootstrap_client_commands_mount_and_install_only_the_bootstrap_package(self) -> None:
        with TemporaryDirectory() as temporary:
            fixture = Fixture(Path(temporary))
            downloads = Path(temporary) / "downloads"
            downloads.mkdir()
            for family, clients, certificate in (
                ("apt", validator.APT_CLIENTS, fixture.apt_certificate),
                ("rpm", validator.RPM_CLIENTS, fixture.rpm_certificate),
            ):
                item = fixture.bootstrap_value["packages"][family]
                package = fixture.site.joinpath(*Path(item["download_path"]).parts)
                command = validator._client_command(
                    clients[0][1], downloads, certificate, family,
                    fixture.site, None, package,
                )
                image_index = command.index(clients[0][1])
                self.assertIn(
                    f"{package.resolve()}:/bootstrap/{package.name}:ro",
                    command[:image_index],
                )
                self.assertIn(
                    f"WK_BOOTSTRAP_PACKAGE=/bootstrap/{package.name}",
                    command[:image_index],
                )
                rendered = "\n".join(command[image_index + 1:])
                if family == "apt":
                    self.assertIn(
                        "/var/lib/apt:rw,noexec,nosuid,nodev,size=256m",
                        command[:image_index],
                    )
                    self.assertIn('apt-get "${apt_options[@]}" download wukongim', rendered)
                    self.assertIn("--no-install-recommends", rendered)
                    self.assertIn("dpkg-deb --control", rendered)
                else:
                    self.assertIn(
                        "/etc/pki/rpm-gpg:rw,noexec,nosuid,nodev,size=8m",
                        command[:image_index],
                    )
                    self.assertIn(
                        "/etc/yum.repos.d:rw,noexec,nosuid,nodev,size=8m",
                        command[:image_index],
                    )
                    self.assertIn("install --downloadonly", rendered)
                    self.assertIn('rpm -qp --scripts "$WK_BOOTSTRAP_PACKAGE"', rendered)
                    self.assertIn("repo_gpgcheck=1", rendered)
                    self.assertIn("gpgkey=file://$installed_key", rendered)

    def test_local_mode_rejects_download_absent_from_indexed_snapshot(self) -> None:
        with TemporaryDirectory() as temporary:
            fixture = Fixture(Path(temporary))

            def tampered_runner(raw_command) -> None:
                command = list(raw_command)
                output = fixture._mount_source(command, "/downloads")
                (output / "tampered.deb").write_bytes(b"tampered")

            with self.assertRaisesRegex(
                validator.ClientValidationError, "absent from the indexed snapshot"
            ):
                fixture.local_validate(tampered_runner)

    def test_remote_mode_rejects_key_substitution_before_starting_docker(self) -> None:
        with TemporaryDirectory() as temporary:
            fixture = Fixture(Path(temporary))
            status = json.dumps({
                "schema": validator.STATUS_SCHEMA,
                "apt": True,
                "rpm": True,
                "reason": "ready",
                "audit_release_id": 9,
                "control_sha": "1" * 40,
                "snapshot_sha256": "2" * 64,
            }).encode()
            runner = mock.Mock()

            def fetcher(url: str, maximum: int) -> bytes:
                if url.endswith("status.json"):
                    return status
                if url.endswith("apt-preview.asc"):
                    return b"substituted certificate"
                return RPM_CERTIFICATE

            with self.assertRaisesRegex(
                validator.ClientValidationError, "remote apt public certificate differs"
            ):
                validator.validate_clients(
                    site_root=None,
                    snapshot_path=None,
                    base_url="https://packages.example",
                    apt_public_cert=fixture.apt_certificate,
                    rpm_public_cert=fixture.rpm_certificate,
                    runner=runner,
                    fetcher=fetcher,
                )
            runner.assert_not_called()

    def test_rejects_missing_indexed_family_and_unsafe_payload_path(self) -> None:
        with TemporaryDirectory() as temporary:
            fixture = Fixture(Path(temporary))
            fixture.snapshot_value["payloads"]["apt"][0]["indexed"] = False
            fixture.write_snapshot()
            with self.assertRaisesRegex(
                validator.ClientValidationError, "no indexed apt payload"
            ):
                fixture.local_validate()

            fixture.snapshot_value["payloads"]["apt"][0]["indexed"] = True
            fixture.snapshot_value["payloads"]["apt"][0]["path"] = "apt/pool/../../escape.deb"
            fixture.write_snapshot()
            with self.assertRaisesRegex(validator.ClientValidationError, "unsafe"):
                fixture.local_validate()

    def test_container_failure_is_fail_closed_and_contextual(self) -> None:
        with TemporaryDirectory() as temporary:
            fixture = Fixture(Path(temporary))

            def failing_runner(raw_command) -> None:
                raise subprocess.CalledProcessError(17, list(raw_command))

            with self.assertRaisesRegex(
                validator.ClientValidationError,
                "ubuntu-24.04 repository setup entrypoint failed",
            ):
                fixture.local_validate(failing_runner)

    def test_mode_and_https_contracts_are_fail_closed(self) -> None:
        with TemporaryDirectory() as temporary:
            fixture = Fixture(Path(temporary))
            cases = (
                {
                    "site_root": fixture.site,
                    "snapshot_path": None,
                    "base_url": None,
                    "pattern": "snapshot is required",
                },
                {
                    "site_root": None,
                    "snapshot_path": None,
                    "base_url": None,
                    "pattern": "exactly one",
                },
                {
                    "site_root": fixture.site,
                    "snapshot_path": fixture.snapshot,
                    "base_url": "https://packages.example",
                    "pattern": "exactly one",
                },
                {
                    "site_root": None,
                    "snapshot_path": None,
                    "base_url": "http://packages.example",
                    "pattern": "absolute HTTPS",
                },
            )
            for case in cases:
                with self.subTest(pattern=case["pattern"]):
                    with self.assertRaisesRegex(
                        validator.ClientValidationError, case["pattern"]
                    ):
                        validator.validate_clients(
                            site_root=case["site_root"],
                            snapshot_path=case["snapshot_path"],
                            base_url=case["base_url"],
                            apt_public_cert=fixture.apt_certificate,
                            rpm_public_cert=fixture.rpm_certificate,
                            runner=mock.Mock(),
                            fetcher=mock.Mock(),
                        )

    def test_pinned_image_inventory_is_exact_and_unique(self) -> None:
        clients = validator.APT_CLIENTS + validator.RPM_CLIENTS
        self.assertEqual(
            ["ubuntu-24.04", "debian-13", "rocky-linux-9", "alma-linux-9"],
            [name for name, _ in clients],
        )
        images = [image for _, image in clients]
        self.assertEqual(len(images), len(set(images)))
        for image in images:
            self.assertIsNotNone(re.fullmatch(r"[^@]+@sha256:[0-9a-f]{64}", image))
        self.assertRegex(
            dict(validator.RPM_CLIENTS)["rocky-linux-9"],
            r"^quay\.io/rockylinux/rockylinux@sha256:[0-9a-f]{64}$",
        )


class StableClientAdmissionTest(unittest.TestCase):
    """The public CLI separates stable input from all legacy preview modes."""

    def test_stable_missing_transport_is_an_admission_failure(self):
        result = subprocess.run([
            __import__("sys").executable, str(SCRIPT), "--channel", "stable",
            "--base-url", "https://packages.githubim.com", "--expected-version", "3.0.0",
            "--apt-public-cert", "/missing/apt", "--rpm-public-cert", "/missing/rpm",
        ], capture_output=True, text=True)
        self.assertEqual(1, result.returncode)
        self.assertEqual("", result.stdout)
        self.assertIn("stable requires transport inputs", result.stderr)
        self.assertNotIn("Traceback", result.stderr)

    def test_stable_rejects_wrong_scope_before_native_or_network(self):
        bad = [
            ([], "stable requires installed CLI acceptance"),
            (["--expected-version", "3.0.0-rc.1"], "stable target must be strict release SemVer"),
            (["--base-url", "https://unreviewed.example"], "stable clients require the canonical HTTPS origin"),
            (["--snapshot", "old.json"], "stable derives its snapshot from transport inputs"),
            (["--test-product-sha", "1" * 40], "TEST ONLY product identity requires explicit test mode"),
            (["--test-only"], "TEST ONLY clients require the fixed fixture product SHA"),
        ]
        for override, reason in bad:
            with self.subTest(reason=reason):
                result = subprocess.run([
                    __import__("sys").executable, str(SCRIPT), "--channel", "stable",
                    "--transport-inputs", "/missing/transport.json",
                    "--base-url", "https://packages.githubim.com", "--expected-version", "3.0.0",
                    "--apt-public-cert", "/missing/apt", "--rpm-public-cert", "/missing/rpm",
                    *override,
                ], capture_output=True, text=True)
                self.assertEqual(1, result.returncode)
                self.assertIn(reason, result.stderr)
                self.assertEqual("", result.stdout)
                self.assertNotIn("Traceback", result.stderr)

    def test_stable_download_command_preserves_checks_and_has_fixed_deadline(self):
        with TemporaryDirectory() as tmp:
            root = Path(tmp)
            certificate = root / 'public.asc'; certificate.write_text('public')
            bootstrap = root / 'bootstrap.pkg'; bootstrap.write_text('bootstrap')
            for family in ('apt', 'rpm'):
                command = validator.stable_client_command('image@sha256:' + '1' * 64,
                    root, certificate, family, bootstrap)
                self.assertEqual(command[-7:-1], ['timeout', '--signal=TERM', '--kill-after=10s', '300s', 'bash', '-c'])
                self.assertNotIn('preview', command[-1])
                if family == 'rpm':
                    self.assertIn('mirrorlist=https://packages.githubim.com/rpm/stable/el/9/x86_64/mirrorlist', command[-1])
                    self.assertNotIn('baseurl=', command[-1])
                    for check in ('sslverify=1', 'gpgcheck=1', 'repo_gpgcheck=1'):
                        self.assertIn(check, command[-1])

    def test_preview_cannot_accept_test_identity_or_transport(self):
        for extra in (["--test-only"], ["--transport-inputs", "ignored.json"],
                      ["--test-product-sha", "1" * 40]):
            with self.subTest(extra=extra):
                result = subprocess.run([
                    __import__("sys").executable, str(SCRIPT),
                    "--base-url", "https://packages.githubim.com",
                    "--apt-public-cert", "/missing/apt", "--rpm-public-cert", "/missing/rpm",
                    *extra,
                ], capture_output=True, text=True)
                self.assertEqual(1, result.returncode)
                self.assertIn("stable-only options require explicit stable mode", result.stderr)
                self.assertEqual("", result.stdout)


if __name__ == "__main__":
    unittest.main()
