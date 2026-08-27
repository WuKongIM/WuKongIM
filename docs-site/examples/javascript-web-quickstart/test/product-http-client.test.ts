import assert from "node:assert/strict";
import test from "node:test";

import { ProductHttpClient } from "../src/server/product-http-client";

test("the trusted client maps a development identity to POST /user/token", async () => {
  const requests: Request[] = [];
  const client = new ProductHttpClient({
    baseUrl: "http://127.0.0.1:5001/",
    fetch: async (input, init) => {
      const request = new Request(input, init);
      requests.push(request);
      return Response.json({ status: 200 });
    },
  });

  await client.updateToken({
    uid: "alice",
    token: "dev-token-alice",
    deviceFlag: 1,
    deviceLevel: 0,
  });

  assert.equal(requests.length, 1);
  assert.equal(requests[0]?.url, "http://127.0.0.1:5001/user/token");
  assert.equal(requests[0]?.method, "POST");
  assert.deepEqual(await requests[0]?.json(), {
    uid: "alice",
    token: "dev-token-alice",
    device_flag: 1,
    device_level: 0,
  });
});

test("the trusted client discovers the configured WebSocket route", async () => {
  const requests: Request[] = [];
  const client = new ProductHttpClient({
    baseUrl: "http://127.0.0.1:5001",
    fetch: async (input, init) => {
      requests.push(new Request(input, init));
      return Response.json({
        tcp_addr: "127.0.0.1:5100",
        ws_addr: "ws://127.0.0.1:5200",
        wss_addr: "",
      });
    },
  });

  const route = await client.discoverRoute();

  assert.equal(requests[0]?.url, "http://127.0.0.1:5001/route");
  assert.equal(requests[0]?.method, "GET");
  assert.deepEqual(route, {
    tcpAddress: "127.0.0.1:5100",
    websocketAddress: "ws://127.0.0.1:5200",
    secureWebsocketAddress: "",
  });
});

test("the trusted client maps person-message recovery to POST /channel/messagesync", async () => {
  const requests: Request[] = [];
  const client = new ProductHttpClient({
    baseUrl: "http://127.0.0.1:5001",
    fetch: async (input, init) => {
      requests.push(new Request(input, init));
      return Response.json({
        start_message_seq: 7,
        end_message_seq: 0,
        more: 0,
        messages: [
          {
            message_id: 99,
            message_idstr: "99",
            message_seq: 8,
            client_msg_no: "client-8",
            from_uid: "alice",
            timestamp: 1_700_000_000,
            payload: "eyJ0eXBlIjoxLCJjb250ZW50Ijoib2ZmbGluZSJ9",
          },
        ],
      });
    },
  });

  const messages = await client.syncPersonMessages({
    loginUid: "bob",
    peerUid: "alice",
    startMessageSeq: 7,
    endMessageSeq: 0,
    limit: 50,
    pullMode: 1,
  });

  assert.equal(
    requests[0]?.url,
    "http://127.0.0.1:5001/channel/messagesync",
  );
  assert.deepEqual(await requests[0]?.json(), {
    login_uid: "bob",
    channel_id: "alice",
    channel_type: 1,
    start_message_seq: 7,
    end_message_seq: 0,
    limit: 50,
    pull_mode: 1,
  });
  assert.deepEqual(messages, [
    {
      messageId: "99",
      messageSeq: 8,
      clientMsgNo: "client-8",
      fromUid: "alice",
      timestamp: 1_700_000_000,
      payload: "eyJ0eXBlIjoxLCJjb250ZW50Ijoib2ZmbGluZSJ9",
    },
  ]);
});

test("message sync rejects a response without the precision-safe message_idstr", async () => {
  const client = new ProductHttpClient({
    baseUrl: "http://127.0.0.1:5001",
    fetch: async () =>
      Response.json({
        messages: [
          {
            message_id: 9_007_199_254_740_992,
            message_seq: 8,
            client_msg_no: "client-8",
            from_uid: "alice",
            timestamp: 1_700_000_000,
            payload: "",
          },
        ],
      }),
  });

  await assert.rejects(
    client.syncPersonMessages({
      loginUid: "bob",
      peerUid: "alice",
      startMessageSeq: 7,
      endMessageSeq: 0,
      limit: 50,
      pullMode: 1,
    }),
    /invalid message_idstr/,
  );
});

test("route discovery rejects an incomplete Product HTTP response", async () => {
  const client = new ProductHttpClient({
    baseUrl: "http://127.0.0.1:5001",
    fetch: async () => Response.json({ tcp_addr: "127.0.0.1:5100" }),
  });

  await assert.rejects(client.discoverRoute(), /invalid ws_addr/);
});
