# Tutorial screenshots

Use screenshots for actions readers must recognize in a real UI. Keep conceptual
flows as small Mermaid diagrams inside a `tutorial-diagram` wrapper so the
SVG fits narrow screens. Each screenshot has a short numbered caption,
descriptive alt text, and a link to its original size through
`TutorialScreenshot`. Chat captures use the actual Chinese and English UIs;
the catalog and Web examples share captures with translated HTML captions.
Manager captures also use its Chinese and English UIs.

## Current capture identities

- Chat Demo and four-Demo catalog: local `main` source revision
  `38157974f`, launched through `node demo/start.mjs`; chat pins
  `wukongimjssdk@1.4.0-beta.1`. The launcher creates a loopback-only
  single-node cluster with 256 hash slots and Token authentication enabled.
  The first-message guide separately explains the older released Docker UI.
- Web SDK: `examples/javascript-web-quickstart`, pinned
  `wukongimjssdk@1.3.5`, against public container
  `ghcr.io/wukongim/wukongim:3.0.0-beta.21`, digest
  `sha256:bb57e4e02266080b2c2ec4e013103bc62b07a668fc64df0b9103df47047c3196`,
  with Token authentication enabled.
- Web EasySDK: official `WuKongIM/WuKongEasySDK-JS` example at tag `v2.0.5`,
  revision `b6d0bbe822b9c5b6f95a10d55b593d30184414f6`, built with
  `npm ci --ignore-scripts && npm run build`. Connected to a loopback-only
  single-node cluster built from local `main` revision `38157974f`, with
  Token authentication and matching WEB device flag `1`. Screenshots show
  the official source example, not a runnable copy of the tutorial's BFF code.
  The three literal bilingual TS blocks were separately type-checked against
  the published npm package `easyjssdk@2.0.5`.
- Manager: the same local-main test cluster, with authentication enabled,
  256 Hash Slots, and `initial_slot_count=8`. The capture account
  `docs-reader` has only read permissions. Node and task screens were read
  without performing administrative actions. Telemetry charts, scaling, and
  restore were not part of this capture.
- Captured on 2026-10-01 in a 1280 × 720 browser viewport. These images show the
  named versions; they do not certify a later server or SDK release.

| Asset under `public/tutorials/` | Capture | Verified state |
| --- | --- | --- |
| `first-message/login.jpg`, `login-en.jpg` | Crop: x430, y270, 420 × 420 | Automatic API address and Alice test identity filled; new-account credential checkbox visible |
| `first-message/recipient.jpg`, `recipient-en.jpg` | Crop: x430, y224, 420 × 275 | Direct chat selected; recipient UID targets Bob |
| `first-message/exchange.jpg`, `exchange-en.jpg` | Crop: x0, y0, 1280 × 280 | Bob receives Alice's message and sends a reply; Alice receives it |
| `demos/catalog.jpg` | Full page: 1280 × 1717 | Four scenario entries; launcher reports all five services ready |
| `web-sdk/exchange.jpg` | Full viewport | Both clients connected; successful SENDACK and matching RECEIVED events |
| `web-sdk/recovery.jpg` | Full viewport, scrolled to events | Bob reconnects after Alice sends offline; SYNCED and one recovered message |
| `easy-web/connect.jpg` | Crop: x20, y47, 1240 × 281; form at scrollY 40.5 | Matching loopback address and temporary Alice credentials; ready to connect |
| `easy-web/exchange.jpg` | Full page: 1280 × 1079 | Alice connects, sends sequence 1 successfully, and receives Bob's reply sequence 2; Bob's independent client also received Alice's message |
| `manager/login.jpg`, `login-en.jpg` | Crop: x740, y85, 480 × 570 | Read-only test account entered, password masked |
| `manager/nodes.jpg`, `nodes-en.jpg` | Full viewport | One alive node, ready with fresh health state |
| `manager/tasks.jpg`, `tasks-en.jpg` | Full viewport | Zero running/failed tasks; eight completed bootstrap tasks in retained history |

## Refresh procedure

1. Launch the current source Demos for chat/catalog captures; for the Web SDK
   example, start an isolated single-node cluster using the Docker tutorial's TOML.
   Use only dummy identities `quickstart-alice` / `alice-local-token` and
   `quickstart-bob` / `bob-local-token`. Bind test services to loopback.
2. Follow the first-message tutorial in independent browser sessions. Confirm
   both clients connect, each sender's pending indicator clears, and the other
   client receives the exact text. Capture the login, recipient and exchange
   states in both `?lang=zh` and `?lang=en`. Capture the catalog and confirm
   the launcher reports every service ready. Never capture real credentials or
   account data. The assigned API port is illustrative; readers keep its
   automatic value rather than copying it.
3. Run the independent Web example with `npm ci`, `npm run build` and
   `npm run dev`. Capture online exchange, then disconnect Bob, send from
   Alice, reconnect Bob and synchronize history. Check SENDACK and RECEIVED
   separately, and confirm the offline message appears through SYNCED.
   For Web EasySDK, build the official example at the manifest's exact tag,
   serve it only on loopback, and verify both online directions in separate
   clients. The current official log reports message sequence and Channel type;
   it intentionally omits complete payloads. Do not imply automatic offline
   recovery from these screenshots. Re-extract all three tutorial TS blocks
   and type-check them against the exact published npm version.
   For Manager, enable authentication on an isolated local-main cluster,
   configure a read-only dummy account, and capture login, nodes, and task
   counters in both languages. Confirm logical Slot rows and Hash Slot ranges
   agree with the test configuration; avoid executing management actions.
4. Save original browser JPEG captures. Crop only to focus on controls; do not
   paint over UI text or fabricate outcomes. Update dimensions and percentage
   marker coordinates in both MDX variants together.
5. Update the exact server/SDK identities above when recapturing. Inspect the
   tutorials on desktop and mobile, in both themes; verify images, captions,
   full-size links and expanded details. Run `bun run verify`.
6. Stop the source launcher, Web examples, and isolated Manager/EasySDK cluster;
   remove only the test container and volume created for this capture.

The login capture has the credential checkbox checked to show the fresh test
account path. This capture run pre-seeded those dummy tokens and unchecked the
box before signing in. Existing-account and migration instructions must always
retain the visible warning to leave it unchecked.
