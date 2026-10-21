# e302 continuation: terminal evidence

The single authorized continuation is finished. This is a failed pre-clock run with successful exact cleanup, not a successful rehearsal or release qualification.

- Request: `chat-20261021T095421Z-cf11d65c`; Lease: `chat-20261021T095421Z-cf11d65c-rehearsal-2`.
- Control: `e30299fa59ea6669ea71024eabe209840f241f19`; product: `fc18221282f289088ec9b4282fda8e0bfc67a3b8`.
- Top run [39605452697](https://github.com/WuKongIM/WuKongIM/actions/runs/39605452697), run number 33 / attempt 1: completed failure. The original number 32 failure remains preserved and its initial cause remains unknown.
- Continuation authentication accepted predecessor 32 → 33, rehearsal attempt 2, and inherited CNY 74.4776 as a **full admitted Quote reserve**. The new Quote was also CNY 74.4776; CNY 148.9552 is the sum of conservative reserves, not a measured or settled bill. Actual bills remain unknown. The original cumulative CNY 1500 ceiling / CNY 1350 stop was retained.
- Bundle, Quote, Acquire and deployment passed. The exact deployment readiness had 3 healthy service nodes, 256 leaders / replica sets, no pending tasks and Prometheus 7/7.
- The original **before-kill** typed diagnostic proves start command exit 0, systemd state read exit 0, but state classifier exit 1. The unit was `active/running`; the only invalid property was empty `Job=`. This happened 7 seconds after the readiness loop started, not after the readiness deadline. The subsequent bounded Linux replay of the exact classifier returned 1 for empty Job and 0 after a single-variable Job=0 substitution. This locates this attempt's control parser failure; it does not establish a product workload failure or explain the old attempt.
- `original-state-safe-reconstruction.txt` reconstructs only seven allowlisted properties, has no terminal newline, and is **104 bytes**, SHA256 `57c3e320a832417c5e803553961f261771af23813115c207218162f214488524`, exactly matching the original typed unit receipt. No raw journal, environment, Manager token or private key was read or archived.
- Authorized Stop [39609861645](https://github.com/WuKongIM/WuKongIM/actions/runs/39609861645) succeeded. Exact Release [39609951023](https://github.com/WuKongIM/WuKongIM/actions/runs/39609951023) succeeded. Zero inventory was observed at **2026-10-21T13:40:52.048097405Z**, matching the new Lease / plan digest / account hash and all 12 resource scopes. Both triggered finalizers also completed successfully. No cancel, repair trailer, source change or additional procurement was used.
- The artifact named `handoff` contains the same 55 files as the cleanup artifact; it contains **no handoff.json or accepted run-start.json**. Artifact naming is not run-start proof. No 2-hour workload, 72-hour formal run, capacity stage or recovery stage passed.

## Verification and source boundaries

`verify-terminal.py` checked all four terminal ZIPs against exact API artifact ID, size and SHA256, all ZIP CRCs, safe paths, strict JSON and member bounds. The two 55-member artifacts match byte-for-byte by member. The standalone preflight/admission receipts match their copies. Current Acquire/Quote/Release copies match previously verified child artifacts. The continuation proof's manifest SHA and all 18 historical evidence files close. Source, control, attempt, budget, plan, selector and zero-inventory identity close. `verification.json` and `member-inventory.json` record these checks.

The frozen folder omits the four ZIP containers, product binaries, SSH config/known_hosts and all private identities. Public member bytes and their hashes are retained. To rerun the ZIP-level check, recover the four exact artifact ZIPs while retained, use their archived API metadata, retain the earlier verified child Quote/Acquire/Release files at the script's documented relative paths, and run:

```sh
python3 terminal/verify-terminal.py --root /path/to/rc-continuation-e302
```

This offline verifier does not newly authenticate a live provider or OIDC identity. The workflow performed the live admission and child operations; this archive checks the retained evidence and its identity closure. The final API snapshot was observed at 2026-10-21T13:48:40Z. The workflow's tracking-Issue step and plaintext runner-identity removal step both succeeded. No additional scheduler/API probe was needed for this seal; scheduler lifecycle remains owned by the existing workflows.

Earlier immutable stage archives remain unchanged: `start-public-frozen`, `bundle-public-frozen`, `quote-acquire-public-frozen`, `deployment-public-frozen`, `bounded-unit-public-frozen`, and `zero-public-frozen`. Their point-in-time statements are superseded only by this terminal evidence, never overwritten.
