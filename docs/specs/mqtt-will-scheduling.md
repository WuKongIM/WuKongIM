# MQTT Will scheduling

## Failure inventory before implementation

1. Accepted Will tasks never publish because the product composes no executor loop.
2. Scanning dispatches Waiting/future work, trusts malformed/out-of-order/late
   pages, or treats a lease deadline as proof of nonpublication. Discovery is
   only a hint; the existing executor rereads authority and owns every decision.
3. A busy or failed key starves another hash Slot, page cursors skip work that
   could not enter a bounded cohort, or stale hints survive leadership loss.
4. Each task retains its body, creates its own goroutine or bypasses the existing
   four-turn execution limit. Only four body-free keys may be admitted per node.
5. Repeated discovery starts overlapping calls for one key. Completion/failure
   must release only volatile admission; durable obligations stay authoritative.
6. Stop returns before callbacks finish, queued work executes after cancellation,
   restart overlaps the old cohort, or App closes message/cluster dependencies
   while a Will call still runs. Shutdown must join and retain timeout ownership.
7. Client EOF does not publish, normal DISCONNECT publishes, or Will Delay is
   ignored. Real product-process Paho tests must prove wire-visible behavior.
8. Scheduling completion is confused with safe uncertain redispatch, unavailable
   owner isolation, restore recovery or scale qualification. Those full-goal gates
   remain required and are not supplied by this worker.

## Intended boundary

One managed scanner rotates currently led hash Slots and validates existing Will
recovery pages. A separate fixed cohort calls a narrow executor port with exact
Will keys. Source reads and execution have independent bounded deadlines. The
scanner never stores bodies or changes Session/Will state. App adapts the existing
WillExecutor and owns start/stop before Gateway admission and dependency teardown.

## Discovered normal-close race

The first product run passed the single-node cluster but failed on three nodes:
`normal-close` published after a valid normal DISCONNECT. The access handler
required a fresh owner operation before mapping DISCONNECT. TCP EOF cancellation,
a fenced owner or operation pressure could therefore run abnormal termination
before the close callback consumed the already decoded normal receipt. A focused
regression invokes the public packet callback under each condition before fixing
that ordering. Disconnect records cleanup intent; it needs no new business scope.

## Implemented worker

The optional product runtime starts one Will scanner plus four direct workers
before Gateway admission. The scanner reads at most 32 pages of 16 rows per turn,
with a 200ms cadence, a two-second scan budget and 250ms per authority read. It
uses the existing strict recovery-page validator. Only due Ready/Executing keys
enter the cohort; Waiting decisions remain with the Session deadline worker.

Across queued and executing work, at most four keys are retained. An admitted
key cannot overlap itself; pressure keeps the unadmitted page position, failed
Slots yield, future boundaries reset hints, and leadership loss drops its hints.
Each execution has its own five-second deadline. App uses a ten-second executor
lease; the executor still rereads/claims the exact key and freezes hook output
before first dispatch. Existing Started work only recovers positive receipts.

Stop cancels the scanner and queued calls, then joins the cohort. Timeout retains
the same run and prevents restart overlap. Successful restart begins with fresh
scan cursors. No new tables, storage columns, RPC IDs or protocol formats are added.
The goroutine catalog gains fixed Will scanner/worker identities without client
labels. Global capacity and latency remain subject to the approved scale gate.

## Verified evidence

- Before implementation the real single-node cluster accepted Will setup but
  timed out waiting for publication after abnormal TCP closure.
- Runtime unit/race coverage verifies 256-Slot rotation, stage/deadline filtering,
  complete-page rejection, cursor preservation under pressure and leadership loss.
  Managed integration verifies four-key admission, joined cancellation, restart
  exclusion and fresh cursors, including callbacks that delay cancellation.
- The first three-node process run exposed normal-close publication. Public
  handler regression first failed for cancelled, fenced and capacity-limited
  dispatch; mapping DISCONNECT before business operation admission fixed it.
  Invalid disconnect controls still use the existing rejection path.
- Real `cmd/wukongim` process tests now pass in both single-node and three-node
  clusters with 256 hash Slots. They verify abnormal-close publication, the
  requested two-second minimum Will Delay, stable server identity and no normal
  or duplicate publication during a six-second post-delivery observation.
  This bounded absence assertion is not arbitrary crash-window duplicate proof.
- Reproduce: `WK_E2E_MQTT_REPORT_DIR=/tmp/mqtt-will-reports GOWORK=off go test -p 2 -tags=e2e ./test/e2e/mqtt/will -count=1 -timeout=4m -v`.
  Frozen source digests, test results and both topology reports are retained in
  [the evidence JSON](../reports/mqtt-will-scheduling.json).

Safe uncertain redispatch, unavailable-owner recovery after node failure, restore
reactivation, offline cleanup, complete failure scenarios and scale qualification
remain required work. This step does not complete the approved MQTT objective.
