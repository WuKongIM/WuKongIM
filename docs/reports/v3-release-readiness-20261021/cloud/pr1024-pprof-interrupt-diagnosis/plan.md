# Read-only pprof interrupt diagnosis plan

Freeze exact source `2cc562fa39f7b430c61a4494636f6ee0ec0d631a`, applicable root AGENTS/CONTEXT, shell helper and its integration/static tests. No applicable FLOW covers scripts root. The latest change is solely the unrelated terminal-evidence test fixture; neither this helper nor its tests changed from c91.

Ranked falsifiable hypotheses, before probes:

1. A real helper cancellation/registration race leaves a newly spawned curl absent from CAPTURE_PIDS, so terminate_children cannot signal or join it. A signal forced exactly before PID append should leave the real HTTP request active until its own max-time; a signal after full registration should promptly cancel it.
2. Curl is already terminated but HTTP request Context cancellation or the handler's deferred active decrement is observed late. Record child existence, request Context done, handler decrement and helper exit separately. Prompt child/context termination with delayed decrement supports this; live curl and uncanceled context refute that mechanism in the probe.
3. Shared-runner contention delays scheduling. This is only a candidate: the archive has no per-process scheduling samples. Locally bounded probes can prove an independent race, but cannot quantify original runner contention or identify the exact CI signal instruction without its missing trace.

Boundaries: at most one original focused test invocation and two scratch real-HTTP probes. The probes will run the frozen original helper with BASH_ENV DEBUG timing hooks, without changing the repository source. Hooks pause only at a known command boundary and send the same TERM signal; responses use loopback synthetic HTTP servers and the original 10-second handler delay. Record elapsed/request lifecycle/helper status/remaining child PIDs and cleanup, retain the first outcome of each. No full suite, CI rerun, cloud, product startup, credential reads, threshold/policy changes, or repository edits.

Original CI event already proves a normal-exit check failure at server2 after the existing two-second drain observation. The subsequent ~4-second test cleanup ending at ~6.034 seconds matches the configured CPU curl max-time (1+5 seconds), but that match is circumstantial until the probe distinguishes the lifecycle.

If the exact race is proven, deliver the smallest regression seam and code boundary. Do not implement a repository fix in this read-only round, do not relax the drain deadline or replace the request-zero assertion, and do not treat a locally green original test as proof of a fix.
