# Original QoS 0 at-most-once preparation

Original MQTT/Will QoS 0 must not be retried by this source delivery path. A
post-enqueue cursor commit leaves a crash window in which takeover sees the same
position as new work. This corrects the earlier all-QoS-0 completion design before
product sender composition: original QoS 0 consumes its uncharged position in a
command-70 Advance BEFORE a delivery candidate is returned. Only an Applied
receipt may yield that candidate; unchanged/ambiguous results never yield another
send. A crash after claim but before enqueue may lose this QoS 0 message, which
is consistent with at-most-once delivery. There is no rollback or resend.

Original QoS 1 (including native persistent publication), delivered at QoS 0 after
subscription downgrade, retains the existing post-enqueue completion path and
exact original charges. OASIS explicitly permits duplicates for that downgrade;
this does not excuse retries of original QoS 0.

PreparedDelivery keeps a private preclaim receipt. CompleteQoS0 remains callable
for a uniform sender flow: preclaimed candidates only verify current ownership
and known commit revision, then return unchanged without a second mutation.
The sender still serializes prepare/enqueue/completion and checks current receive
permission immediately before enqueue. Original QoS 0 can be lost after that
permission check fails, enqueue fails or ownership ends; it is never resurrected.
An original-QoS-0 position with a positive accounting charge is invalid evidence.
No new table, field encoding, command or RPC format is introduced.

Protocol basis: [OASIS MQTT 5.0](https://docs.oasis-open.org/mqtt/mqtt/v5.0/os/mqtt-v5.0-os.html),
sections 4.3.1 (at most once), 3.8.4 (QoS 1 to QoS 0 duplicates), and 4.4 (retry).

## Failure inventory before implementation

- Original QoS 0 sends again after successful enqueue but before completion,
  takeover, process loss or an ambiguous claim reply.
- An exact-retry Unchanged receipt returns a second candidate; failed/refused
  claims expose a packet; preclaimed completion debits twice or demands an obsolete
  parent revision after unrelated ACK/renewal.
- QoS 1 downgrade loses its charged obligation before enqueue, or an invalid
  original QoS 0 charge is erased as valid data.
- The new preclaim relaxes current owner/permission/clock checks or callback
  failure handling, or claims actual network delivery from a cursor commit.

Tests precede the correction using actual metadata commits and live Owners.
These are not product process acceptance; full sender/lifecycle work remains.

## Frozen context

Source `db1f9bccdf57413e81ccbf8616fbb280c7bcf563`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `internal/usecase/mqttsession/FLOW.md`: `7a8903adc4eb11cc3e31ba2a621f2ceb30ca5896916daa5a94e5f5d36726a244`
- `internal/app/FLOW.md`: `a586dfaff85f2e258e894c81e195909e27e9cac5428581545ae4ea77176ffe87`
- `pkg/channel/FLOW.md`: `e1afae5bebc02c8af1228479ee5cc32686507fe969b6500c27380527de249b01`
- `pkg/db/FLOW.md`: `3560287fef837ef40dcf04754ac1037a28ec85d26dae7748f465ab9524c624fe`
- `pkg/db/meta/FLOW.md`: `39d9e3024bb42cd14587b12557c0d58b9250444c21ceac6598581517096e0079`
- `pkg/protocol/publication/FLOW.md`: `c233c21b41ca6cfc2f4c34ffec50bba4eb099952591856b4f3233f231a2ec655`
