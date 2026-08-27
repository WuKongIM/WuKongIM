import assert from "node:assert/strict";
import test from "node:test";

import {
  MAX_PERSON_MESSAGE_SYNC_LIMIT,
  createBffHandler,
  type WuKongIMProductClient,
} from "../src/server/bff";

test("the BFF person-message page limit stays aligned at 100", () => {
  assert.equal(MAX_PERSON_MESSAGE_SYNC_LIMIT, 100);
});

test("a browser session provisions a development identity through the BFF", async () => {
  const upstreamCalls: string[] = [];
  const productClient: WuKongIMProductClient = {
    async updateToken(input) {
      upstreamCalls.push(`token:${input.uid}:${input.token}`);
    },
    async discoverRoute() {
      upstreamCalls.push("route");
      return {
        tcpAddress: "",
        websocketAddress: "ws://127.0.0.1:5200",
        secureWebsocketAddress: "",
      };
    },
    async syncPersonMessages() {
      throw new Error("not used in this slice");
    },
  };
  const handle = createBffHandler({
    productClient,
    tokenFactory: () => "dev-token-alice",
  });

  const response = await handle(
    new Request("http://127.0.0.1:5173/api/development/identity", {
      method: "POST",
      headers: {
        "content-type": "application/json",
        host: "127.0.0.1:5173",
        origin: "http://127.0.0.1:5173",
      },
      body: JSON.stringify({ uid: "alice" }),
    }),
  );

  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), {
    uid: "alice",
    token: "dev-token-alice",
    websocketUrl: "ws://127.0.0.1:5200",
  });
  assert.deepEqual(upstreamCalls, ["token:alice:dev-token-alice", "route"]);
});

test("the development BFF rejects requests addressed through a non-loopback host", async () => {
  let reachedProductHttp = false;
  const productClient: WuKongIMProductClient = {
    async updateToken() {
      reachedProductHttp = true;
    },
    async discoverRoute() {
      reachedProductHttp = true;
      return {
        tcpAddress: "",
        websocketAddress: "ws://127.0.0.1:5200",
        secureWebsocketAddress: "",
      };
    },
    async syncPersonMessages() {
      reachedProductHttp = true;
      return [];
    },
  };
  const handle = createBffHandler({
    productClient,
    tokenFactory: () => "must-not-be-used",
  });

  const response = await handle(
    new Request("http://quickstart.example/api/development/identity", {
      method: "POST",
      headers: {
        "content-type": "application/json",
        host: "quickstart.example",
        origin: "http://quickstart.example",
      },
      body: JSON.stringify({ uid: "alice" }),
    }),
  );

  assert.equal(response.status, 403);
  assert.equal(reachedProductHttp, false);
});

test("identity provisioning fails closed on a non-WebSocket route", async () => {
  const productClient: WuKongIMProductClient = {
    async updateToken() {},
    async discoverRoute() {
      return {
        tcpAddress: "127.0.0.1:5100",
        websocketAddress: "https://127.0.0.1:5200",
        secureWebsocketAddress: "",
      };
    },
    async syncPersonMessages() {
      return [];
    },
  };
  const handle = createBffHandler({
    productClient,
    tokenFactory: () => "dev-token-alice",
  });

  await assert.rejects(
    handle(
      new Request("http://127.0.0.1:5173/api/development/identity", {
        method: "POST",
        headers: {
          "content-type": "application/json",
          host: "127.0.0.1:5173",
          origin: "http://127.0.0.1:5173",
        },
        body: JSON.stringify({ uid: "alice" }),
      }),
    ),
    /must use ws/,
  );
});

