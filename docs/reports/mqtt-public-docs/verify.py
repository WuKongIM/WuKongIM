#!/usr/bin/env python3
"""Verify paired live receipts and their exact example/source identities."""
import hashlib
import gzip
import json
from pathlib import Path
import re
import subprocess

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[2]
context = json.loads((HERE / "source-context.json").read_text())
for name, digest in context["instruction_sha256"].items():
    source = subprocess.check_output(["git", "show", f'{context["source_revision"]}:{name}'], cwd=ROOT)
    assert hashlib.sha256(source).hexdigest() == digest, name

scenario = ROOT / "test/e2e/mqtt/docs_quickstart/AGENTS.md"
assert hashlib.sha256(scenario.read_bytes()).hexdigest() == context["scenario_instruction_sha256"]

for nodes in (1, 3):
    report = json.loads((HERE / f"mqtt-docs-quickstart-{nodes}.json").read_text())
    assert report["scenario"] == "mqtt-js-docs-quickstart"
    assert report["nodes"] == nodes and report["hash_slots"] == 256
    assert report["passed"] is True
    assert report["invalid_token_checked"] is (nodes == 1)
    if nodes == 1:
        assert report["invalid_token_rejected"] is True
    client = report["client"]
    assert client["passed"] is True and client["client_version"] == "5.16.0"
    exchanges = client["exchanges"]
    assert len(exchanges) == 2
    assert [item["from_uid"] for item in exchanges] == ["alice", "bob"]
    for item in exchanges:
        for key in ("message_id", "message_seq"):
            assert isinstance(item[key], str) and re.fullmatch(r"[1-9][0-9]*", item[key])
    assert exchanges[0]["message_id"] != exchanges[1]["message_id"]
    assert set(report["example_sha256"]) == {"quickstart.mjs", "package-lock.json"}
    for name, digest in report["example_sha256"].items():
        source = ROOT / "docs-site/examples/mqtt-quickstart" / name
        assert hashlib.sha256(source.read_bytes()).hexdigest() == digest, name

binary = context["product_binary"]
assert binary["race"] is False
candidate = Path(binary["path"])
if candidate.exists():
    assert hashlib.sha256(candidate.read_bytes()).hexdigest() == binary["sha256"]
validation = json.loads((HERE / "validation-results.json").read_text())
assert validation["docs_verify"] == validation["flow_doc_contracts"] == "PASS"
assert validation["bilingual_mqtt_pages"] == 16
for name, digest in validation["retained_log_sha256"].items():
    assert hashlib.sha256((HERE / name).read_bytes()).hexdigest() == digest, name
site_log = gzip.decompress((HERE / "docs-verify-final.log.gz").read_bytes()).decode()
assert f'{validation["content_contracts"]} pass' in site_log and '0 fail' in site_log
assert 'static output contract passed' in site_log
live_log = gzip.decompress((HERE / "mqtt-js-final.log.gz").read_bytes()).decode()
assert '--- PASS: TestMQTTJSQuickstart' in live_log and '\nPASS\n' in live_log
print("PASS: live MQTT.js receipts, retained gates, example hashes and frozen instructions")
