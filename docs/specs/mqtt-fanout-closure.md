# MQTT fanout closure diagnosis

The sealing-contention candidate at `fd9ba0012` passes two sequential ordinary
2,000-member workloads and one original 100,000-member acceptance, each with
500 persistent clients, twenty initial publications, a fresh post-churn
publication and all 600 retirements. The bounded control probe localizes one
unsubscribe closure to source sealing CAS; exact progress/closed-drain regression
cases precede the qualified pending repair. No broader historical owner-loss or
initial receipt deadline cause is inferred from these passes.
[Repair, acceptance and retained failures](../reports/mqtt-unsubscribe-sealing/README.md).

The preceding compound-read candidate at `838222fcf` passes once at original
size but still has a smaller unsubscribe/conflict closure. Its earlier comparison
remains [historical evidence](../reports/mqtt-compound-originals/README.md).

The unprofiled full workload at `dd8e49bb9` prepares 100,000 members and confirms
500 subscriptions, then fails its unchanged three-minute initial receipt wait.
Public owner gauges show 497 live/held owners at failure. This does not yet prove
whether the other three closed from ACK, Sender, renewal or transport failure,
nor whether live clients also miss the receipt deadline.

## Failure inventory before the next probe

- Receipt timeout lacks client progress: emit bounded counts/histograms and
  closed/incomplete client counts on failure through independent Paho state.
- A closed base Paho client does not reconnect and cannot receive the future
  post-churn publication. End the wait on that conclusive workload failure, retaining the original
  three-minute deadline for clients that remain open; never pass fewer receipts.
- Late diagnostics lose the server cause: emit the harness's bounded node dump
  before client/server cleanup. Save only fixed diagnostic error stages in evidence.
- Temporary probes alter behavior: wrappers and error-only terminal logs must
  preserve contexts, authority, packet limits, concurrency and retries. Compile
  them through an external Go overlay, not product source.
- Smaller preparation masks the trigger: first use the same 500 clients/twenty
  messages with 2,000 members, recording actual dimensions. It cannot replace
  100,000-member acceptance. Preserve the original failed full artifact.

Ranked hypotheses and predictions: ACK CAS retry exhaustion produces an ACK
terminal conflict stage; Sender authority/clock failures produce a closed Sender
stage; renewal exhaustion produces a missing/expired installed lease; transport
or keepalive loss closes Paho without one of those earlier server stages.

## Subsequent evidence

The original-size boundary overlay reaches the same three-minute receipt timeout
with all 500 clients still open and 13–19 observed receipts each. No ACK, Sender,
renewal, gateway or control terminal stage is captured. This proves the deadline
can fail without connection closure; it does not identify the earlier three lost
owners. [Full boundary provenance](../reports/mqtt-full-dd8e-boundaries-provenance.json).

A separate bounded CPU/goroutine/metric profile finds fourteen of sixteen
delivery workers waiting on fresh read barriers, including ten in Accounting,
and two on proposal futures. A scheduling experiment passes thirteen isolated
safety cases but shows no benefit in the 500-client run or sequential 64-client
pair. That experiment was not adopted; its product baseline remained at `dd8e49bb9`.
[Rejected experiment and measurement limits](mqtt-accounted-delivery-experiment.md).

The baseline 64-client boundary probe completes twenty initial publications,
one fresh publication and 192 retirements with correct identities/order, but
fails its unchanged idle criterion (10.80 barriers/client/s). It does not
reproduce unsubscribe/conflict or qualify full acceptance. The conflict and
earlier three-owner closure origin remain unconfirmed.

Two subsequent corrected stage probes keep 500 clients/twenty publications
with 2,000 members. Actual Slot read-barrier waits account for about 84% of
delivery execution; window preparation accounts for 56–57%, Accounting 29%,
and empty exchange recovery 3–4%. One probe repeats a receipt timeout with
all clients open; another reaches all initial receipts in 141.029s and fails
on round 2 unsubscribe, with two public conflict closures. These measurements
do not localize those conflicts or establish a repair. The preceding original-size
probe fails initial SUBSCRIBE before fanout and cannot supply throughput data.
[Stage evidence and next regression seam](mqtt-delivery-read-budget.md).
