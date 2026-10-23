# Offline permission pressure: local 2,000 SEND/s

Only the six retained first-measured/terminal service scrapes were read. Source is exact4cb/tree1f282. No new run, endpoint, build, DB read, or product change.

Origin cohort_busy is observed at terminal99/35/74, total208; the unique increment is the64cohort/1024call/16MiB origin limiter. First cut cohorts47/54/42 and calls384/385/319 plus1.36–1.63MiB credits are point observations below limits; they do not identify which limit was hit at rejection time. Serving128executing/1024waiters/16MiB/2s is a separate admission layer. Only serving admission=ok histograms are observed; absent busy series and unexported private waiter count remain unknown.

Permission fresh barrier interval means are about237/243/213ms, while snapshot means are about11–13microseconds. Proposal completed future lifetimes in the same interval are approximately239–293ms by Slot; this metric includes proposal waiting and is not FSM execution time. First/terminal sample boundaries and overlapping populations prevent causal attribution from sums alone.

The remaining discriminating signal is where fresh quorum/apply and person-directory callers wait during the initial seconds (plus CPU samples), rather than another larger concurrency setting. Any later profiled run is diagnostic perturbation only and retains the same original gates/short first-failure cutoff. Raw profiles/stacks/UIDs are private.
