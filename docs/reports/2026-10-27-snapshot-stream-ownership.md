# Snapshot stream ownership repair

PR #1043's first Linux default suite failed in the unchanged
`TestPluginBindingArchiveImportRedistributesNativeReplicas/1_to_1_node_cluster_1_replicas`:
target verification closed a database with one open snapshot. The exact subcase
passed 21 local Mac executions; those passes do not erase the CI failure.

The snapshot producer published pipe EOF/error before deferred view release.
Consumer Close only canceled and closed its pipe reader, without joining the
producer. Verification's `io.Copy → stream.Close → database.Close` could therefore
reach the native leak check before snapshot release. The same pattern existed
in both message backup producer APIs.

Before production changes, real public-API integration tests exercised ordinary
and backup metadata streams plus both message backup APIs. Reading one header
byte leaves the producer blocked in a longer pipe write. On unchanged code,
bounded Linux execution reproduced 30 metadata open-snapshot errors and errors
in both message APIs. EOF controls passed. These tests establish the early-Close
ownership defect; they do not uniquely reproduce the original CI full-EOF
interleaving. Linux used a pre-existing cached AMD64 image under emulation on
the Mac, with network disabled and exact container cleanup. This is functional
diagnosis, not native Linux performance acceptance.

Each stream now owns one producer-completion channel. Close cancels and closes
the pipe reader first, then joins the producer, so blocked writes can finish.
Deferred cleanup releases the pinned view before pipe terminal publication and
then closes the completion channel. Normal write errors remain the terminal
cause; an unfinished producer has a non-success fallback if its panic is
recovered by the goroutine registry. All closers join the same owner. Stream
bytes, database encodings, workload limits and migration gates are unchanged.

The original CI failure and all pre-repair outputs are preserved outside the
checkout. Fresh repaired-source regression and CI results must qualify this
candidate before the held original-scale workload starts. This repair does not
establish the cause of R13's two group-delivery deadline expirations.
