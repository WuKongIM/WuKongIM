# One test assertion correction after the first implementation attempt

The first focused run after implementation had 25 successful methods and one failure.
The failed assertion looked for activation SHA c×40 anywhere in serialized historical
snapshot evidence and incorrectly matched the fixture snapshot digest c×64. Its intended
contract is historical *control* identity independence. The existing assertion now compares
only control_sha, seal_control_sha and candidate.control_sha. No new test or behavior was
added after implementation; the source implementation stayed unchanged. First-red and
focused-attempt-1 logs are retained. No blind focused retry is performed; the required
related and full suites validate the corrected assertion as part of their first runs.
