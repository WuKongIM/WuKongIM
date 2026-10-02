# JavaScript / Web quickstart example

This runnable TypeScript example shows how a browser application can connect
two users with `wukongimjssdk@1.3.5`, exchange text messages, disconnect, and
restore missed messages.

The browser never calls WuKongIM HTTP API directly. A small localhost Node.js
service obtains development tokens and routes, then exposes only the two
operations the browser needs.

> This is a development example, not an account system or production gateway.
> Keep WuKongIM HTTP API on a trusted network and replace the development identity
> endpoint with your authenticated application backend.

## Prerequisites

- Node.js 20.11 or newer and npm.
- A ready WuKongIM single-node cluster.
- WuKongIM HTTP API reachable from this Node.js process.
- A `ws_addr` or `wss_addr` returned by `/route` that the browser can reach.

## Run

```bash
npm ci
npm run dev
```

Open <http://127.0.0.1:5173>.

1. Connect Alice and Bob.
2. Send one message in each direction.
3. Disconnect Bob and send another message from Alice.
4. Reconnect Bob to load the missed message.

The event panel distinguishes a local outgoing message, the server send result,
an online incoming message, and a message restored after reconnect.

The first person-message directory is projected asynchronously after SENDACK.
A latest-page history request can briefly return HTTP 200 with an empty list.
The example's backend retries only an empty latest-page result (both sequence
bounds zero) within its existing 20-attempt budget, waiting 250 ms between
attempts. A genuinely empty chat returns an empty list after at most 19 waits
(4.75 seconds plus HTTP request time). Nonempty results and ordinary cursor pages
return immediately. Unrelated errors still fail; production applications must
provide their own consistency and request-deadline policy.

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `WK_DOCS_QUICKSTART_HOST` | `127.0.0.1` | UI and local service bind address. Only loopback names and addresses are accepted. |
| `WK_DOCS_QUICKSTART_PORT` | `5173` | UI and local service port. |
| `WK_DOCS_QUICKSTART_PRODUCT_HTTP_URL` | `http://127.0.0.1:5001` | WuKongIM HTTP API base URL used only by Node.js. |

The browser calls these same-origin endpoints:

- `POST /api/development/identity`
- `POST /api/messages/sync`

The Node.js service maps them to `POST /user/token`, `GET /route`, and
`POST /channel/messagesync`. It validates loopback Host and Origin headers,
bounds request bodies and sync pages, and never sends the WuKongIM HTTP API address
to the browser.

## Project layout

```text
src/client/   Browser UI, SDK wrapper, and reconnect flow
src/server/   Local service and WuKongIM HTTP API client
public/       HTML and CSS
test/         Fast unit tests
e2e/          Real-browser messaging and reconnect assertions
scripts/      Browser bundle build
```

## Check changes

```bash
npm test
npm run build
```

`npm run build` creates `dist/` and runs TypeScript without emitting type-check
output. The browser bundle removes SDK `console` calls because the published SDK
can log decoded message data.

## Browser tests

The repository's Go E2E harness starts a single-node cluster with Token
authentication enabled and runs the pinned Chromium tests through `npm run test:e2e`.
The tests use BFF-issued credentials and cover online messaging and reconnect
recovery. Run this scenario from the repository root following
`test/e2e/message/javascript_web_quickstart/AGENTS.md`.

## Production work still required

- Authenticate users in your own backend and issue short-lived credentials.
- Serve the page over HTTPS and connect with a correctly configured `wss://`
  endpoint.
- Add authorization, rate limits, audit logging, token revocation, and durable
  business data.
- Design group chat, custom messages, media, push, background behavior, and
  multi-device conflict handling separately.
- Test reconnect, offline recovery, browser lifecycle, and rollback in every
  browser and deployment environment you support.

On reconnect, live delivery may precede the history response. The browser test checks that history contains the offline message and that the UI displays it once across both paths.
