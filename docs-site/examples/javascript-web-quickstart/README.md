# JavaScript / Web quickstart example

This runnable TypeScript example shows how a browser application can connect
two users with `wukongimjssdk@1.3.5`, exchange text messages, disconnect, and
restore missed messages.

The browser never calls WuKongIM Product HTTP directly. A small localhost Node.js
service obtains development tokens and routes, then exposes only the two
operations the browser needs.

> This is a development example, not an account system or production gateway.
> Keep Product HTTP on a trusted network and replace the development identity
> endpoint with your authenticated application backend.

## Prerequisites

- Node.js 20.11 or newer and npm.
- A ready WuKongIM single-node cluster.
- Product HTTP reachable from this Node.js process.
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

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `WK_DOCS_QUICKSTART_HOST` | `127.0.0.1` | UI and local service bind address. Only loopback names and addresses are accepted. |
| `WK_DOCS_QUICKSTART_PORT` | `5173` | UI and local service port. |
| `WK_DOCS_QUICKSTART_PRODUCT_HTTP_URL` | `http://127.0.0.1:5001` | WuKongIM Product HTTP base URL used only by Node.js. |

The browser calls these same-origin endpoints:

- `POST /api/development/identity`
- `POST /api/messages/sync`

The Node.js service maps them to `POST /user/token`, `GET /route`, and
`POST /channel/messagesync`. It validates loopback Host and Origin headers,
bounds request bodies and sync pages, and never sends the Product HTTP address
to the browser.

## Project layout

```text
src/client/   Browser UI, SDK wrapper, and reconnect flow
src/server/   Local service and Product HTTP client
public/       HTML and CSS
test/         Fast unit tests
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
