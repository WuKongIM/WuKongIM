# TEST ONLY negative-fault proof repair, generation 3

The independent parent review found a P2 in generation 2: request entries were written before send_response/body, and the negative branch accepted generic SSL errors. A BrokenPipe before response headers therefore still produced a planned fault receipt and passed. The original review evidence remains at ../client-compatibility-review/fault-delivery-probe.{py,json}; generation 1 and 2 evidence are unchanged.

## Red before repair

All regression edits preceded this implementation change. The actual serve()/nested Handler is invoked through a controlled server boundary, injecting BrokenPipe at send_response, end_headers, body write and flush, for both fault modes, plus successful controls. These ten handler subcases were red because the original receipt had no post-write completion flag. Tests also specify missing/false completion flags and a completed-write receipt paired with SSL EOF, generic fetch/resolve/signature/checksum diagnostics, and corrupt-mode 404; the new diagnostic interface initially produced TypeError (01). This interface failure is not claimed as an actual native reproduction.

The independent original Handler/branch reproduction is rerun unchanged except for the source path and rejection assertion (03): both failures before headers now reject. An additional actual-branch control probe executed against the frozen generation-2 source reproduces acceptance of post-write generic SSL/fetch failures (05). The same probe is green on the repaired code (06). These are bounded control-flow probes, not Linux client execution.

## Repair

A request receipt starts with response_write_completed=false and is appended in finally after the send/write/flush attempt. Only completion of both wfile.write and wfile.flush sets true. The comment and schema field deliberately describe server write completion only; they do not prove that the socket peer received or acted on every byte. Failed writes remain visible with false. The fault verifier requires true, the actual missing/corrupt path/status/body digest, and a matching APT diagnostic.

Missing-byhash accepts an explicit 404 Not Found or index hash/size mismatch (APT may try the mutable B fallback against the A release). Corrupt-byhash requires the hash/size mismatch; a bare 404 is insufficient. Supported APT integrity diagnostics are Hash Sum mismatch, Size mismatch and File has unexpected size. Generic SSL/fetch/resolve/signature/checksum text is insufficient. Existing native nonzero/timeout/tool/signal/harness checks remain, and 06 verifies success/timeout/tool/signal exits remain rejected even with completed-write fault receipts.

The English integrity strings were checked against primary APT source:
- https://raw.githubusercontent.com/Debian/apt/main/apt-pkg/acquire-item.cc (FailMessage and RenameOnError)
- https://raw.githubusercontent.com/Debian/apt/main/methods/basehttp.cc (MaximumSizeExceeded response)

## Validation and boundary

57 directly related tests pass (04), including real loopback TLS and both SHA algorithms. The original independent reviewer probe rejects both prior false positives (03). The actual negative branch controls all match expected verdicts (06). git diff --check passes. Only the same two owned test files were edited; workflow bytes and 49 native case count are unchanged. No commit, push, CI rerun, Docker, native package build, credential or producer change. Native Linux 49-case acceptance remains pending.
