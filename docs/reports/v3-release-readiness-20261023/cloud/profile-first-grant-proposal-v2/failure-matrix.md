# Proposal v2 failure matrix — before implementation

No product/build/load/profile-to-product execution is authorized here. Fake loopback processes only. Keep original proposal-v1 and runtime freezes unchanged.

- Wrong source/build receipt/hash/clean source/binary embedded revision or native platform: zero product launch, explicit preflight refusal. Source cleanclone and two exact built executables are required; no prebuilt-v1 copy and no go build during the run.
- Receipt/run/assignment/generation/stale/time/deadline/owner mismatch: retain original watcher skip behavior; no profile request or retry.
- Fresh first-grant receipt: exactly CPU seconds10 then nominal5s node2 goroutine debug0. GOR filename .pb.gz, CPU .pb.gz; no heap/context/other request. Fixed4MiB GOR/5s deadline and8MiB CPU/15s deadline stay.
- Non200 (including3xx with curl0), timeout28, headerless overflow63, connection/othercurl error, empty body or post-read byte excess: no artifact promotion, typed partial; preserve bounded per-kind curl exit/HTTP status/fixed error enum and byte count without URL/header/token/raw stderr. Missing/unreadable code stays explicit rather than inventing HTTP success.
- TERM/HUP/INT or coordinator/stop/hardbound while request/timer active: cancel, join, delete partial bodies, preserve bounded known error evidence; no delayed request after cancellation. Keep registration guards, same PGID and independent570TERM+5KILL/600s cleanup.
- Output/claim/preexisting artifact errors must not overwrite old evidence or alter original coordinator exit. Profiling valid flag onlyqualifies collection; SEND active/drain and realgor offset remain unproven until independently observed.
- Fake results cannot prove actual protobuf semantics, real product cause, Linux capacity or every registration micro-race. Original GOR missing remains unknown; the format change is not proof that4MiB caused it.

Test first: one fake contract with normal/protobuf URL+suffix, non200, timeout and headerless overflow subcases; assert bounded status and all ownedprocess/activeHTTP zero and no token in artifacts. Also one cancellationcase covering the new status-on-cancel branch. Static exactdiff/AST/Bash syntax andreceipt/input proofs before any root run decision.
