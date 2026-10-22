# Retained500QPS loss observations — offline addendum

The parent12payload freeze remains unchanged. This addendum reads only existing worker evidence, three canonical service stdout logs and node2 existing metric cuts. No DB, pprof, service request, compilation, new workload or source change occurred. Eight inputfiles retained identical bytes/mtime_ns/SHA256 before and after these reads.

All five retained expired samples are domain4/generation1 ordinarygroup samples, uniquely matched within the bounded original10000 initialpopulation. Exactindexes/fingerprints are in safe-expired-identities.json; no rawUID/channel/clientnumber/messagebody is output. The existing identitytranscription was reused with50k maximum inspected indexes,16670 attemptedworkerownedcandidates and about0.060s CPU. Relevant clean79a identity/payload/traffic sources were byteequal to its earlier reference. A matchingfingerprint identifies a candidate, not its loss cause; no exactACK packet/time/messageID is reconstructed here.

|Worker|Ordinal|Group index|Fingerprint|
|---|---:|---:|---|
|0|114|1656|73af27ef912febfe6a7b032d961122bb|
|0|414|1623|3276a62a15a9216f4683222b7751c108|
|1|214|1735|562ccbccd13140cd9a4a1ca28610ea89|
|1|514|1759|3e1a78cb539878dc6c90b460b9bf7398|
|2|314|1646|0db0904a5c1178a44b1596dfd762770d|

Within firstgrant→terminalworkercut, the three canonicalservice logs contain207 delivery-plan-incomplete events: node1=60,node2=75,node3=72. All207 have phasepresence/resulttimeout/modedurable and the fixed contextdeadlineexceeded sentinel. Observedownerpush count is0, with0unparseablecandidate lines and0outsidewindow events. Duplicate app.log/warn.log copies were not counted. These aggregatewarnings are not an exactmessageID join to fiveexpiredsamples.

Node2 firstretainedmetriccut has recipientqueue14/1024 andinflight40/320, so neither is full at that point. Terminalcut hasqueue0/inflight0. Individualscrape timestamps were not retained; firstcut is around+5s and misses earlier activity. This endpoint evidence and the independent singleGORsnapshot must not be generalized to continuouspool occupancy.

The finalreport is stillmissing due coordinatorfinalize8. The retainedcut classification product_failure is not a completefinalverdict. Nativehost, lowerofferedrate, samplingperturbation, active/drain and source/windowlimits from the parent freeze remain. Further inference belongs to independent signals/source analysis; there is no automaticrerun.
