# Implementation addendum

- `zero_inventory.observed_at` must be no earlier than Lease creation and the corresponding Release operation/job start. It need not follow workflow completion; the real zero at 10:19:19 predates run completion.
- Sidecar/evidence failures block new procurement and qualification. They cannot bypass or prevent cleanup/Release of a Lease already owned by the stage/finalizer.
- Adjacent numeric workflow sequence is generic `current.run_number == predecessor.run_number + 1`, both attempts 1; 32→33 is this request's evidence, not a hardcoded code path.
- One stdlib Python authentication helper is preferred over a large embedded Python block in shell: proposed filename `scripts/chat-lifecycle/authenticate-released-preclock.py`. No additional helper/framework. Shell/workflow invoke python3 explicitly. The scope and CLI seam remain identical to the approved single helper.
