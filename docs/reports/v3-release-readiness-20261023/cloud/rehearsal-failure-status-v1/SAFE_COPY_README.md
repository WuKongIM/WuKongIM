# Finite public copy of a failed rehearsal

This directory is a selective public evidence copy, not the complete terminal
bundle. The original rehearsal failed after 61.223094218 seconds with one
expired correlated sample; sending had stopped. It did not pass two hours or
72 hours. These historical files do not establish current cloud resource state.

Three original service application logs contain raw recipient UIDs and are not
copied. Six binary pprof originals are also omitted because identity-string
safety was not established. The original local files remain available for
subsequent local diagnosis; SHA-256 and byte counts are retained in
`excluded-original-references.json`. Existing verified offline top-15 summaries
and their receipts are separately copied under `../rc-loss-diagnosis/signals-axis`.

`SOURCE_PUBLIC_ALLOWLIST.txt`, `SOURCE_SHA256SUMS` and
`SOURCE_archive-summary.json` are unchanged source authority references. They
intentionally list excluded originals and must not be interpreted as a complete
manifest of this selected copy. The source README remains historical source
material. The enclosing `COPY_BATCH3_SHA256SUMS` is the actual copied-file
manifest for this archive batch.

`extract-safe-delivery-warnings.py` verifies exactly the three frozen original
log hashes and sizes, selects the 37 plan-incomplete warnings, and produces
`safe-delivery-warnings.json`. It retains only timestamps, embedded host service
node, fixed error category, timeout/phase/mode, recipient count, source line and
first-occurrence recipient sample ordinal. Ordinals are anonymous grouping
labels, not workload user indexes. No raw UID, Channel ID, ClientMsgNo, payload
or arbitrary error text is emitted. It preserves the distinction between source
archive filename and embedded host node. This extraction is a parser check and
does not reproduce the product failure or establish a causal message join.

Repeat with the original local source (which is intentionally not public):

```sh
python3 extract-safe-delivery-warnings.py --original-source-root /path/to/original/public-v1 --output /tmp/safe-delivery-warnings.json
```
