# Owned-session cleanup failure inventory (before repair)

- A test child uses Setpgid and survives killing only the driver process group.
- Driver success/exit 7/panic/deadline/output limit/cancellation bypasses test Cleanup.
- Repeated cancellation interrupts cleanup or replaces the original test outcome.
- Cleanup targets the caller, an unrelated session, or a PID whose SID changed.
- Process enumeration errors or inaccessible identity are reported as zero survivors.
- Zombies keep cleanup pending forever even though no live process remains.
- A cleanup failure is treated as success or overwrites the original failure details.
- Separate stdout/stderr exceed their combined policy limit or lose failure evidence.
- A child holds inherited pipes open after driver failure and obscures exit 7 as timeout.

The pre-repair probe below uses real processes, including a separate child PGID,
and performs exact-PID emergency cleanup after recording the reproduced survivor.
