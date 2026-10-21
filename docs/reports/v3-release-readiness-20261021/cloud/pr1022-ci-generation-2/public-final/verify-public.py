from pathlib import Path
import argparse,json,gzip,hashlib
p=argparse.ArgumentParser();p.add_argument("--restore-to",type=Path);a=p.parse_args();root=Path(__file__).resolve().parent
if a.restore_to:
 a.restore_to=a.restore_to.resolve();a.restore_to.mkdir(exist_ok=False)
rows=json.loads((root/"ARCHIVE_ALLOWLIST.json").read_text())["files"]
for row in rows:
 path=(root/row["path"]).resolve();assert path.is_relative_to(root)
 data=path.read_bytes();assert len(data)==row["archive_bytes"] and hashlib.sha256(data).hexdigest()==row["archive_sha256"]
 raw=gzip.decompress(data) if row["encoding"]=="gzip" else data
 assert len(raw)==row["source_bytes"] and hashlib.sha256(raw).hexdigest()==row["source_sha256"]
 if a.restore_to:
  dest=(a.restore_to/row["original_path"]).resolve();assert dest.is_relative_to(a.restore_to);dest.parent.mkdir(parents=True,exist_ok=True);dest.write_bytes(raw)
print("Verified",len(rows),"public evidence files")
