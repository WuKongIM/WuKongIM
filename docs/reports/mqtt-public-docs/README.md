# MQTT public documentation validation

The user confirmed eight bilingual chapters: first exchange, overview,
authentication/topics, messages, persistent sessions/QoS, Will, HTTP/SDK
interoperability and operations/troubleshooting. The first example uses Node.js
and MQTT.js; MQTT-to-MQTT comes before cross-protocol integration.

`source-context.json` freezes the starting revision and applicable instruction
digests before edits. It binds the ordinary product binary to the previously
verified main-integration candidate. Product code is unchanged by this task;
`../mqtt-main-integration/verify.py --source` checks that source manifest.
The candidate is not a race build. The client runtime is Node.js v22.12.0 with
MQTT.js 5.16.0, installed by the example's exact npm lockfile; Bun is 1.3.11.

The final `mqtt-docs-quickstart-1.json` and `mqtt-docs-quickstart-3.json` receipts prove the exact example exchanged
messages in both directions, under token authentication, through single-node
and three-node clusters with 256 hash slots. The three-node case places Alice
and Bob on different ingresses and waits for public Slot leader stability.
The single-node case separately rejects an invalid token without credential
disclosure. Success requires exact payload bytes, authenticated sender,
application number, channel type and decimal-string message identities.
The final receipt is written after joined product cleanup, including failures.

Before implementation, the navigation test failed on the missing topic routes
and the process test failed because the example was absent. Initial live runs
then reached a ten-second reception deadline. A twenty-second bounded receive
wait and a sixty-second complete example deadline passed both topologies;
this is not a latency or performance qualification. A subsequent three-node run
while the site build was running hit the whole-example deadline;
`mqtt-docs-quickstart-3-incomplete.json` preserves that unsuccessful result,
and `mqtt-docs-quickstart-1-before-bounded-acks.json` preserves its paired
single-node success and the earlier example hash. The cause of the server-side
nonconfirmation was not established. The final client bounds subscription and
publish-acknowledgement promises independently at ten seconds, including socket
closure, rather than waiting for the global deadline. After the build finished,
the final example passed both topologies in 53.03 seconds (24.34 / 28.69).
This proves the final isolated run, not robustness during concurrent load. Complete site validation
also exposed missing MQTT internal transport IDs in the documentation catalog.
The catalog and bilingual internal inventory now match the authoritative Go
constants and aliases while retaining reserved IDs and private boundaries.

Reproduce from the repository root with Go 1.25.11 and Node.js >=20.11:

```bash
npm --prefix docs-site/examples/mqtt-quickstart ci --ignore-scripts --no-audit --no-fund
go build -o /tmp/wukongim-mqtt-docs ./cmd/wukongim
WK_E2E_BINARY=/tmp/wukongim-mqtt-docs \
WK_E2E_MQTT_REPORT_DIR=/tmp/mqtt-docs-results \
GOWORK=off go test -tags=e2e ./test/e2e/mqtt/docs_quickstart \
  -count=1 -timeout=4m -p=1 -v
cd docs-site
bun run verify
```

Verify these retained receipts from the repository root with
`python3 docs/reports/mqtt-public-docs/verify.py`. The FLOW changes also require
the `flow-doc-contracts` named check from the protected Review Agent policy.

These results establish this online Node.js example and documentation outputs.
They do not establish Node.js persistent replay/Will acceptance, every SDK's
rendering or recovery, public TLS deployment, full Linux/fault/load acceptance,
or availability in a released package/image. Existing MQTT interop/Will/recovery
evidence remains separate; the public chapters preserve those boundaries.

The complete documentation gate passed 212 focused contracts, example checks,
generated navigation/OpenAPI, lint, MDX/types, static export, internal links,
search, sitemap and per-page Markdown. The export contains 444 bounded RSC
URLs. All 12 JavaScript fragments in the 16 paired pages parse with Node.js.
Chinese quickstart and English topic previews were checked in the in-app
browser; code blocks, warning callouts and chapter links render correctly.
