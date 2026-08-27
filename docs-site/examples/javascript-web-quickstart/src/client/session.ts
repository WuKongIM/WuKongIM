export interface DevelopmentIdentity {
  uid: string;
  token: string;
  websocketUrl: string;
}

export interface QuickstartBff {
  provisionIdentity(uid: string): Promise<DevelopmentIdentity>;
}

export interface RuntimeMessage {
  messageId: string;
  messageSeq: number;
  clientMsgNo: string;
  fromUid: string;
  text: string;
}

export interface SendAcknowledgement {
  clientSeq: number;
  messageSeq: number;
  reasonCode: number;
}

/** Narrow adapter implemented by the pinned JavaScript SDK runtime. */
export interface ChatRuntime {
  connect(identity: DevelopmentIdentity): Promise<{ nodeId?: number }>;
  disconnect(): Promise<void>;
  sendText(peerUid: string, text: string): Promise<{ clientSeq: number }>;
  syncMessages(peerUid: string): Promise<RuntimeMessage[]>;
  onMessage(listener: (message: RuntimeMessage) => void): () => void;
  onSendAcknowledgement(
    listener: (ack: SendAcknowledgement) => void,
  ): () => void;
  onUnexpectedDisconnect(listener: (reasonCode?: number) => void): () => void;
}

export type ConnectionState =
  | "idle"
  | "connecting"
  | "connected"
  | "disconnected"
  | "failed";

export type SessionEventKind =
  | "status"
  | "outgoing"
  | "sendack"
  | "received"
  | "synced"
  | "error";

export interface SessionEvent {
  id: number;
  kind: SessionEventKind;
  text: string;
}

export interface SessionSnapshot {
  uid: string;
  peerUid: string;
  connection: ConnectionState;
  nodeId?: number;
  events: SessionEvent[];
}

interface QuickstartSessionOptions {
  uid: string;
  peerUid: string;
  bff: QuickstartBff;
  runtime: ChatRuntime;
  maxEvents?: number;
}

/** Coordinates one SDK singleton inside one browser browsing context. */
export class QuickstartSession {
  readonly #uid: string;
  readonly #peerUid: string;
  readonly #bff: QuickstartBff;
  readonly #runtime: ChatRuntime;
  readonly #maxEvents: number;
  #connection: ConnectionState = "idle";
  #nodeId?: number;
  #nextEventId = 1;
  #events: SessionEvent[] = [];
  #seenMessages = new Set<string>();
  #subscribers = new Set<(snapshot: SessionSnapshot) => void>();

  constructor(options: QuickstartSessionOptions) {
    this.#uid = options.uid;
    this.#peerUid = options.peerUid;
    this.#bff = options.bff;
    this.#runtime = options.runtime;
    this.#maxEvents = options.maxEvents ?? 40;

    this.#runtime.onSendAcknowledgement((ack) => {
      const result = ack.reasonCode === 1 ? "success" : `failed (${ack.reasonCode})`;
      this.#append("sendack", `SENDACK ${result} · seq ${ack.messageSeq}`);
    });
    this.#runtime.onMessage((message) => {
      if (message.fromUid !== this.#peerUid || !this.#remember(message)) {
        return;
      }
      this.#append(
        "received",
        `${message.fromUid} · realtime · ${message.text}`,
      );
    });
    this.#runtime.onUnexpectedDisconnect((reasonCode) => {
      this.#connection = "disconnected";
      this.#nodeId = undefined;
      this.#append(
        "status",
        reasonCode === undefined
          ? "Connection lost"
          : `Connection lost · reason ${reasonCode}`,
      );
    });
  }

  async connect(): Promise<void> {
    if (this.#connection === "connecting" || this.#connection === "connected") {
      throw new Error("disconnect the current SDK session before connecting again");
    }
    this.#connection = "connecting";
    this.#append("status", `Connecting ${this.#uid}…`);
    try {
      const identity = await this.#bff.provisionIdentity(this.#uid);
      const connection = await this.#runtime.connect(identity);
      this.#connection = "connected";
      this.#nodeId = connection.nodeId;
      this.#append(
        "status",
        connection.nodeId === undefined
          ? "Connected"
          : `Connected to node ${connection.nodeId}`,
      );
    } catch (error) {
      this.#connection = "failed";
      this.#append("error", error instanceof Error ? error.message : "Connection failed");
      throw error;
    }
  }

  async sendText(text: string): Promise<void> {
    if (this.#connection !== "connected") {
      throw new Error("connect before sending a message");
    }
    const value = text.trim();
    if (value === "") {
      throw new Error("message text is required");
    }
    await this.#runtime.sendText(this.#peerUid, value);
    this.#append("outgoing", value);
  }

  async disconnect(): Promise<void> {
    try {
      await this.#runtime.disconnect();
      this.#connection = "disconnected";
      this.#nodeId = undefined;
      this.#append("status", "Disconnected manually");
    } catch (error) {
      this.#connection = "failed";
      this.#nodeId = undefined;
      this.#append(
        "error",
        error instanceof Error ? error.message : "Transport close was not confirmed",
      );
      throw error;
    }
  }

  async reconnectAndSync(): Promise<void> {
    await this.connect();
    try {
      const messages = await this.#runtime.syncMessages(this.#peerUid);
      let recovered = 0;
      for (const message of messages) {
        if (message.fromUid !== this.#peerUid || !this.#remember(message)) {
          continue;
        }
        recovered += 1;
        this.#append(
          "synced",
          `${message.fromUid} · recovered · ${message.text}`,
        );
      }
      this.#append("status", `Sync complete · ${recovered} recovered`);
    } catch (error) {
      this.#append(
        "error",
        error instanceof Error ? error.message : "Message sync failed",
      );
      throw error;
    }
  }

  snapshot(): SessionSnapshot {
    return {
      uid: this.#uid,
      peerUid: this.#peerUid,
      connection: this.#connection,
      nodeId: this.#nodeId,
      events: this.#events.map((event) => ({ ...event })),
    };
  }

  subscribe(listener: (snapshot: SessionSnapshot) => void): () => void {
    this.#subscribers.add(listener);
    listener(this.snapshot());
    return () => {
      this.#subscribers.delete(listener);
    };
  }

  #append(kind: SessionEventKind, text: string): void {
    this.#events.push({ id: this.#nextEventId++, kind, text });
    if (this.#events.length > this.#maxEvents) {
      this.#events.splice(0, this.#events.length - this.#maxEvents);
    }
    const snapshot = this.snapshot();
    for (const subscriber of this.#subscribers) {
      subscriber(snapshot);
    }
  }

  #remember(message: RuntimeMessage): boolean {
    const key =
      message.messageId ||
      message.clientMsgNo ||
      `${message.fromUid}:${message.messageSeq}`;
    if (this.#seenMessages.has(key)) return false;
    this.#seenMessages.add(key);
    return true;
  }
}