test("the development BFF rejects cross-origin browser mutations", async () => {
  let reachedProductHttp = false;
  const productClient: WuKongIMProductClient = {
    async updateToken() {
      reachedProductHttp = true;
    },
    async discoverRoute() {
      reachedProductHttp = true;
      return {
        tcpAddress: "",
        websocketAddress: "ws://127.0.0.1:5200",
        secureWebsocketAddress: "",
      };
    },
    async syncPersonMessages() {
      reachedProductHttp = true;
      return [];
    },
  };
  const handle = createBffHandler({
    productClient,
    tokenFactory: () => "must-not-be-used",
  });

  const response = await handle(
    new Request("http://127.0.0.1:5173/api/development/identity", {
      method: "POST",
      headers: {
        "content-type": "application/json",
        host: "127.0.0.1:5173",
        origin: "https://attacker.example",
      },
      body: JSON.stringify({ uid: "alice" }),
    }),
  );

  assert.equal(response.status, 403);
  assert.equal(reachedProductHttp, false);
});

test("identity provisioning rejects an invalid UID before Product HTTP", async () => {
  let reachedProductHttp = false;
  const productClient: WuKongIMProductClient = {
    async updateToken() {
      reachedProductHttp = true;
    },
    async discoverRoute() {
      reachedProductHttp = true;
      throw new Error("must not be reached");
    },
    async syncPersonMessages() {
      reachedProductHttp = true;
      return [];
    },
  };
  const handle = createBffHandler({
    productClient,
    tokenFactory: () => "must-not-be-used",
  });

  const response = await handle(
    new Request("http://127.0.0.1:5173/api/development/identity", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ uid: "../alice" }),
    }),
  );

  assert.equal(response.status, 400);
  assert.deepEqual(await response.json(), {
    error: "uid must use 1-64 letters, numbers, dots, underscores, or hyphens",
  });
  assert.equal(reachedProductHttp, false);
});

test("a browser session recovers person messages through the BFF", async () => {
  let receivedSyncInput: unknown;
  const productClient: WuKongIMProductClient = {
    async updateToken() {
      throw new Error("not used in this slice");
    },
    async discoverRoute() {
      throw new Error("not used in this slice");
    },
    async syncPersonMessages(input) {
      receivedSyncInput = input;
      return [
        {
          messageId: "99",
          messageSeq: 8,
          clientMsgNo: "client-8",
          fromUid: "alice",
          timestamp: 1_700_000_000,
          payload: "eyJ0eXBlIjoxLCJjb250ZW50Ijoib2ZmbGluZSJ9",
        },
      ];
    },
  };
  const handle = createBffHandler({
    productClient,
    tokenFactory: () => "not-used",
  });

  const response = await handle(
    new Request("http://127.0.0.1:5173/api/messages/sync", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        uid: "bob",
        peerUid: "alice",
        startMessageSeq: 7,
        endMessageSeq: 0,
        limit: 50,
        pullMode: 1,
      }),
    }),
  );

  assert.equal(response.status, 200);
  assert.deepEqual(receivedSyncInput, {
    loginUid: "bob",
    peerUid: "alice",
    startMessageSeq: 7,
    endMessageSeq: 0,
    limit: 50,
    pullMode: 1,
  });
  assert.deepEqual(await response.json(), {
    messages: [
      {
        messageId: "99",
        messageSeq: 8,
        clientMsgNo: "client-8",
        fromUid: "alice",
        timestamp: 1_700_000_000,
        payload: "eyJ0eXBlIjoxLCJjb250ZW50Ijoib2ZmbGluZSJ9",
      },
    ],
  });
});

test("message recovery rejects an unbounded page before Product HTTP", async () => {
  let reachedProductHttp = false;
  const productClient: WuKongIMProductClient = {
    async updateToken() {
      throw new Error("not used in this slice");
    },
    async discoverRoute() {
      throw new Error("not used in this slice");
    },
    async syncPersonMessages() {
      reachedProductHttp = true;
      return [];
    },
  };
  const handle = createBffHandler({
    productClient,
    tokenFactory: () => "not-used",
  });

  const response = await handle(
    new Request("http://127.0.0.1:5173/api/messages/sync", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        uid: "bob",
        peerUid: "alice",
        startMessageSeq: 0,
        endMessageSeq: 0,
        limit: 10_000,
        pullMode: 1,
      }),
    }),
  );

  assert.equal(response.status, 400);
  assert.equal(reachedProductHttp, false);
});
