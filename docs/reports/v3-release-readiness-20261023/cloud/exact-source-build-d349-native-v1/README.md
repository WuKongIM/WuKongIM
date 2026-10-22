# Exact d349 native diagnostic build

Independent real `.git` clone at full d349a7bdd2ca8f6bd4683fbccbf83010d3f9cd3e/tree 763f8432135e6b94df28451a43a4a635c7aba2a0. Installed Go1.25.11, CGO disabled, darwin/arm64, trimpath, explicit `-s -w -buildid=`. Two sequential `-p=1` builds with GOMAXPROCS1, no product process or tests. Both stamped exact d349 and clean; source clean before and after. All 76 embedded assets were copied from the frozen79a source only after matching retained cloud SHA and match in this clone. Prior clone and binary bytes were not changed.

79a→d349 exact20-path allowlist is retained in source-selection.json; production diff is retained separately. #1026 collector and #1027 app/metrics SystemBusy classification plus MessageID/seq warnings add observation; #1028 appends bounded finalize-stage diagnostics and preserves report preference/verdict/gates. This is not a fix for presence expiry and does not establish cloud root cause. New native binary equality to cloud Linux is not claimed.

Binaries under darwin-arm64 remain private. Finite public payload contains receipts, metadata, source/instruction hashes, build logs and source diff only.
