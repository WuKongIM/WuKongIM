#!/usr/bin/env python3
"""Read one immutable offline ZIP; report missing bounded correlation evidence."""
import hashlib
import json
import stat
import sys
import zipfile
from pathlib import Path, PurePosixPath

EXPECTED_SHA = "a74b828be1a383d34bb8779bba4ed7473e51461ef6826c2d6d1a22afef415ccf"


def main():
    path = Path(sys.argv[1])
    assert not path.is_symlink()
    body = path.read_bytes()
    assert len(body) <= 2 << 20
    assert hashlib.sha256(body).hexdigest() == EXPECTED_SHA
    with zipfile.ZipFile(path) as archive:
        members = archive.infolist()
        assert len(members) <= 64
        assert len({m.filename for m in members}) == len(members)
        assert sum(m.file_size for m in members) <= 4 << 20
        for member in members:
            relative = PurePosixPath(member.filename)
            assert not relative.is_absolute() and ".." not in relative.parts
            assert "\\" not in member.filename and not stat.S_ISLNK(member.external_attr >> 16)
        assert archive.testzip() is None
        report = json.loads(archive.read("final.json"))
        assert report["correlation"]["expired"] == report["messages"]["losses"] == 1
        matches = [m.filename for m in members if PurePosixPath(m.filename).name == "worker-evidence.json"]
        result = {
            "scope": "offline_archive_evidence_sufficiency_only",
            "source_zip_sha256": EXPECTED_SHA,
            "members": len(members),
            "crc_all_valid": True,
            "expired_correlations": 1,
            "worker_evidence_member_count": len(matches),
            "result": "RED_MISSING_BOUNDED_SAMPLE_EVIDENCE" if not matches else "REQUIRES_DOCUMENT_VALIDATION",
            "historical_product_cause_proven": False,
            "runtime_write_failure_proven": False,
        }
        print(json.dumps(result, indent=2))
        return 1 if not matches else 2


if __name__ == "__main__":
    raise SystemExit(main())
