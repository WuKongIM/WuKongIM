# Delivery warning field semantics addendum

The original parent v1 files and manifest remain unchanged. Additional read-only source inspection uses the same frozen 79a commit, after reading applicable valid package FLOW files.

`internal/app/online_delivery.go:59` logs `event.Recipients` and `event.Failure.RecipientUID` directly. `internal/runtime/delivery/runtime.go:499` emits exactly one terminal observation for one accepted plan with one `plan.Event.MessageID`, with Recipients = `plan.RecipientCount()`. This rules out interpreting the warning as a metric aggregated across multiple MessageIDs. The UID is a bounded sample of the first recipient in the first failed authority target, as selected by `planFailureSample` (runtime.go:636 onward). It is not a logged sender or proof that only this UID failed.

`processPlan` (runtime.go:514 onward) calls `EndpointsByTargets` once and, for an individual returned target error, records the first presence failure and continues to successful sibling targets. The failed target has no subsequent local presence retry or owner push in this invocation. This is a code-supported explanation for how an accepted durable message may miss online delivery when a presence lookup times out; it is not yet proof that the sample in question used this exact failed plan.

The prior exact group-member mapping and matching 435-recipient count remain strong structural evidence. An exact MessageID/sequence join is still required; successful sibling targets also mean a plan-level timeout does not prove zero recipients received. No raw UID, Channel ID, ClientMsgNo, payload or arbitrary error content is added here. No product test/reproduction/fix, source mutation, live action or cloud action occurred.
