# README visual assets

The banner is an illustration. All application screenshots are direct browser
captures of the repository's current embedded UIs, taken on **2026-10-02**.
Screenshots have only been exported or compressed; UI text and results were not
painted, replaced, or generated.

## Capture identity

- Server source: `3261bbf6755d8cd504731b76214588311976f00c`.
- Server build: `GOWORK=off go build -o /tmp/wukongim-readme-redesign-server ./cmd/wukongim`.
  This is a development build, not a released-package screenshot.
- Chat SDK: `wukongimjssdk@1.4.0-beta.1`; the other three demos use
  `easyjssdk@2.0.5`.
- Runtime: a loopback-only single-node cluster with 256 hash slots and client
  Token authentication enabled. Streaming and AI behavior use simulated models.
- Manager: a separate isolated single-node cluster with 256 hash slots,
  eight logical Slots, authentication enabled, and a dummy account with
  read permissions only. No administrative operations were performed.
- Desktop viewport: 1440 × 900. Mobile chat: 390 × 844.

| Asset | Captured state |
| --- | --- |
| `chat-demo-en.png`, `chat-demo.jpg` | Alice and Bob connected in independent browser contexts; both message directions verified in the actual English and Chinese UIs |
| `chat-demo-mobile-cn.webp` | Current mobile layout with synchronized history from the same test conversation |
| `stream-demo.webp` | Question sent through the SDK; simulated-model reply received and completed |
| `support-demo.webp` | Visitor handoff accepted; human agent reply received in the visitor UI |
| `agent-demo.webp` | Document retrieval completed; creating a test to-do awaits confirmation |
| `manager-nodes-en.jpg`, `manager-nodes-cn.jpg` | One alive, ready node with fresh reported health, viewed with read permissions |
| `wukongim-hero.webp` | New 3:1 banner illustration generated with the built-in `image_gen` tool |

The asset hashes, dimensions, source identities, and verified states are recorded
in [capture-manifest.json](./capture-manifest.json). These captures do not qualify
a later release or imply that simulated models are live external services.

## Refresh the screenshots

1. Build the server from the intended source revision. Start the source launcher
   with `WK_DEMO_SERVER_BIN=/path/to/binary node demo/start.mjs --no-open`.
2. Use the actual launcher URLs. In independent browser contexts, connect the
   README's dummy Alice and Bob identities, send messages in both directions,
   and confirm the recipient receives each exact text before capture.
3. Capture English and Chinese chat separately, then mobile history. Start each
   remaining Demo through its UI; verify streaming completion, visitor handoff
   and human reply, or the pending tool confirmation before capture.
4. For Manager, start a separate loopback cluster with authentication enabled and
   a read-only dummy account. Confirm one node is alive and ready before capture.
5. Export screenshots directly from the browser. Keep PNG or use JPEG quality 94;
   use lossless WebP for the scenario previews. Preserve complete UI content.
6. Update the manifest and both READMEs together, inspect desktop/mobile layout
   and full-size links, then stop only the test processes created for capture.

## Banner generation prompt

Mode: built-in `image_gen`, opaque background. The generated original is retained
in the tool's image library; the repository asset is a WebP export at quality 88.

```text
Use case: ads-marketing.
Asset type: a newly designed GitHub README banner for the open-source WuKongIM real-time messaging server.
Create a premium, restrained landscape banner, approximately 3:1 aspect ratio, with an elegant dark ink background and the project's existing warm coral-orange accent, soft warm white typography. Make this feel like a carefully art-directed developer-product cover, clear even at README width.
Subject: real-time conversations flowing through self-hosted infrastructure. Use a few crisp, sculptural rounded speech bubbles and slim translucent message cards, connected by very fine coral paths. The cards can suggest chat, notifications and streaming AI through abstract marks, with one subtle layered core shape. Keep the composition airy and balanced with ample margin, thoughtful hierarchy, refined soft lighting and tactile matte/glass surfaces. No visual clutter.
Text verbatim: "WuKongIM" as the large typographic anchor, spelled exactly W u K o n g I M with this capitalization. Under it, the smaller line "Real-time messaging." and then the small line "Chat · Notifications · AI". Use clean, confident sans-serif typography. Place lettering in clear negative space and the sculptural messaging illustration in the remaining visual field, maintaining broad horizontal balance.
Constraints: illustration only, not a fabricated app screenshot. No server racks, cables, orange cubes, device inventory, circuit-board wallpaper, performance numbers, extra text, new logo, watermark, UI controls, glossy rainbow gradients, or busy star fields. Do not recreate the previous three-server cluster illustration. This banner will sit below an existing README heading and needs a compact editorial silhouette. Opaque background.
```
