import assert from "node:assert/strict";
import test from "node:test";

import { BrowserBffClient } from "../src/client/browser-bff";

test("the browser provisions an identity only through the same-origin BFF", async () => {
  const requests: Request[] = [];
  const bff = new BrowserBffClient({
    fetch: async (input, init) => {
      requests.push(new Request(input, init));
      return Response.json({
        uid: "alice",
        token: "dev-token-alice",
        websocketUrl: "ws://127.0.0.1:5200",
      });
    },
  });

  const identity = await bff.provisionIdentity("alice");

  assert.equal(requests[0]?.url, "http://localhost/api/development/identity");
  assert.equal(requests[0]?.method, "POST");
  assert.deepEqual(await requests[0]?.json(), { uid: "alice" });
  assert.deepEqual(identity, {
    uid: "alice",
    token: "dev-token-alice",
    websocketUrl: "ws://127.0.0.1:5200",
  });
});

test("the browser asks the BFF for bounded person-message recovery", async () => {
  const requests: Request[] = [];
  const bff = new BrowserBffClient({
    fetch: async (input, init) => {
      requests.push(new Request(input, init));
      return Response.json({ messages: [] });
    },
  });

  const messages = await bff.syncPersonMessages({
    uid: "bob",
    peerUid: "alice",
    startMessageSeq: 0,
    endMessageSeq: 0,
    limit: 50,
    pullMode: 1,
  });

  assert.equal(requests[0]?.url, "http://localhost/api/messages/sync");
  assert.deepEqual(await requests[0]?.json(), {
    uid: "bob",
    peerUid: "alice",
    startMessageSeq: 0,
    endMessageSeq: 0,
    limit: 50,
    pullMode: 1,
  });
  assert.deepEqual(messages, []);
});
