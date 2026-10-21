"""CLI protocol tests; the external GPG fixture does not prove cryptography."""

import copy
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import unittest


ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts/local-signing-exchange.py"
SCHEMA = "wukongim/local-signing-descriptor/v1"


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":")).encode() + b"\n"


def facts(path, relative):
    data = path.read_bytes()
    return {"path": relative, "sha256": hashlib.sha256(data).hexdigest(), "size": len(data)}


def key_record(kind, fingerprint, created, expires, capabilities, bits=3072, algorithm=1):
    fields = [kind, "u", str(bits), str(algorithm), fingerprint[-16:], str(created),
              str(expires) if expires else "", "", "", "", "", capabilities, "", "", ""]
    return ":".join(fields) + ":\n" + "fpr:::::::::" + fingerprint + ":\n"


class Fixture:
    """A synthetic TEST ONLY descriptor and a recording public-tool boundary."""

    def __init__(self, base):
        self.base = base
        self.inputs = base / "inputs"
        self.inputs.mkdir()
        self.descriptor_path = base / "descriptor.json"
        self.output = base / "request.json"
        self.log = base / "gpg-log.jsonl"
        self.gpg = base / "fake-gpg"
        self.gpg.write_text("#!" + sys.executable + "\n" + '''
import json, os, pathlib, sys, time
args = sys.argv[1:]
cert = json.loads(pathlib.Path(args[-1]).read_text())
with open(LOG, "a") as log:
    log.write(json.dumps({"args": args, "env": dict(os.environ)}) + "\\n")
if cert.get("fail"):
    print("PRIVATE DIAGNOSTIC MUST NOT LEAK", file=sys.stderr)
    raise SystemExit(9)
if cert.get("flood"):
    sys.stdout.write("X" * (2 * 1024 * 1024))
    raise SystemExit(0)
if "--list-packets" in args:
    print(cert.get("packets", ":public key packet:\\n:public sub key packet:"))
elif "--show-keys" in args:
    sys.stdout.write(cert["records"])
else:
    raise SystemExit("unsupported native command")
'''.replace("LOG", repr(str(self.log))))
        self.gpg.chmod(0o700)
        self.cert_data = {}
        self.descriptor = {
            "schema": SCHEMA, "mode": "test_only", "session_id": "1" * 32,
            "context": {
                "source": {"repository": "WuKongIM/WuKongIM", "release_id": 17,
                           "tag": "v3.0.0", "version": "3.0.0", "source_sha": "2" * 40},
                "control_sha": "3" * 40,
                "toolchain": {"image": "ghcr.io/wukongim/native-package-signing-toolchain",
                              "digest": "sha256:" + "4" * 64, "workflow_sha": "5" * 40},
                "audit": {"kind": "test_only", "id": "test-only-17", "base_id": None},
            },
            "certificates": {}, "inventory": [],
        }
        now = int(time.time())
        for index, family in enumerate(("apt", "rpm")):
            fingerprints = [format(index * 3 + n, "040X") for n in (1, 2, 3)]
            primary, current, successor = fingerprints
            records = key_record("pub", primary, now - 86400, None, "cSC")
            records += key_record("sub", current, now - 86400, now + 60 * 86400, "s")
            records += key_record("sub", successor, now + 30 * 86400, now + 120 * 86400, "s")
            self.cert_data[family] = {"records": records}
            self.descriptor["certificates"][family] = {
                "primary_fingerprint": primary,
                "current_signing_subkey_fingerprint": current,
                "next_signing_subkey_fingerprint": successor,
                "historical_signing_subkey_fingerprints": [],
            }
            self.refresh_certificate(family)
        for role, relative in [
            ("rpm_payload", "payloads/wukongim.rpm"),
            ("deb_payload", "payloads/wukongim.deb"),
            ("source_resolution", "evidence/resolution.json"),
            ("source_attestation", "evidence/attestation.json"),
            ("source_metadata", "evidence/metadata.json"),
            ("control", "control/channels.json"),
            ("toolchain_manifest", "control/toolchain.json"),
        ] + [("attestation_evidence", f"evidence/asset-{n}.json") for n in range(8)]:
            path = self.inputs / relative
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(b"SYNTHETIC TEST ONLY " + relative.encode() + b"\n")
            self.descriptor["inventory"].append({"role": role, **facts(path, relative)})
        self.descriptor["inventory"].sort(key=lambda item: item["path"])
        self.save()

    def refresh_certificate(self, family):
        relative = f"keys/{family}.asc"
        path = self.inputs / relative
        path.parent.mkdir(exist_ok=True)
        path.write_bytes(canonical(self.cert_data[family]))
        self.descriptor["certificates"][family].update(facts(path, relative))

    def save(self, raw=None):
        self.descriptor_path.write_bytes(raw if raw is not None else canonical(self.descriptor))
        return hashlib.sha256(self.descriptor_path.read_bytes()).hexdigest()

    def run(self, *, expected=None, output=None, command="prepare-rpm", extra=()):
        env = {**os.environ, "STABLE_SECRET_SENTINEL": "MUST_NOT_INHERIT", "GPG_TTY": "BAD"}
        return subprocess.run(
            [sys.executable, "-B", str(SCRIPT), command,
             "--descriptor", str(self.descriptor_path),
             "--expected-descriptor-sha256", expected or self.save(),
             "--input-root", str(self.inputs), "--request-id", "6" * 32,
             "--gpg", str(self.gpg), "--output", str(output or self.output), *extra],
            env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=10,
        )


class LocalSigningExchangeTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="local-signing-exchange-test-")
        self.fixture = Fixture(Path(self.temp.name))

    def tearDown(self):
        self.temp.cleanup()

    def assert_rejected(self, result, output=None):
        self.assertEqual(result.returncode, 1, result.stderr.decode())
        self.assertFalse((output or self.fixture.output).exists())
        self.assertNotIn(b"PRIVATE DIAGNOSTIC", result.stdout + result.stderr)

    def test_prepares_deterministic_test_only_request_without_trust_claims(self):
        first = self.fixture.run()
        self.assertEqual(first.returncode, 0, first.stderr.decode())
        request_bytes = self.fixture.output.read_bytes()
        request = json.loads(request_bytes)
        receipt = json.loads(first.stdout)
        self.assertEqual(request["schema"], "wukongim/local-signing-request/v1")
        self.assertEqual((request["mode"], request["family"], request["phase"]),
                         ("test_only", "rpm", "rpm_payloads"))
        self.assertIsNone(request["parent_verified_receipt_sha256"])
        self.assertEqual(request["descriptor_sha256"], self.fixture.save())
        self.assertEqual(len(request["inputs"]), 17)
        self.assertEqual(request["expected_outputs"],
                         [{"path": "signed/wukongim.rpm", "role": "signed_rpm"}])
        self.assertTrue(receipt["plan_only"])
        self.assertTrue(receipt["checks"]["public_key_policy"])
        for check in ("source_provenance", "github", "control_commit", "rpm_unsigned",
                      "signatures", "cloud", "publication"):
            self.assertIs(receipt["checks"][check], False)
        self.assertEqual(receipt["request_sha256"], hashlib.sha256(request_bytes).hexdigest())
        second = self.fixture.run(output=Path(self.temp.name) / "second.json")
        self.assertEqual(second.returncode, 0, second.stderr.decode())
        self.assertEqual((Path(self.temp.name) / "second.json").read_bytes(), request_bytes)
        calls = [json.loads(line) for line in self.fixture.log.read_text().splitlines()]
        self.assertEqual(len(calls), 8)
        for call in calls:
            self.assertNotIn("STABLE_SECRET_SENTINEL", call["env"])
            self.assertNotIn("GPG_TTY", call["env"])
            self.assertNotEqual(call["env"]["HOME"], os.environ.get("HOME"))
            self.assertEqual(call["env"]["HOME"], call["env"]["GNUPGHOME"])
            self.assertTrue("--list-packets" in call["args"] or "--show-keys" in call["args"])
            self.assertFalse(any(arg in call["args"] for arg in
                                 ("--import", "--sign", "--list-secret-keys", "--decrypt")))
            self.assertFalse(call["args"][-1].startswith(str(self.fixture.inputs)))

    def test_external_digest_rejects_coherent_descriptor_rewrite_before_gpg(self):
        expected = self.fixture.save()
        self.fixture.descriptor["context"]["source"]["source_sha"] = "a" * 40
        self.fixture.save()
        self.assert_rejected(self.fixture.run(expected=expected))
        self.assertFalse(self.fixture.log.exists())

    def test_rejects_noncanonical_duplicate_and_unknown_fields(self):
        original = copy.deepcopy(self.fixture.descriptor)
        for raw in (canonical(original).replace(b'"schema":', b'"schema":"x","schema":'),
                    json.dumps(original, indent=2).encode(),
                    canonical({**original, "sign": True})):
            with self.subTest(raw=raw[:30]):
                expected = self.fixture.save(raw)
                self.assert_rejected(self.fixture.run(expected=expected))

    def test_rejects_invalid_context_mode_and_integer_types(self):
        original = copy.deepcopy(self.fixture.descriptor)
        mutations = [
            lambda d: d.update(mode="stable"),
            lambda d: d.update(session_id="bad"),
            lambda d: d["context"]["source"].update(release_id=True),
            lambda d: d["context"]["source"].update(tag="v3.0.1"),
            lambda d: d["context"]["source"].update(repository="other/repo"),
            lambda d: d["context"]["source"].update(version="3.0.0-beta.1"),
            lambda d: d["context"].update(control_sha="bad"),
            lambda d: d["context"]["toolchain"].update(digest="latest"),
            lambda d: d["context"]["audit"].update(kind="github", id=42),
            lambda d: d["context"]["audit"].update(base_id="old"),
            lambda d: d["inventory"][0].update(size=True),
            lambda d: d["inventory"][0].update(command="echo bad"),
        ]
        for mutate in mutations:
            with self.subTest(mutate=mutate):
                self.fixture.descriptor = copy.deepcopy(original)
                mutate(self.fixture.descriptor)
                self.assert_rejected(self.fixture.run())
        self.assertFalse(self.fixture.log.exists())

    def test_rejects_inventory_closure_duplicates_roles_and_order(self):
        original = copy.deepcopy(self.fixture.descriptor)
        mutations = [
            lambda d: d["inventory"].pop(),
            lambda d: d["inventory"].append(d["inventory"][0]),
            lambda d: d["inventory"].reverse(),
            lambda d: d["inventory"][0].update(role="response"),
            lambda d: d["inventory"][0].update(path="keys/apt.asc"),
        ]
        for mutate in mutations:
            with self.subTest(mutate=mutate):
                self.fixture.descriptor = copy.deepcopy(original)
                mutate(self.fixture.descriptor)
                self.assert_rejected(self.fixture.run())
        self.fixture.descriptor = original
        (self.fixture.inputs / "extra").write_text("unexpected")
        self.assert_rejected(self.fixture.run())
        self.assertFalse(self.fixture.log.exists())

    def test_rejects_missing_wrong_digest_or_actual_size_before_gpg(self):
        entry = self.fixture.descriptor["inventory"][0]
        path = self.fixture.inputs / entry["path"]
        raw = path.read_bytes()
        for replacement in (None, b"X" * len(raw), raw + b"X"):
            with self.subTest(replacement=replacement):
                if replacement is None:
                    path.unlink()
                else:
                    path.write_bytes(replacement)
                self.assert_rejected(self.fixture.run())
        self.assertFalse(self.fixture.log.exists())

    def test_rejects_unsafe_paths_without_native_access(self):
        entry = self.fixture.descriptor["inventory"][0]
        for path in ("/tmp/escape", "../escape", "control/../escape", "control\\escape", "./escape"):
            with self.subTest(path=path):
                entry["path"] = path
                self.assert_rejected(self.fixture.run())
        self.assertFalse(self.fixture.log.exists())

    def test_rejects_leaf_and_parent_symlinks_and_hardlinks(self):
        entry = self.fixture.descriptor["inventory"][0]
        path = self.fixture.inputs / entry["path"]
        original = path.read_bytes()
        outside = Path(self.temp.name) / "outside"
        outside.write_bytes(original)
        path.unlink()
        path.symlink_to(outside)
        self.assert_rejected(self.fixture.run())
        path.unlink()
        os.link(outside, path)
        self.assert_rejected(self.fixture.run())
        path.unlink()
        path.write_bytes(original)
        parent = path.parent
        moved = parent.with_name("moved")
        parent.rename(moved)
        parent.symlink_to(moved, target_is_directory=True)
        self.assert_rejected(self.fixture.run())
        self.assertFalse(self.fixture.log.exists())

    def test_rejects_output_conflicts_and_preserves_existing_bytes(self):
        self.fixture.output.write_text("preserve me")
        result = self.fixture.run()
        self.assertEqual(result.returncode, 1, result.stderr.decode())
        self.assertEqual(self.fixture.output.read_text(), "preserve me")
        self.fixture.output.unlink()
        self.assert_rejected(self.fixture.run(output=self.fixture.inputs / "request.json"),
                             self.fixture.inputs / "request.json")
        result = self.fixture.run(output=self.fixture.descriptor_path)
        self.assertEqual(result.returncode, 1, result.stderr.decode())
        self.assertFalse(self.fixture.log.exists())

    def test_rejects_secret_packets_and_secret_colon_records(self):
        original = copy.deepcopy(self.fixture.cert_data["apt"])
        for data in ({**original, "packets": ":secret key packet:"},
                     {**original, "records": original["records"].replace("pub:", "sec:", 1)}):
            with self.subTest(data=data):
                self.fixture.cert_data["apt"] = data
                self.fixture.refresh_certificate("apt")
                self.assert_rejected(self.fixture.run())

    def test_rejects_rpm_algorithm_and_bad_topology(self):
        original = copy.deepcopy(self.fixture.cert_data["rpm"])
        records = original["records"]
        for changed in (records.replace(":3072:1:", ":2048:1:"),
                        records.replace(":3072:1:", ":255:22:"),
                        records.replace(":cSC:", ":csSC:"),
                        records.replace(":s:", ":se:")):
            with self.subTest(changed=changed[:60]):
                self.fixture.cert_data["rpm"]["records"] = changed
                self.fixture.refresh_certificate("rpm")
                self.assert_rejected(self.fixture.run())

    def test_rejects_short_id_collision_across_families_and_preview_reuse(self):
        cert = self.fixture.descriptor["certificates"]["rpm"]
        original = copy.deepcopy(cert)
        cert["primary_fingerprint"] = "F" * 32 + self.fixture.descriptor["certificates"]["apt"]["primary_fingerprint"][-8:]
        self.assert_rejected(self.fixture.run())
        self.fixture.descriptor["certificates"]["rpm"] = original
        preview = json.loads((ROOT / "manifests/preview-signing.json").read_text())
        cert = self.fixture.descriptor["certificates"]["rpm"]
        cert["primary_fingerprint"] = preview["rpm"]["primary_fingerprint"]
        self.assert_rejected(self.fixture.run())
        self.assertFalse(self.fixture.log.exists())

    def test_rejects_malformed_certificate_controls_before_gpg(self):
        original = copy.deepcopy(self.fixture.descriptor["certificates"])
        changes = [
            {"primary_fingerprint": None},
            {"next_signing_subkey_fingerprint": None},
            {"historical_signing_subkey_fingerprints": "not-an-array"},
            {"historical_signing_subkey_fingerprints": [None]},
            {"size": False},
            {"sha256": "BAD"},
            {"secret_subkey_env": "DO_NOT_READ"},
        ]
        for change in changes:
            with self.subTest(change=change):
                self.fixture.descriptor["certificates"] = copy.deepcopy(original)
                self.fixture.descriptor["certificates"]["apt"].update(change)
                result = self.fixture.run()
                self.assert_rejected(result)
                self.assertNotIn(b"Traceback", result.stderr)
        self.assertFalse(self.fixture.log.exists())

    def test_rejects_expired_current_and_insufficient_successor_runway(self):
        original = self.fixture.cert_data["apt"]["records"]
        rows = original.splitlines()
        for row_number, new_expiry in ((2, int(time.time()) - 1),
                                      (4, int(time.time()) + 61 * 86400)):
            with self.subTest(row_number=row_number):
                changed = list(rows)
                fields = changed[row_number].split(":")
                fields[6] = str(new_expiry)
                changed[row_number] = ":".join(fields)
                self.fixture.cert_data["apt"]["records"] = "\n".join(changed) + "\n"
                self.fixture.refresh_certificate("apt")
                self.assert_rejected(self.fixture.run())

    def test_rejects_real_oversize_files_before_native_commands(self):
        certificate = self.fixture.descriptor["certificates"]["apt"]
        path = self.fixture.inputs / certificate["path"]
        with path.open("wb") as stream:
            stream.truncate(1024 * 1024 + 1)
        certificate["size"] = 1024 * 1024 + 1
        self.assert_rejected(self.fixture.run())
        self.fixture.refresh_certificate("apt")
        entry = self.fixture.descriptor["inventory"][0]
        path = self.fixture.inputs / entry["path"]
        with path.open("wb") as stream:
            stream.truncate(8 * 1024 * 1024 + 1)
        entry["size"] = 8 * 1024 * 1024 + 1
        self.assert_rejected(self.fixture.run())
        self.assertFalse(self.fixture.log.exists())

    def test_rejects_declared_total_limit_and_reused_request_id(self):
        original = copy.deepcopy(self.fixture.descriptor)
        for entry in self.fixture.descriptor["inventory"]:
            if entry["role"] in {"rpm_payload", "deb_payload"}:
                entry["size"] = 800 * 1024 * 1024
        self.assert_rejected(self.fixture.run())
        self.fixture.descriptor = original
        self.assert_rejected(self.fixture.run(extra=("--request-id", "1" * 32)))
        self.assertFalse(self.fixture.log.exists())

    def test_native_failure_and_output_limit_leave_no_result(self):
        for switch in ("fail", "flood"):
            with self.subTest(switch=switch):
                self.fixture.cert_data["apt"][switch] = True
                self.fixture.refresh_certificate("apt")
                self.assert_rejected(self.fixture.run())
                del self.fixture.cert_data["apt"][switch]

    def test_no_signing_or_response_acceptance_commands_exist(self):
        for command in ("sign", "accept-response", "prepare-metadata", "generate-key", "publish"):
            with self.subTest(command=command):
                result = self.fixture.run(command=command)
                self.assertEqual(result.returncode, 2)
                self.assertFalse(self.fixture.output.exists())
        self.assertFalse(self.fixture.log.exists())


if __name__ == "__main__":
    unittest.main()
