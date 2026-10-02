# Live Demo process/browser failure inventory

Written before the implementation, against approved source `992321e67`.
Approved seams: the live Demo browser UI, role-authorized loopback BFF,
real wsmux JSON-RPC transport and existing Product HTTP management APIs.
Every run owns a real 256-hash-slot single-node cluster; no mock substitutes
for message acceptance, permission decisions, recipient delivery or state.

| Failure / race | Required observation |
| --- | --- |
| BFF missing, unavailable or rejected bootstrap | No connected/success state; user can retry |
| Invalid JSON, oversized body, foreign Origin/Host, unsupported method | Explicit rejection; no role or room created |
| Invalid SDK token | Actual gateway refuses connection; inputs remain gated |
| New viewer accidentally reuses creator credentials | Distinct UID; invitation contains no token/ownerKey/viewerKey |
| Viewer requests host role or control with viewer capability | No owner credential; control denied |
| Wrong room / foreign target / stale recovery capability | Denied without changing another room |
| Unicode malformed invitation/recovery capability | Clean authorization rejection, no internal exception |
| Own ACK mistaken for recipient delivery | Separate genuine second-viewer incoming frame and DOM assertion |
| Own echo / repeated logical event | One chat row per logical event, bounded identity cache |
| Like counted locally as whole-room total | Genuine other-viewer interaction; no invented cumulative/online number |
| Malformed payload or viewer-forged room_state | Actual product-delivered message ignored; no pageerror/false announcement |
| Text interpreted as HTML | Literal text; no script execution |
| Single-viewer mute removes receiving ability | Product rejects both text and like, yet victim receives another viewer's message |
| Refresh generates a new UID or loses mute | Same UID; current permission restored; cannot send |
| Unmute or another room affects unrelated audience | Only target in this room changes |
| Product mute changed outside BFF while its snapshot still allows | Actual SDK reason4 produces explicit rejection; stale snapshot cannot reopen input |
| Snapshot acquired before newer live update returns late | Newer notice/policy/roster wins; input gated until merge |
| Product applied mute but real response was lost | Pending separately from confirmed value; same immutable operation retries; opposite operation denied |
| Pending target tries to leave | Reject until original permission operation resolves; snapshot never references a missing viewer |
| Product control failed before application | No claimed confirmed mute; pending/rejection remains truthful |
| State saved while IM publication fails | Current state query works; notification pending; genuine manual retry delivers |
| Old notification retried after newer state/new viewer | Does not roll back version or evict newcomer missing from old roster |
| SEND response lost after real acceptance | Result unknown; immutable logical retry; no automatic resend storm |
| Offline audience reconnects | Current announcement/permissions restored; no old barrage/like playback or history polling |
| Previously denied audience observes mute, then unmuted offline | Resume restores current permission even when /state equals bootstrap version |
| Hidden page accumulates animations | Queue cleared/bounded; new foreground traffic still works |
| Real-client burst exceeds display capacity | Chat <=100, animation <=12 and bounded queue/cache; responsiveness maintained |
| Mobile/reduced motion/keyboard | No horizontal overflow; composer and role navigation usable |
| Missing hashed asset / index cache | Missing asset 404; actual JS/CSS served; GET/HEAD/index revalidation work |
| Leave/close/expiry/restart | Credentials invalidated; no continued live send after removal; cleanup bounded |
| Old actual heartbeat returns 410 after new room created | Old async result cannot retire the replacement session |
| Process error/port conflict/test failure | Retain red report and diagnostics; terminate all owned processes |

Artifacts: report/failure JSON, bounded sanitized actual incoming event identities,
source and frozen instruction SHA256, binary SHA256, screenshots, process logs
and explicit cleanup result. Never serialize bootstrap credentials or CONNECT frames.
