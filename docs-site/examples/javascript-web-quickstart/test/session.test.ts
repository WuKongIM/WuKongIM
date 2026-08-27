import assert from "node:assert/strict";
import test from "node:test";

import {
  QuickstartSession,
  type ChatRuntime,
  type DevelopmentIdentity,
  type QuickstartBff,
  type RuntimeMessage,
  type SendAcknowledgement,
} from "../src/client/session";

class FakeChatRuntime implements ChatRuntime {
  messageListener?: (message: RuntimeMessage) => void;
  acknowledgementListener?: (ack: SendAcknowledgement) => void;
  disconnectListener?: (reasonCode?: number) => void;
  syncResult: RuntimeMessage[] = [];

  onMessage(listener: (message: RuntimeMessage) => void): () => void {
    this.messageListener = listener;
    return () => {
      this.messageListener = undefined;
    };
  }

  onSendAcknowledgement(
    listener: (ack: SendAcknowledgement) => void,
  ): () => void {
    this.acknowledgementListener = listener;
    return () => {
      this.acknowledgementListener = undefined;
    };
  }

  onUnexpectedDisconnect(listener: (reasonCode?: number) => void): () => void {
    this.disconnectListener = listener;
    return () => {
      this.disconnectListener = undefined;
    };
  }

  async connect(_identity: DevelopmentIdentity): Promise<{ nodeId?: number }> {
    return { nodeId: 1 };
  }

  async disconnect(): Promise<void> {}

  async sendText(_peerUid: string, _text: string): Promise<{ clientSeq: number }> {
    return { clientSeq: 9 };
  }

  async syncMessages(_peerUid: string): Promise<RuntimeMessage[]> {
    return this.syncResult;
  }
}

test("a connected session exposes queued send and successful SENDACK", async () => {
  const bff: QuickstartBff = {
    async provisionIdentity(uid) {
      return {
        uid,
        token: "dev-token-alice",
        websocketUrl: "ws://127.0.0.1:5200",
      };
    },
  };
  const runtime = new FakeChatRuntime();
  const session = new QuickstartSession({
    uid: "alice",
    peerUid: "bob",
    bff,
    runtime,
  });

  await session.connect();
  await session.sendText("hello bob");
  runtime.acknowledgementListener?.({
    clientSeq: 9,
    messageSeq: 4,
    reasonCode: 1,
  });

  const snapshot = session.snapshot();
  assert.equal(snapshot.connection, "connected");
  assert.equal(snapshot.nodeId, 1);
  assert.deepEqual(
    snapshot.events.map(({ kind, text }) => ({ kind, text })),
    [
      { kind: "status", text: "Connecting alice…" },
      { kind: "status", text: "Connected to node 1" },
      { kind: "outgoing", text: "hello bob" },
      { kind: "sendack", text: "SENDACK success · seq 4" },
    ],
  );
});

test("a connected session refuses a second connection instead of replacing its SDK socket", async () => {
  let provisions = 0;
  const bff: QuickstartBff = {
    async provisionIdentity(uid) {
      provisions += 1;
      return {
        uid,
        token: "dev-token-alice",
        websocketUrl: "ws://127.0.0.1:5200",
      };
    },
  };
  const session = new QuickstartSession({
    uid: "alice",
    peerUid: "bob",
    bff,
    runtime: new FakeChatRuntime(),
  });

  await session.connect();
  await assert.rejects(() => session.connect(), /disconnect the current SDK session/);
  assert.equal(provisions, 1);
  assert.equal(session.snapshot().connection, "connected");
});

test("a disconnected session reconnects and labels only unseen messages as recovered", async () => {
  const bff: QuickstartBff = {
    async provisionIdentity(uid) {
      return {
        uid,
        token: "dev-token-alice",
        websocketUrl: "ws://127.0.0.1:5200",
      };
    },
  };
  const runtime = new FakeChatRuntime();
  const session = new QuickstartSession({
    uid: "alice",
    peerUid: "bob",
    bff,
    runtime,
  });
  const realtime: RuntimeMessage = {
    messageId: "message-1",
    messageSeq: 1,
    clientMsgNo: "client-1",
    fromUid: "bob",
    text: "before disconnect",
  };
  const offline: RuntimeMessage = {
    messageId: "message-2",
    messageSeq: 2,
    clientMsgNo: "client-2",
    fromUid: "bob",
    text: "while offline",
  };
  const ownEarlierMessage: RuntimeMessage = {
    messageId: "message-3",
    messageSeq: 3,
    clientMsgNo: "client-3",
    fromUid: "alice",
    text: "sent before disconnect",
  };

  await session.connect();
  runtime.messageListener?.(realtime);
  await session.disconnect();
  runtime.syncResult = [realtime, ownEarlierMessage, offline];
  await session.reconnectAndSync();

  const snapshot = session.snapshot();
  assert.equal(snapshot.connection, "connected");
  assert.deepEqual(
    snapshot.events
      .filter(({ kind }) => kind === "received" || kind === "synced")
      .map(({ kind, text }) => ({ kind, text })),
    [
      { kind: "received", text: "bob · realtime · before disconnect" },
      { kind: "synced", text: "bob · recovered · while offline" },
    ],
  );
});

test("the UI can observe session state and unsubscribe", async () => {
  const bff: QuickstartBff = {
    async provisionIdentity(uid) {
      return {
        uid,
        token: "dev-token-alice",
        websocketUrl: "ws://127.0.0.1:5200",
      };
    },
  };
  const runtime = new FakeChatRuntime();
  const session = new QuickstartSession({
    uid: "alice",
    peerUid: "bob",
    bff,
    runtime,
  });
  const observed: string[] = [];

  const unsubscribe = session.subscribe((snapshot) => {
    observed.push(snapshot.connection);
  });
  await session.connect();
  unsubscribe();
  await session.disconnect();

  assert.deepEqual(observed, ["idle", "connecting", "connected"]);
});
