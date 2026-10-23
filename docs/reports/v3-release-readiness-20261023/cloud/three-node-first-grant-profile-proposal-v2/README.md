# Three-node sampler V2 once-status correction

Preparation and synthetic process fixtures only. V1 and its manifest remain unchanged. The reproduced defect was a second unclaimed watcher overwriting the first status when its pre-claim source/launcher/Slot/owner/path guard failed. All five cases failed before implementation and now preserve original bytes and mtime and launch no helper.

The only watcher correction moves the atomic claim immediately after safe ownership/output checks and before source/launcher/node guards; only a claim owner may write the status. Unclaimed failures print closed status/reason/request-count fields. Two concurrent valid watchers admit one owner and three helpers total; the duplicate emits already_claimed without status writes. Existing source failures owned by the first claim still produce a closed skipped status. Helper bytes are unchanged from V1.

Ten unittest invocations passed (six unique method bodies; the four original methods execute twice because the extended fixture imports and subclasses their class). Tests use synthetic git/binary/helper files and owned fake processes; no TCP, product, real profile, cloud or native build. Existing time/byte caps, six requests maximum, shared cancellation and whole-run budget remain as V1. A separate exact-source/binary/launcher input proposal and root approval remain necessary before any actual load.
