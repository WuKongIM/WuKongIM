# v3 staged cleanup failure inventory

Parent verified with read-only Docker network inspect that an absent network returns `Error response from daemon: network <id> not found`, not `No such`. v2 removes the network in reader cleanup but keeps its active ID, then treats the second inspection as a failure.

Regression: create one owned container and network through the actual OwnedDocker interface, finish reader cleanup, then final cleanup. Stub uses the observed network-not-found diagnostic if the removed network is reinspected. Both stages must pass, each removed active ID must stop being queried, attempted names must remain, and both final label queries must run. Unrelated inspector errors must remain failures. No real Docker execution in this agent.
