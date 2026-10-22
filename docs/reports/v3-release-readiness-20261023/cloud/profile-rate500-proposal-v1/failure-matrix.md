# Fixed500 diagnostic proposal — before materialization/execution

- Only workload.send_rate_per_second2000→500 may change; recursive parser comparison must find exactly that one typed field. No gate/expiry/retry/burst/canary/population/config change to force pass.500 is belowformal target and cannot qualify release/performance acceptance.
- Existing LoadConfig/Config.Validate and BuildCoordinatorAssignments(cfg,1) must compute true identities. RunID is deliberately unchanged, so newly computed runhash may remain identical; assignment fence changes with config. No forged receipt/identity or Python recreation of Go confighash.
- Existing deployment-only validate guard must remain intact; local scenario must not be relabeledformal or its guard bypassed. If no offlinePlanentry is callable without a parser build, record that prerequisite rather than launchworkload to learnidentity.
- Exact source/build guard, native5b307/081948, Arecipient/batch1/compaction1000/5s/GOMAX2+1,10000online/2000groups/seed, originalstrictgates,10sexpiry/retry and sameCPU10+nominal5sGORdebug0 sampler remain unchanged.
- Wrong configdiff/hash/source/native receipt/ownership/deadline must preventexecution; no automaticrepair/retry/ratefallback.
-500QPS may still failfirstattempt beforeexpiry, or expirefirst, or reach120s operatorstop. Preserve actual terminal and scope; no pass claimed from reducedrate/operatorstop.
-600s total/570TERM+5KILL/360startup/120target/480lateststop/exit7hold90/PGID+64portcleanup unchanged. Same samplingoverhead/active-drain uncertainty; no extrapolationtocloudrootcause.
- No services/load/parsercompile yet authorized by this preparation; samplerfixturesalreadyqualified inproposal-v2 and unchanged. Parser/source/configchecks are finite, offline and happenbeforemeasurementquiet.
